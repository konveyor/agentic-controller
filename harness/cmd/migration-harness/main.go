package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/konveyor/migration-harness/internal/acp"
	"github.com/konveyor/migration-harness/internal/config"
	"github.com/konveyor/migration-harness/internal/git"
	"github.com/konveyor/migration-harness/internal/goose"
	"github.com/konveyor/migration-harness/internal/hub"
	"github.com/konveyor/migration-harness/internal/logging"
	"github.com/konveyor/migration-harness/internal/prompt"
	"github.com/konveyor/migration-harness/internal/tee"
	"github.com/konveyor/migration-harness/internal/watcher"
)

var rootCmd = &cobra.Command{
	Use:   "migration-harness",
	Short: "Thin git plumbing wrapper for goose-based migration stages",
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a single migration stage (plan, execute, or verify)",
	RunE:  runStage,
}

func init() {
	rootCmd.AddCommand(runCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runStage(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 1. Load config from env
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// 2. Resolve app info + git creds from Hub
	cloneDir := os.Getenv("HARNESS_WORK_DIR")
	if cloneDir == "" {
		cloneDir = "/workspace/repo"
	}

	// Fail-closed token revocation: always register cleanup when a valid
	// token ID exists. The defer decides at exit time whether to actually
	// revoke — only an intermediate workflow stage that succeeded skips
	// revocation (the next stage needs the token). Every other exit path
	// (failure, last stage, standalone run) revokes immediately.
	hubClient := hub.NewClient(cfg.HubBaseURL, cfg.HubToken)
	var stageSucceeded bool
	if tokenID, ok := parseHubTokenID(cfg); ok {
		intermediate := isIntermediateWorkflowStage(cfg)
		defer func() {
			if intermediate && stageSucceeded {
				logging.Info("intermediate workflow stage succeeded — deferring token revocation to next stage")
				return
			}
			if intermediate {
				logging.Warn("intermediate workflow stage failed — revoking token early (no subsequent stage will run)")
			}
			if err := hubClient.RevokeToken(tokenID); err != nil {
				logging.Warn("hub token revocation (id=%d): %v", tokenID, err)
			} else {
				logging.Ok("hub token revoked (id=%d)", tokenID)
			}
		}()
	} else if cfg.HubTokenID == "" && cfg.HubToken != "" {
		logging.Warn("HUB_TOKEN_ID not set — skipping token revocation (token will expire via TTL)")
	} else if cfg.HubTokenID != "" {
		logging.Warn("HUB_TOKEN_ID %q is not a valid numeric ID — skipping token revocation", cfg.HubTokenID)
	}

	creds, err := resolveFromHub(cfg, hubClient)
	if err != nil {
		return fmt.Errorf("hub resolution: %w", err)
	}

	if cfg.TargetBranch == creds.Branch {
		return fmt.Errorf("TARGET_BRANCH %q must differ from source branch", cfg.TargetBranch)
	}
	creds.Branch = cfg.TargetBranch

	// 3. Clone, strip creds, checkout branch
	logging.Header("Git Setup")
	logging.Info("cloning %s...", creds.RepoURL)

	repo, err := git.Clone(ctx, creds, cloneDir)
	if err != nil {
		return fmt.Errorf("clone: %w", err)
	}

	if err := git.ConfigureAuthor(repo); err != nil {
		return fmt.Errorf("configure git author: %w", err)
	}

	if err := git.StripCredentials(repo); err != nil {
		return fmt.Errorf("strip credentials: %w", err)
	}
	hub.ClearEnv()

	if err := git.CheckoutBranch(repo, creds.Branch); err != nil {
		return fmt.Errorf("checkout branch %s: %w", creds.Branch, err)
	}
	logging.Ok("cloned to %s, branch %s", cloneDir, creds.Branch)

	// Base of this stage run: everything past this commit is work the run
	// produced. Push compares HEAD against it so runs that produce no
	// commits do not create empty remote branches.
	baseSHA, err := git.HeadSHA(repo)
	if err != nil {
		// Fail open — an unknown base must never block a push of real work.
		logging.Warn("resolve base commit: %v", err)
	}

	// 4. Discover skills early — controls which setup steps run
	skillPaths, err := discoverSkills()
	if err != nil {
		return fmt.Errorf("discover skills: %w", err)
	}
	hasSkills := len(skillPaths) > 0

	if hasSkills {
		if err := git.EnsureGitignore(cloneDir, []string{
			"graphify-out/",
			".goose/",
			"__pycache__/",
			"node_modules/",
			"target/",
			"*.tmp",
			"*.swp",
			"*.bak",
		}); err != nil {
			logging.Warn("gitignore: %v", err)
		}

		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir: %w", err)
		}
		if err := symlinkSkillsDir(home, skillsDir()); err != nil {
			return fmt.Errorf("skill symlink: %w", err)
		}
		logging.Ok("symlinked %s/.agents/skills → %s", home, skillsDir())
	}

	if hasSkills {
		// 4b. Write analysis to workspace (if resolved from Hub)
		wroteAnalysis, err := fetchAndWriteAnalysis(hubClient, cfg.AppID, cloneDir)
		if err != nil {
			logging.Warn("analysis fetch: %v", err)
		}

		// 4c. Commit harness-managed files so they survive on the branch.
		// Only commit when there is actual grounding data (analysis.json);
		// .gitignore patterns take effect locally without a commit.
		if wroteAnalysis {
			if err := git.CommitFiles(repo, []string{
				".gitignore",
				".konveyor/analysis.json",
			}, "harness: add grounding data"); err != nil {
				return fmt.Errorf("commit harness files: %w", err)
			}
		}
	}

	// 5. Start goose serve. With the ACP tee (default) goose binds
	// loopback on :4001 and the harness owns the pod's :4000 endpoint;
	// with HARNESS_ACP_TEE=off goose takes :4000 itself as before.
	logging.Header("Goose Setup")
	goosePort := 0
	if cfg.ACPTee {
		goosePort = goose.LoopbackACPPort
	}
	srv, err := goose.StartServe(ctx, goose.ServeConfig{
		Port:         goosePort,
		BindLoopback: cfg.ACPTee,
		SecretKey:    cfg.ACPSecretKey,
		Provider:     cfg.Provider,
		Model:        cfg.Model,
		APIKey:       cfg.APIKey,
		Endpoint:     cfg.Endpoint,
	})
	if err != nil {
		return fmt.Errorf("start goose serve: %w", err)
	}
	defer srv.Stop()

	// 6. Connect ACP, create session
	wsClient, err := acp.WaitReadyDial(ctx, "127.0.0.1", srv.Port(), srv.SecretKey(), 30*time.Second)
	if err != nil {
		return fmt.Errorf("connect to goose: %w", err)
	}
	defer wsClient.Close()

	session := acp.NewSessionClient(wsClient)
	sessionID, err := session.CreateSession(ctx, cloneDir, nil)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// 6b. Expose the run: tee listener on the pod ACP port. Viewers get
	// a verbatim pipe to goose plus the run session's live stream —
	// message/thought chunks, tool calls, usage — and may redirect the
	// run (steer/cancel) unless HARNESS_HITL_STEER=off. Permission asks
	// are offered to whoever is watching. Failure here never fails the
	// run — it only loses live viewers.
	var teeSrv *tee.Server
	if cfg.ACPTee {
		t := tee.New(tee.Config{
			SecretKey:    cfg.ACPSecretKey,
			UpstreamAddr: fmt.Sprintf("127.0.0.1:%d", srv.Port()),
			HITLTimeout:  cfg.HITLTimeout,
			SteerEnabled: cfg.HITLSteer,
		})
		if err := t.Start(goose.DefaultACPPort); err != nil {
			logging.Warn("ACP tee: %v — run continues without live viewers", err)
		} else {
			defer t.Stop()
			t.AttachRun(wsClient, sessionID)
			session.SetPermissionForwarder(t)
			teeSrv = t
			logging.Ok("ACP tee on :%d (goose loopback :%d, viewer steering %s)",
				goose.DefaultACPPort, srv.Port(), map[bool]string{true: "on", false: "off"}[cfg.HITLSteer])
		}
	}

	// Harness lifecycle → viewer status frames, in standard ACP
	// vocabulary. Everything is a no-op without a live tee.
	emitPlan := func(prep, agentRun, finish string) {
		if teeSrv == nil {
			return
		}
		entry := func(content, status string) map[string]any {
			return map[string]any{"content": content, "priority": "medium", "status": status}
		}
		teeSrv.EmitRunUpdate(map[string]any{
			"sessionUpdate": "plan",
			"entries": []map[string]any{
				entry("Prepare workspace: clone, branch, grounding data", prep),
				entry("Agent works the stage task", agentRun),
				entry(fmt.Sprintf("Push results to branch %s", creds.Branch), finish),
			},
		})
	}
	var pushSeq atomic.Int64
	emitPush := func(title string, fn func() (bool, error)) (bool, error) {
		if teeSrv == nil {
			return fn()
		}
		id := fmt.Sprintf("harness-push-%d", pushSeq.Add(1))
		teeSrv.EmitRunUpdate(map[string]any{
			"sessionUpdate": "tool_call", "toolCallId": id, "title": title,
			"kind": "execute", "status": "in_progress",
		})
		pushed, err := fn()
		status := "completed"
		if err != nil {
			status = "failed"
		}
		teeSrv.EmitRunUpdate(map[string]any{
			"sessionUpdate": "tool_call_update", "toolCallId": id, "status": status,
		})
		return pushed, err
	}
	emitNotice := func(format string, args ...any) {
		if teeSrv == nil {
			return
		}
		teeSrv.EmitRunNotice(fmt.Sprintf(format, args...))
	}

	// Workspace prep all happened before the tee existed; publish it as
	// already done so a viewer's first glance shows the ladder.
	emitPlan("completed", "pending", "pending")

	// 7. Build prompt from context layers
	stagePrompt := prompt.Build(prompt.Layers{
		AgentPrompt:   cfg.AgentPrompt,
		WorkflowGuide: cfg.WorkflowGuide,
		StageTask:     cfg.StageInstructions,
	})

	// 8. Start filesystem watcher BEFORE blocking prompt
	pushFn := func() error {
		_, err := emitPush("git push (auto-commit watcher)", func() (bool, error) {
			return git.Push(ctx, creds, repo, creds.Branch, baseSHA)
		})
		return err
	}
	w, err := watcher.New(cloneDir, pushFn)
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	if err := w.Start(ctx); err != nil {
		return fmt.Errorf("start watcher: %w", err)
	}
	defer w.Stop()

	// 9. Send single ACP prompt (blocks until goose finishes or MaxTurns is hit)
	logging.Header("Running Stage")
	logging.Info("max turns: %d", cfg.MaxTurns)
	emitPlan("completed", "in_progress", "pending")
	if teeSrv != nil {
		teeSrv.SetRunActive(true)
	}
	promptResult, err := session.SendPrompt(ctx, sessionID, []acp.ContentBlock{
		{Type: "text", Text: stagePrompt},
	}, cfg.MaxTurns)
	if teeSrv != nil {
		teeSrv.SetRunActive(false)
	}

	// A viewer's session/cancel surfaces as a clean stop with
	// stopReason=cancelled — a deliberate human abort, not a success.
	cancelled := err == nil && promptResult != nil && promptResult.StopReason == "cancelled"
	if cancelled {
		logging.Warn("run cancelled by an attached viewer")
	}
	if err != nil {
		logging.Err("prompt failed: %v", err)
	}
	emitPlan("completed", "completed", "in_progress")

	// 10. Check goose health
	if !srv.Alive() {
		logging.Err("goose serve crashed")
	}

	// 11. Check for uncommitted work
	if wt, err := repo.Worktree(); err == nil {
		if st, err := wt.Status(); err == nil && !st.IsClean() {
			logging.Warn("worktree dirty at stage end — agent left %d uncommitted paths", len(st))
		}
	}

	// 12. Stop watcher before final push
	w.Stop()

	// 13. Determine exit status from ACP/goose signals
	stageFailed := err != nil || !srv.Alive() || cancelled

	// 14. Final push (use a fresh context — the signal context may
	// already be cancelled after SIGINT)
	logging.Header("Final Push")
	pushCtx, pushCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer pushCancel()
	pushed, err := emitPush("git push (final)", func() (bool, error) {
		return git.Push(pushCtx, creds, repo, creds.Branch, baseSHA)
	})
	if err != nil {
		emitNotice("stage failed — final push error: %v", err)
		return fmt.Errorf("final push: %w", err)
	}
	emitPlan("completed", "completed", "completed")

	// 15. Exit. pushed is false when the run produced no commits, so the
	// notices must not claim work landed on the branch.
	if stageFailed {
		switch {
		case cancelled && pushed:
			emitNotice("run cancelled by viewer — partial work pushed to branch %s", creds.Branch)
		case cancelled:
			emitNotice("run cancelled by viewer — no commits to push")
		case pushed:
			emitNotice("stage failed — partial work pushed to branch %s", creds.Branch)
		default:
			emitNotice("stage failed — no commits to push")
		}
		logging.Err("stage failed")
		return fmt.Errorf("stage failed")
	}
	stageSucceeded = true
	if pushed {
		emitNotice("stage succeeded — results pushed to branch %s", creds.Branch)
	} else {
		emitNotice("stage succeeded — no changes to push")
	}
	logging.Ok("stage succeeded")
	return nil
}

func symlinkSkillsDir(homeDir, skillsSrc string) error {
	skillsSrc, err := filepath.Abs(skillsSrc)
	if err != nil {
		return fmt.Errorf("resolve skills source: %w", err)
	}

	agentsDir := filepath.Join(homeDir, ".agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return err
	}

	link := filepath.Join(agentsDir, "skills")
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Readlink(link); err == nil && target == skillsSrc {
				return nil
			}
			if err := os.Remove(link); err != nil {
				return fmt.Errorf("remove stale symlink %s: %w", link, err)
			}
		} else {
			return fmt.Errorf("%s already exists and is not a symlink", link)
		}
	}

	return os.Symlink(skillsSrc, link)
}

