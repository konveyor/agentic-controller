package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
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

	// Stage-aware token revocation: register cleanup before Hub resolution
	// so the token is revoked even if resolveFromHub fails partway.
	hubClient := hub.NewClient(cfg.HubBaseURL, cfg.HubToken)
	if tokenID, revoke := shouldRevokeToken(cfg); revoke {
		defer func() {
			if err := hubClient.RevokeToken(tokenID); err != nil {
				logging.Warn("hub token revocation (id=%d): %v", tokenID, err)
			} else {
				logging.Ok("hub token revoked (id=%d)", tokenID)
			}
		}()
	} else if cfg.HubTokenID == "" && cfg.HubToken != "" {
		logging.Warn("HUB_TOKEN_ID not set — skipping token revocation (token will expire via TTL)")
	} else if cfg.HubTokenID != "" {
		stage, sErr := strconv.ParseUint(cfg.WorkflowStage, 10, 64)
		count, cErr := strconv.ParseUint(cfg.WorkflowStageCount, 10, 64)
		if sErr == nil && cErr == nil && stage > 0 && count > 0 {
			logging.Info("workflow stage %d/%d — skipping token revocation", stage, count)
		} else {
			logging.Warn("invalid workflow metadata (stage=%q, count=%q) — skipping token revocation", cfg.WorkflowStage, cfg.WorkflowStageCount)
		}
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

	// 4. Discover skills early — controls which setup steps run
	skillContent, skillPaths, err := discoverSkills()
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
	}

	if hasSkills {
		// 4b. Write analysis to workspace (if resolved from Hub)
		if err := fetchAndWriteAnalysis(hubClient, cfg.AppID, cloneDir); err != nil {
			logging.Warn("analysis fetch: %v", err)
		}

		// 4c. Commit harness-managed files so they survive on the branch
		if err := git.CommitFiles(repo, []string{
			".gitignore",
			".konveyor/analysis.json",
		}, "harness: add grounding data"); err != nil {
			return fmt.Errorf("commit harness files: %w", err)
		}
	}

	// 5. Start goose serve
	logging.Header("Goose Setup")
	srv, err := goose.StartServe(ctx, 0, cfg.ACPSecretKey, cfg.Provider, cfg.Model, cfg.APIKey, cfg.Endpoint)
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

	// 7. Build prompt from context layers
	stagePrompt := prompt.Build(prompt.Layers{
		AgentPrompt:   cfg.AgentPrompt,
		WorkflowGuide: cfg.WorkflowGuide,
		Skill:         skillContent,
		StageTask:     cfg.StageInstructions,
	})

	// 8. Start filesystem watcher BEFORE blocking prompt
	pushFn := func() error {
		return git.Push(ctx, creds, repo, creds.Branch)
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
	_, err = session.SendPrompt(ctx, sessionID, []acp.ContentBlock{
		{Type: "text", Text: stagePrompt},
	}, cfg.MaxTurns)

	if err != nil {
		logging.Err("prompt failed: %v", err)
	}

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
	stageFailed := err != nil || !srv.Alive()

	// 14. Final push (use a fresh context — the signal context may
	// already be cancelled after SIGINT)
	logging.Header("Final Push")
	pushCtx, pushCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer pushCancel()
	if err := git.Push(pushCtx, creds, repo, creds.Branch); err != nil {
		return fmt.Errorf("final push: %w", err)
	}

	// 15. Exit
	if stageFailed {
		logging.Err("stage failed")
		return fmt.Errorf("stage failed")
	}
	logging.Ok("stage succeeded")
	return nil
}

const defaultSkillsDir = "/opt/skills"

func skillsDir() string {
	if v := os.Getenv("HARNESS_SKILLS_DIR"); v != "" {
		return v
	}
	return defaultSkillsDir
}

func discoverSkills() (string, []string, error) {
	pattern := filepath.Join(skillsDir(), "*/SKILL.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", nil, err
	}
	if len(matches) == 0 {
		logging.Info("no skills found at %s — proceeding without skills", pattern)
		return "", nil, nil
	}

	var combined strings.Builder
	for i, m := range matches {
		content, err := os.ReadFile(m)
		if err != nil {
			return "", nil, fmt.Errorf("read skill %s: %w", m, err)
		}
		logging.Info("discovered skill: %s", m)
		if i > 0 {
			combined.WriteString("\n\n---\n\n")
		}
		combined.Write(content)
	}
	return combined.String(), matches, nil
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

// shouldRevokeToken decides whether the harness should revoke the Hub API
// token on exit and returns the parsed token ID. Standalone AgentRuns
// always revoke. Workflow stages revoke only on the last stage so
// subsequent stages can reuse the token.
func shouldRevokeToken(cfg *config.Config) (uint, bool) {
	if cfg.HubTokenID == "" {
		return 0, false
	}
	tokenID, err := strconv.ParseUint(cfg.HubTokenID, 10, 64)
	if err != nil {
		return 0, false
	}
	if cfg.WorkflowStage == "" && cfg.WorkflowStageCount == "" {
		return uint(tokenID), true
	}
	stage, err := strconv.ParseUint(cfg.WorkflowStage, 10, 64)
	if err != nil || stage == 0 {
		return 0, false
	}
	count, err := strconv.ParseUint(cfg.WorkflowStageCount, 10, 64)
	if err != nil || count == 0 {
		return 0, false
	}
	if stage == count {
		return uint(tokenID), true
	}
	return 0, false
}

func fetchAndWriteAnalysis(hubClient *hub.Client, appIDStr string, workDir string) error {
	appID, err := hub.ParseAppID(appIDStr)
	if err != nil {
		return fmt.Errorf("invalid APP_ID %q: %w", appIDStr, err)
	}
	insights, err := hubClient.FetchAnalysis(appID)
	if err != nil {
		return err
	}
	if len(insights) == 0 {
		logging.Info("no analysis results for app %s", appIDStr)
		return nil
	}

	analysisDir := filepath.Join(workDir, ".konveyor")
	if err := os.MkdirAll(analysisDir, 0o755); err != nil {
		return fmt.Errorf("create .konveyor dir: %w", err)
	}

	data, err := json.MarshalIndent(insights, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal analysis: %w", err)
	}

	analysisPath := filepath.Join(analysisDir, "analysis.json")
	if err := os.WriteFile(analysisPath, data, 0o644); err != nil {
		return fmt.Errorf("write analysis: %w", err)
	}

	logging.Ok("wrote %d analysis insights to %s", len(insights), analysisPath)
	return nil
}
