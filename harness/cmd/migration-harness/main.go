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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/konveyor/agentic-controller/api/skill"
	"github.com/konveyor/migration-harness/internal/acp"
	"github.com/konveyor/migration-harness/internal/askuser"
	"github.com/konveyor/migration-harness/internal/config"
	"github.com/konveyor/migration-harness/internal/git"
	"github.com/konveyor/migration-harness/internal/goose"
	"github.com/konveyor/migration-harness/internal/hub"
	"github.com/konveyor/migration-harness/internal/logging"
	"github.com/konveyor/migration-harness/internal/params"
	"github.com/konveyor/migration-harness/internal/prompt"
	"github.com/konveyor/migration-harness/internal/tee"
	"github.com/konveyor/migration-harness/internal/termination"
	"github.com/konveyor/migration-harness/internal/watcher"
)

var rootCmd = &cobra.Command{
	Use:   "migration-harness",
	Short: "Thin git plumbing wrapper for goose-based migration stages",
}

// exitCode carries runStage's exit code out of cobra's RunE closure —
// cobra's own Execute() error handling only distinguishes "no error"
// from "error", which cannot express the harness's three-way exit-code
// contract (ADR 0011: 0 succeeded, 1 failed, 2 limit reached).
var exitCode int

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a single migration stage (plan, execute, or verify)",
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := runStage(cmd, args)
		exitCode = code
		return err
	},
}

// askUserCmd is the stdio MCP server goose spawns for the ask_user tool
// (listed in session/new mcpServers by runStage). It never loads the
// harness config — it only speaks MCP on stdin/stdout.
var askUserCmd = &cobra.Command{
	Use:    askuser.Subcommand,
	Short:  "Serve the ask_user MCP tool over stdio (started by goose, not by hand)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		srv := askuser.New(os.Stdout, func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		})
		err := srv.Serve(ctx, os.Stdin)
		if err == context.Canceled {
			return nil
		}
		return err
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(askUserCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		// Surface the failure message on the pod termination log so the reason
		// reaches the AgentRun's Ready condition, not solely the logs (#143).
		_ = termination.Write(err.Error())
		if exitCode == 0 {
			// Safety net: an error surfaced from outside runStage (e.g. a
			// cobra flag-parsing error, so RunE never ran) leaves exitCode
			// at its zero value — that must still mean failure.
			exitCode = 1
		}
		os.Exit(exitCode)
	}
	os.Exit(exitCode)
}