const defaultSkillsDir = "/opt/skills"

func skillsDir() string {
	if v := os.Getenv("HARNESS_SKILLS_DIR"); v != "" {
		return v
	}
	return defaultSkillsDir
}

func discoverSkills() ([]string, error) {
	pattern := filepath.Join(skillsDir(), "*/SKILL.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		logging.Info("no skills found at %s — proceeding without skills", pattern)
		return nil, nil
	}

	for _, m := range matches {
		logging.Info("discovered skill: %s", m)
	}
	return matches, nil
}

func resolveFromHub(cfg *config.Config, hubClient *hub.Client) (*git.Credentials, error) {
	logging.Header("Hub Resolution")

	appID, err := hub.ParseAppID(cfg.AppID)
	if err != nil {
		return nil, fmt.Errorf("invalid APP_ID %q: %w", cfg.AppID, err)
	}

	app, err := hubClient.FetchApp(appID)
	if err != nil {
		return nil, fmt.Errorf("fetch app: %w", err)
	}
	logging.Ok("app: %s (id=%d), repo: %s", app.Name, app.ID, app.Repository.URL)

	identity, err := hubClient.FetchGitCreds(appID)
	if err != nil {
		return nil, fmt.Errorf("fetch git creds: %w", err)
	}

	creds := &git.Credentials{
		RepoURL: app.Repository.URL,
		Branch:  app.Repository.Branch,
	}
	if identity != nil {
		creds.Username = identity.User
		creds.Token = identity.Password
		if creds.Username == "" {
			creds.Username = "x-access-token"
		}
		logging.Ok("git identity: %s", identity.Name)
	}

	return creds, nil
}

