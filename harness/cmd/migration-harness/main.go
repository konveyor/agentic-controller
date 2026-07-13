package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/spf13/cobra"

	"github.com/konveyor/migration-harness/internal/config"
	"github.com/konveyor/migration-harness/internal/detect"
	"github.com/konveyor/migration-harness/internal/execute"
	"github.com/konveyor/migration-harness/internal/fixloop"
	"github.com/konveyor/migration-harness/internal/git"
	"github.com/konveyor/migration-harness/internal/goose"
	"github.com/konveyor/migration-harness/internal/handoff"
	"github.com/konveyor/migration-harness/internal/logging"
	"github.com/konveyor/migration-harness/internal/metrics"
	"github.com/konveyor/migration-harness/internal/plan"
	"github.com/konveyor/migration-harness/internal/rundir"
	"github.com/konveyor/migration-harness/internal/verify"
)

var rootCmd = &cobra.Command{
	Use:   "migration-harness",
	Short: "AI-powered code migration CLI",
	Long:  "migration-harness orchestrates LLM agents through a 5-step pipeline (detect, plan, execute, verify, fix-loop) to automate enterprise code migrations.",
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Configure migration-harness",
	RunE:  runInit,
}

var runCmd = &cobra.Command{
	Use:   "run [repo-or-url] [request]",
	Short: "Run a full migration pipeline",
	Args:  cobra.MaximumNArgs(2),
	RunE:  runMigration,
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of the latest migration run",
	RunE:  runStatus,
}

var resumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Resume an incomplete migration",
	RunE:  runResume,
}

var stepCmd = &cobra.Command{
	Use:   "step <name> <repo>",
	Short: "Run a single pipeline step",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runStep,
}