func runStage(cmd *cobra.Command, args []string) (int, error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Written on the way out regardless of how runStage returns — early
	// setup failures keep this safe default; a real prompt outcome
	// overwrites it below (ADR 0011: the blob is written "on exit").
	term := terminationBlob{ExitCode: 1, Outcome: outcomeFailed.String()}
	defer func() { writeTerminationLog(terminationLogPath(), term) }()

	// 1. Load config from env
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return 1, fmt.Errorf("config: %w", err)
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
		return 1, fmt.Errorf("hub resolution: %w", err)
	}
	// Model text is logged at stage end; whatever it says, the
	// credentials goose inherits from this environment must not be in it.
	red := newRedactor(cfg.APIKey, cfg.HubToken, cfg.ACPSecretKey, creds.Token)

	if cfg.TargetBranch == creds.Branch {
		return 1, fmt.Errorf("TARGET_BRANCH %q must differ from source branch", cfg.TargetBranch)
	}
	creds.Branch = cfg.TargetBranch

	// 3. Clone, strip creds, checkout branch
	logging.Header("Git Setup")
	logging.Info("cloning %s...", creds.RepoURL)

	repo, err := git.Clone(ctx, creds, cloneDir)
	if err != nil {
		return 1, fmt.Errorf("clone: %w", err)
	}

	if err := git.ConfigureAuthor(repo, cfg.GitAuthorName, cfg.GitAuthorEmail); err != nil {
		return 1, fmt.Errorf("configure git author: %w", err)
	}

	if err := git.StripCredentials(repo); err != nil {
		return 1, fmt.Errorf("strip credentials: %w", err)
	}
	hub.ClearEnv()

	if err := git.CheckoutBranch(repo, creds.Branch); err != nil {
		return 1, fmt.Errorf("checkout branch %s: %w", creds.Branch, err)
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
		return 1, fmt.Errorf("discover skills: %w", err)
	}
	hasSkills := len(skillPaths) > 0

	// Always-loaded rules. The runtime discovers on-demand skills itself and
	// the agent reads them when it judges them relevant, but nothing
	// guarantees it ever reads a rule, so the harness puts those in the prompt
	// (ADR 0014). The loader decided which are rules and recorded them, since
	// a skill's directory is its frontmatter name and nothing outside the
	// image knows that (ADR 0015).
	manifest, err := skill.ReadManifest(skillsDir())
	if err != nil {
		return 1, fmt.Errorf("read skill manifest: %w", err)
	}
	rules, err := skill.RuleContent(skillsDir(), manifest)
	if err != nil {
		return 1, fmt.Errorf("read rules: %w", err)
	}
	if len(rules) > 0 {
		names := make([]string, 0, len(rules))
		for _, r := range rules {
			names = append(names, r.Name)
		}
		logging.Info("always-loaded rules: %s", strings.Join(names, ", "))
	}

	if hasSkills {
		home, err := os.UserHomeDir()
		if err != nil {
			return 1, fmt.Errorf("resolve home dir: %w", err)
		}
		if err := symlinkSkillsDir(home, skillsDir()); err != nil {
			return 1, fmt.Errorf("skill symlink: %w", err)
		}
		logging.Ok("symlinked %s/.agents/skills → %s", home, skillsDir())

		// 4b. Write analysis to workspace (if resolved from Hub). Uncommitted:
		// the entry point never commits files itself; all commits are authored
		// by the agent.
		if err := fetchAndWriteAnalysis(hubClient, cfg.AppID, cloneDir); err != nil {
			logging.Warn("analysis fetch: %v", err)
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
		Mode:         cfg.Params.Execution.Mode,
		MaxTurns:     cfg.MaxTurns,
	})
	if err != nil {
		return 1, fmt.Errorf("start goose serve: %w", err)
	}
	defer srv.Stop()

	// 6. Connect ACP, create session
	wsClient, err := acp.WaitReadyDial(ctx, "127.0.0.1", srv.Port(), srv.SecretKey(), 30*time.Second)
	if err != nil {
		return 1, fmt.Errorf("connect to goose: %w", err)
	}
	defer wsClient.Close()

	session := acp.NewSessionClient(wsClient)
	// ask_user: the harness binary doubles as a stdio MCP server giving the
	// agent one tool whose questions reach attached viewers (through the
	// tee) and block the turn until answered — the in-turn "stop and
	// confirm" the transcript otherwise cannot express.
	var mcpServers []acp.MCPServer
	if cfg.HITLAsk {
		if self, err := os.Executable(); err == nil {
			mcpServers = append(mcpServers, acp.MCPServer{
				Name:    askuser.ToolName,
				Command: self,
				Args:    []string{askuser.Subcommand},
				Env:     []acp.EnvVar{},
			})
		} else {
			logging.Warn("ask_user tool disabled: cannot resolve harness executable: %v", err)
		}
	}
	sessionID, err := session.CreateSession(ctx, cloneDir, mcpServers)
	if err != nil {
		return 1, fmt.Errorf("create session: %w", err)
	}
	if len(mcpServers) > 0 {
		logging.Ok("ask_user tool mounted (questions reach attached viewers; HARNESS_HITL_ASK=off to disable)")
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
		Rules:         rules,
		WorkflowGuide: cfg.WorkflowGuide,
		Parameters:    params.RenderSection(cfg.Params),
		StageTask:     cfg.StageInstructions,
		AskUser:       len(mcpServers) > 0,
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
		return 1, fmt.Errorf("create watcher: %w", err)
	}
	if err := w.Start(ctx); err != nil {
		return 1, fmt.Errorf("start watcher: %w", err)
	}
	defer w.Stop()

	// 9. Send the primary ACP prompt (blocks until goose finishes
	// naturally, hits its native GOOSE_MAX_TURNS, or the harness cancels
	// it for cost — ADR 0011).
	logging.Header("Running Stage")
	logging.Info("max turns: %d", cfg.MaxTurns)
	emitPlan("completed", "in_progress", "pending")
	if teeSrv != nil {
		teeSrv.SetRunActive(true)
	}
	primaryResult, err := session.SendPrompt(ctx, sessionID, []acp.ContentBlock{
		{Type: "text", Text: stagePrompt},
	}, cfg.CostLimit)
	if teeSrv != nil {
		teeSrv.SetRunActive(false)
	}

	stageOutcome, limit := classifyOutcome(primaryResult, err, params.NativeTurnLimit(cfg.MaxTurns))

	// An unanswered ask_user question is a HITL gate the harness stopped the
	// turn on: it also surfaces as stopReason=cancelled (the harness fired
	// session/cancel), so check it before the viewer-cancel case and give it
	// its own message rather than blaming a viewer.
	hitlUnanswered := session.HITLGateUnanswered()
	viewerCancelled := err == nil && primaryResult != nil &&
		primaryResult.StopReason == "cancelled" && !primaryResult.CostLimitReached && !hitlUnanswered
	switch {
	case hitlUnanswered:
		logging.Err("run stopped: an ask_user question went unanswered (HITL gate, fail-closed)")
	case viewerCancelled:
		logging.Warn("run cancelled by an attached viewer")
	}
	if err != nil {
		logging.Err("prompt failed: %s", red.redact(err.Error()))
	}
	if primaryResult != nil && logAgentClosingMessage(primaryResult, red) {
		emitNotice("agent ended on goose's provider-error text — the model call may have failed; see the pod log")
	}

	// 9b. One-shot handoff prompt when a limit was reached (ADR 0011).
	// A handoff-prompt error is logged only — the limit-reached outcome
	// stands regardless; the watcher has been auto-pushing throughout
	// the run, so real work isn't lost even if this best-effort round
	// trip fails.
	var handoffResult *acp.PromptResult
	if stageOutcome == outcomeLimitReached {
		logging.Warn("execution limit reached (%s) — sending handoff prompt", limit)
		// ACP cost is session-cumulative (combineUsage relies on the same
		// fact), so the handoff call must compare against the full
		// configured MaxCost, not the primary's reserved CostLimit —
		// cumulative spend already sits at ~85% of MaxCost when the
		// primary stops, so reusing CostLimit here would leave the
		// handoff no room to write .konveyor/handoff.md and commit
		// before being cancelled itself.
		var handoffErr error
		handoffResult, handoffErr = session.SendPrompt(ctx, sessionID, []acp.ContentBlock{
			{Type: "text", Text: handoffPromptText},
		}, cfg.MaxCost)
		if handoffErr != nil {
			logging.Warn("handoff prompt: %v — continuing, the limit-reached outcome stands", handoffErr)
		}
		if handoffResult != nil {
			logAgentClosingMessage(handoffResult, red)
		}
	}
	emitPlan("completed", "completed", "in_progress")

	// 10. Check goose health — a crash overrides any outcome above.
	if !srv.Alive() {
		logging.Err("goose serve crashed")
		stageOutcome, limit = outcomeFailed, limitNone
	}

	// 11. Check for uncommitted work
	if wt, err := repo.Worktree(); err == nil {
		if st, err := wt.Status(); err == nil && !st.IsClean() {
			logging.Warn("worktree dirty at stage end — agent left %d uncommitted paths", len(st))
		}
	}

	// 12. Stop watcher before final push
	w.Stop()

	// 13. Usage stats and termination log, regardless of outcome. Set
	// before the final push below so a push failure doesn't discard
	// them — the deferred writeTerminationLog fires no matter how this
	// function returns (ADR 0011: the blob is written "on exit").
	if primaryResult != nil {
		u := combineUsage(primaryResult, handoffResult)
		term = terminationBlob{
			ExitCode:     stageOutcome.exitCode(),
			Outcome:      stageOutcome.String(),
			LimitReached: string(limit),
			StopReason:   primaryResult.StopReason,
			Usage:        &u,
		}
	} else {
		term = terminationBlob{ExitCode: stageOutcome.exitCode(), Outcome: stageOutcome.String()}
	}

	// 14. Final push (use a fresh context — the signal context may
	// already be cancelled after SIGINT)
	logging.Header("Final Push")
	pushCtx, pushCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer pushCancel()
	pushed, pushErr := emitPush("git push (final)", func() (bool, error) {
		return git.Push(pushCtx, creds, repo, creds.Branch, baseSHA)
	})
	if pushErr != nil {
		emitNotice("stage failed — final push error: %v", pushErr)
		term.ExitCode = 1
		term.Outcome = outcomeFailed.String()
		term.LimitReached = ""
		return 1, fmt.Errorf("final push: %w", pushErr)
	}
	emitPlan("completed", "completed", "completed")

	// 15. Exit. pushed is false when the run produced no commits, so the
	// notices must not claim work landed on the branch.
	switch stageOutcome {
	case outcomeSucceeded:
		stageSucceeded = true
		if pushed {
			emitNotice("stage succeeded — results pushed to branch %s", creds.Branch)
		} else {
			emitNotice("stage succeeded — no changes to push")
		}
		logging.Ok("stage succeeded")
		return 0, nil

	case outcomeLimitReached:
		if pushed {
			emitNotice("execution limit reached (%s) — handoff committed, results pushed to branch %s", limit, creds.Branch)
			logging.Ok("stage stopped at execution limit (%s) — handoff committed", limit)
		} else {
			// The handoff prompt itself only gets the runtime's native
			// per-prompt turn budget too — for a very small configured
			// maxTurns, that may not be enough for the agent to actually
			// write .konveyor/handoff.md and commit before its own turns
			// run out. Report what actually happened, not what was hoped for.
			emitNotice("execution limit reached (%s) — no commits to push", limit)
			logging.Warn("stage stopped at execution limit (%s) — handoff prompt ran but produced no commit", limit)
		}
		return 2, nil

	default: // outcomeFailed
		switch {
		case hitlUnanswered && pushed:
			emitNotice("run stopped — an ask_user question went unanswered (no human to decide); partial work pushed to branch %s", creds.Branch)
		case hitlUnanswered:
			emitNotice("run stopped — an ask_user question went unanswered (no human to decide); no commits to push")
		case viewerCancelled && pushed:
			emitNotice("run cancelled by viewer — partial work pushed to branch %s", creds.Branch)
		case viewerCancelled:
			emitNotice("run cancelled by viewer — no commits to push")
		case pushed:
			emitNotice("stage failed — partial work pushed to branch %s", creds.Branch)
		default:
			emitNotice("stage failed — no commits to push")
		}
		if hitlUnanswered {
			logging.Err("stage failed: ask_user question unanswered (HITL gate)")
			return 1, fmt.Errorf("stage failed: ask_user question unanswered (HITL gate)")
		}
		logging.Err("stage failed")
		return 1, fmt.Errorf("stage failed")
	}
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
	if app.Repository == nil {
		return nil, fmt.Errorf("application %q has no source repository configured", app.Name)
	}

	// Fail fast on non-git sources instead of attempting the clone and
	// failing later with a confusing go-git error (issue #143). Runs before
	// the log line below so a bad source is rejected cleanly rather than
	// surfacing later as a deep go-git clone error.
	if err := hub.ValidateSourceRepository(app.Repository); err != nil {
		return nil, err
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

// closingMessageLimit bounds what a closing message adds to the pod log.
// Enough for a summary or a provider error; a wall of text is cut, with
// its full size noted.
const closingMessageLimit = 4000

// logAgentClosingMessage writes the agent's closing message (and any goose
// notices) to the pod log, secrets redacted, and reports whether the message
// is one of goose's provider-failure texts. The log line is the only durable record of what
// the model said: the live transcript lives in the tee and dies with the
// pod, and nothing else persists assistant text. goose reports provider
// failures as assistant prose rather than RPC errors — without this a run
// that never reached the model ends "stage succeeded — no changes to push"
// with no trace of why.
func logAgentClosingMessage(r *acp.PromptResult, red *redactor) (providerError bool) {
	for _, n := range r.Notices {
		logging.Warn("goose notice: %s", red.redact(n))
	}
	full := red.redact(strings.TrimSpace(r.FinalMessage()))
	if full == "" {
		logging.Info("agent closing message: none (%d text chunks during the turn)", len(r.Chunks))
		return false
	}
	msg := full
	if runes := []rune(full); len(runes) > closingMessageLimit {
		msg = string(runes[:closingMessageLimit]) + fmt.Sprintf("… [truncated, %d bytes total]", len(full))
	}
	if r.ClosingProviderError() {
		logging.Warn("agent closing message matches goose's provider-error text "+
			"(the model call may have failed or been refused; goose ended the turn normally):\n%s", msg)
		return true
	}
	logging.Info("agent closing message:\n%s", msg)
	return false
}