// parseHubTokenID extracts the Hub API token ID from config.
// Returns (0, false) when no valid token ID is available.
func parseHubTokenID(cfg *config.Config) (uint, bool) {
	if cfg.HubTokenID == "" {
		return 0, false
	}
	tokenID, err := strconv.ParseUint(cfg.HubTokenID, 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(tokenID), true
}

// isIntermediateWorkflowStage reports whether the harness is running an
// intermediate (not last) stage of a multi-stage workflow. Returns false
// for standalone runs, last stages, and invalid metadata.
func isIntermediateWorkflowStage(cfg *config.Config) bool {
	if cfg.WorkflowStage == "" || cfg.WorkflowStageCount == "" {
		return false
	}
	stage, err := strconv.ParseUint(cfg.WorkflowStage, 10, 64)
	if err != nil || stage == 0 {
		return false
	}
	count, err := strconv.ParseUint(cfg.WorkflowStageCount, 10, 64)
	if err != nil || count == 0 {
		return false
	}
	return stage < count
}

func fetchAndWriteAnalysis(hubClient *hub.Client, appIDStr string, workDir string) (bool, error) {
	appID, err := hub.ParseAppID(appIDStr)
	if err != nil {
		return false, fmt.Errorf("invalid APP_ID %q: %w", appIDStr, err)
	}
	insights, err := hubClient.FetchAnalysis(appID)
	if err != nil {
		return false, err
	}
	if len(insights) == 0 {
		logging.Info("no analysis results for app %s", appIDStr)
		return false, nil
	}

	analysisDir := filepath.Join(workDir, ".konveyor")
	if err := os.MkdirAll(analysisDir, 0o755); err != nil {
		return false, fmt.Errorf("create .konveyor dir: %w", err)
	}

	data, err := json.MarshalIndent(insights, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal analysis: %w", err)
	}

	analysisPath := filepath.Join(analysisDir, "analysis.json")
	if err := os.WriteFile(analysisPath, data, 0o644); err != nil {
		return false, fmt.Errorf("write analysis: %w", err)
	}

	logging.Ok("wrote %d analysis insights to %s", len(insights), analysisPath)
	return true, nil
}