func init() {
	runCmd.Flags().BoolVar(&plan.AutoApprove, "auto-approve", false, "Skip interactive plan approval")
	rootCmd.AddCommand(initCmd, runCmd, statusCmd, resumeCmd, stepCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func checkDeps() error {
	for _, dep := range []string{"goose", "graphify"} {
		if _, err := exec.LookPath(dep); err != nil {
			return fmt.Errorf("required command not found: %s", dep)
		}
	}
	return nil
}

func runInit(cmd *cobra.Command, args []string) error {
	logging.Header("Migration Harness Setup")

	if err := checkDeps(); err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)

	provider := promptWithDefault(reader, "LLM provider (e.g. anthropic, gcp_vertex_ai, openai)", "anthropic")
	model := promptWithDefault(reader, "Model name", "claude-sonnet-4-5")
	maxTurnsStr := promptWithDefault(reader, "Max turns per step", strconv.Itoa(config.DefaultMaxTurns))
	maxFixStr := promptWithDefault(reader, "Max fix iterations", strconv.Itoa(config.DefaultMaxFixIterations))

	maxTurns, err := strconv.Atoi(maxTurnsStr)
	if err != nil {
		maxTurns = config.DefaultMaxTurns
	}
	maxFix, err := strconv.Atoi(maxFixStr)
	if err != nil {
		maxFix = config.DefaultMaxFixIterations
	}

	cfg := &config.Config{
		Model:            model,
		Provider:         provider,
		MaxTurns:         maxTurns,
		MaxFixIterations: maxFix,
	}

	path := config.DefaultConfigPath()
	if err := config.Save(path, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	logging.Ok("Config saved to %s", path)
	return nil
}

func promptWithDefault(reader *bufio.Reader, prompt, defaultVal string) string {
	fmt.Fprintf(os.Stderr, "%s [%s]: ", prompt, defaultVal)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

func runMigration(cmd *cobra.Command, args []string) error {
	var repoArg, request string
	switch len(args) {
	case 2:
		repoArg = args[0]
		request = args[1]
	case 1:
		repoArg = args[0]
	}
	if request == "" {
		request = os.Getenv("KONVEYOR_INSTRUCTIONS")
	}
	if request == "" {
		return fmt.Errorf("request is required: pass as argument or set KONVEYOR_INSTRUCTIONS")
	}
	if repoArg == "" {
		repoArg = os.Getenv("GIT_REPO_URL")
	}
	if repoArg == "" {
		return fmt.Errorf("repo is required: pass as argument or set GIT_REPO_URL")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cfgPath := config.DefaultConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config (run 'migration-harness init' first): %w", err)
	}

	tracker := metrics.NewTracker()

	creds, err := git.ReadFromEnv()
	if err != nil {
		return fmt.Errorf("git credentials: %w", err)
	}

	var workDir string
	var repo *gogit.Repository

	if creds != nil {
		tracker.StartStep("git-clone")
		logging.Header("Git Setup")
		logging.Info("cloning %s...", creds.RepoURL)

		tmpBase := os.Getenv("HARNESS_WORK_DIR")
		if tmpBase == "" {
			tmpBase = os.TempDir()
		}
		workDir, err = os.MkdirTemp(tmpBase, "migration-harness-"+filepath.Base(creds.RepoURL)+"-*")
		if err != nil {
			return fmt.Errorf("create temp dir: %w", err)
		}
		repo, err = git.Clone(ctx, creds, workDir)
		if err != nil {
			return fmt.Errorf("clone: %w", err)
		}

		if err := git.StripCredentials(repo); err != nil {
			return fmt.Errorf("strip credentials: %w", err)
		}

		git.ClearEnvCredentials()

		if err := git.CheckoutBranch(repo, creds.Branch); err != nil {
			return fmt.Errorf("checkout branch: %w", err)
		}

		logging.Ok("cloned to %s, branch %s", workDir, creds.Branch)
		tracker.EndStep()
	} else {
		workDir = repoArg
		if _, err := os.Stat(workDir); err != nil {
			return fmt.Errorf("repo path does not exist: %s", workDir)
		}
	}

	runsDir := rundir.DefaultRunsDir()
	runDir, err := rundir.New(runsDir, filepath.Base(workDir))
	if err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	logging.Info("run directory: %s", runDir)

	installDir := findInstallDir()
	skillDir := filepath.Join(installDir, "skill-bundle", "goose-migration")
	recipesDir := filepath.Join(installDir, "recipes")
	logDir := filepath.Join(runDir, "logs")

	runner := goose.NewCLIRunnerFull(cfg.Provider, cfg.Model, cfg.Endpoint, cfg.APIKey, logDir)

	var pushFn func() error
	if creds != nil && repo != nil {
		pushFn = func() error {
			return git.Push(ctx, creds, repo, creds.Branch)
		}
	}

	session := handoff.NewSession(
		generateSessionID(),
		request,
		repoArg,
		branchName(creds),
		cfg.Model,
		cfg.Provider,
	)

	pipelineStatus := "completed"

	syncSession := func(stepMsg string) {
		session.Status = "in_progress"
		if err := handoff.WriteSession(workDir, session); err != nil {
			logging.Warn("write session.json: %v", err)
		}
		commitAndPush(repo, pushFn, stepMsg)
	}

	defer func() {
		session.Status = pipelineStatus

		if err := handoff.WriteSession(workDir, session); err != nil {
			logging.Warn("write session.json: %v", err)
		}

		m := tracker.Generate(pipelineStatus)
		if err := metrics.WriteMetrics(runDir, m); err != nil {
			logging.Warn("write metrics: %v", err)
		}

		if creds != nil && repo != nil {
			if _, err := git.CommitAll(repo, "konveyor: session handoff"); err != nil {
				logging.Warn("commit handoff: %v", err)
			}
			if err := git.Push(ctx, creds, repo, creds.Branch); err != nil {
				logging.Warn("push: %v", err)
			}
		}
	}()

	// Step 1: Detect
	tracker.StartStep("detect")
	detectResult, err := detect.Run(ctx, workDir, runDir)
	if err != nil {
		pipelineStatus = "failed"
		return fmt.Errorf("detect: %w", err)
	}
	session.Pipeline.Detect = &handoff.DetectStatus{
		StepStatus: handoff.StepStatus{
			Status:          "completed",
			DurationSeconds: tracker.StepDuration("detect"),
		},
		Nodes:       detectResult.Graph.Nodes,
		Edges:       detectResult.Graph.Edges,
		Communities: detectResult.Graph.Communities,
	}
	tracker.EndStep()
	syncSession("konveyor: detect complete")

	// Step 2: Plan
	tracker.StartStep("plan")
	p, err := plan.Run(ctx, workDir, runDir, request, skillDir, runner)
	if err != nil {
		pipelineStatus = "failed"
		return fmt.Errorf("plan: %w", err)
	}
	session.Pipeline.Plan = &handoff.PlanStatus{
		StepStatus:   handoff.StepStatus{Status: "completed", DurationSeconds: tracker.StepDuration("plan")},
		ItemsPlanned: len(p.Items),
	}
	tracker.EndStep()
	syncSession("konveyor: plan complete")

	// Step 3: Execute
	tracker.StartStep("execute")
	items, summary, execCommits, err := execute.Run(ctx, workDir, runDir, recipesDir, p, runner, repo, pushFn)
	if err != nil {
		pipelineStatus = "failed"
		return fmt.Errorf("execute: %w", err)
	}
	for _, c := range execCommits {
		session.Commits = append(session.Commits, handoff.CommitRecord{
			SHA: c.SHA, Message: c.Message, Step: c.Step,
		})
	}
	session.Pipeline.Execute = &handoff.ExecuteStatus{
		StepStatus:     handoff.StepStatus{Status: "completed", DurationSeconds: tracker.StepDuration("execute")},
		ItemsSucceeded: summary.Ok,
		ItemsFailed:    summary.Failed,
		ItemsSkipped:   summary.Skipped,
	}
	tracker.EndStep()
	syncSession("konveyor: execute complete")

	// Step 4: Verify
	tracker.StartStep("verify")
	vr, err := verify.Run(ctx, workDir, runDir, recipesDir, p.MigrationType, runner)
	if err != nil {
		logging.Warn("verify: %v", err)
	}
	verifyOk := vr != nil && vr.BuildOk
	session.Pipeline.Verify = &handoff.VerifyStatus{
		StepStatus:  handoff.StepStatus{Status: "completed", DurationSeconds: tracker.StepDuration("verify")},
		BuildOk:     verifyOk,
		TestsPassed: safeTestsPassed(vr),
	}
	tracker.EndStep()
	syncSession("konveyor: verify complete")

	// Step 5: Fix Loop
	tracker.StartStep("fix-loop")
	flReport, flCommits, err := fixloop.Run(ctx, workDir, runDir, recipesDir, p.MigrationType, cfg.MaxFixIterations, runner, repo, pushFn)
	if err != nil {
		logging.Warn("fix-loop: %v", err)
	}
	for _, c := range flCommits {
		session.Commits = append(session.Commits, handoff.CommitRecord{
			SHA: c.SHA, Message: c.Message, Step: c.Iter,
		})
	}
	session.Pipeline.FixLoop = &handoff.FixLoopStatus{
		StepStatus: handoff.StepStatus{Status: fixLoopStatus(flReport)},
		Iterations: fixLoopIterations(flReport),
	}
	tracker.EndStep()

	// Write handoff
	if err := handoff.WriteHandoff(workDir, session, p, items, vr); err != nil {
		logging.Warn("write handoff: %v", err)
	}

	if !verifyOk && (flReport == nil || flReport.Status != "success") {
		pipelineStatus = "partial"
	}

	logging.Header("Migration Complete")
	logging.Ok("status: %s", pipelineStatus)
	logging.Info("run dir: %s", runDir)

	return nil
}

func runStatus(cmd *cobra.Command, args []string) error {
	runsDir := rundir.DefaultRunsDir()
	latest, err := rundir.Latest(runsDir)
	if err != nil {
		return fmt.Errorf("no runs found: %w", err)
	}
	logging.Info("latest run: %s", latest)

	metricsPath := filepath.Join(latest, "metrics.json")
	if data, err := os.ReadFile(metricsPath); err == nil {
		fmt.Fprintln(os.Stderr, string(data))
	} else {
		logging.Warn("no metrics.json found")
	}
	return nil
}

func runResume(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("resume is not yet implemented")
}

func runStep(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("step execution is not yet implemented")
}

func findInstallDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func branchName(creds *git.Credentials) string {
	if creds != nil {
		return creds.Branch
	}
	return ""
}

func safeTestsPassed(vr *verify.VerifyResult) int {
	if vr != nil {
		return vr.TestsPassed
	}
	return 0
}

func fixLoopStatus(r *fixloop.FixLoopReport) string {
	if r != nil {
		return r.Status
	}
	return "skipped"
}

func fixLoopIterations(r *fixloop.FixLoopReport) int {
	if r != nil {
		return r.Iterations
	}
	return 0
}

func commitAndPush(repo *gogit.Repository, pushFn func() error, msg string) {
	if repo == nil {
		return
	}
	hash, err := git.CommitAll(repo, msg)
	if err != nil {
		logging.Warn("commit (%s): %v", msg, err)
		return
	}
	if hash == (plumbing.Hash{}) {
		return
	}
	if pushFn != nil {
		if err := pushFn(); err != nil {
			logging.Warn("push (%s): %v", msg, err)
		}
	}
}

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
