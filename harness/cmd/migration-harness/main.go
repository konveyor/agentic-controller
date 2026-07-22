package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	gogit "github.com/go-git/go-git/v5"

	"github.com/konveyor/migration-harness/internal/acp"
	"github.com/konveyor/migration-harness/internal/config"
	"github.com/konveyor/migration-harness/internal/git"
	"github.com/konveyor/migration-harness/internal/goose"
	"github.com/konveyor/migration-harness/internal/logging"
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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// 1. Load config from env
	cfg := config.LoadFromEnv()
	if cfg == nil {
		return fmt.Errorf("KONVEYOR_MODEL_PRIMARY_MODEL and KONVEYOR_MODEL_PRIMARY_PROVIDER are required")
	}

	// 2. Read git creds
	creds, err := git.ReadFromEnv()
	if err != nil {
		return fmt.Errorf("git credentials: %w", err)
	}
	if creds == nil {
		return fmt.Errorf("GIT_REPO_URL is required")
	}

	// 3. Clone, strip creds, checkout branch
	logging.Header("Git Setup")
	logging.Info("cloning %s...", creds.RepoURL)

	cloneDir := os.Getenv("HARNESS_WORK_DIR")
	if cloneDir == "" {
		cloneDir = "/workspace/repo"
	}

	repo, err := git.Clone(ctx, creds, cloneDir)
	if err != nil {
		return fmt.Errorf("clone: %w", err)
	}

	if err := git.StripCredentials(repo); err != nil {
		return fmt.Errorf("strip credentials: %w", err)
	}
	git.ClearEnvCredentials()

	if err := git.CheckoutBranch(repo, creds.Branch); err != nil {
		return fmt.Errorf("checkout branch %s: %w", creds.Branch, err)
	}
	logging.Ok("cloned to %s, branch %s", cloneDir, creds.Branch)

	// 4. Start goose serve
	logging.Header("Goose Setup")
	srv, err := goose.StartServe(ctx, 0, cfg.Provider, cfg.Model, cfg.APIKey, cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("start goose serve: %w", err)
	}
	defer srv.Stop()

	// 5. Connect ACP, create session
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

	// 6. Discover skills
	skillContent, skillPaths, err := discoverSkills()
	if err != nil {
		return fmt.Errorf("discover skills: %w", err)
	}

	// 7. Build prompt from context layers
	prompt := buildPrompt(skillContent)

	// 8. Start filesystem watcher BEFORE blocking prompt
	commitPush := func() error {
		if _, err := git.CommitAll(repo, "konveyor: auto-commit progress"); err != nil {
			return err
		}
		return git.Push(ctx, creds, repo, creds.Branch)
	}
	w, err := watcher.New(cloneDir, commitPush)
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
		{Type: "text", Text: prompt},
	}, cfg.MaxTurns)

	if err != nil {
		logging.Err("prompt failed: %v", err)
	}

	if !srv.Alive() {
		logging.Err("goose serve crashed")
	}

	// 10. Stop watcher
	w.Stop()

	// 11. Determine exit status from ACP/goose signals
	stageFailed := err != nil || !srv.Alive()

	status := "succeeded"
	if stageFailed {
		status = "failed"
	}

	// 11b. Write handoff.md for next stage
	if err := writeHandoff(cloneDir, skillPaths, status, repo); err != nil {
		logging.Warn("handoff: %v", err)
	}

	// 12. Final commit + push
	logging.Header("Final Push")
	if _, err := git.CommitAll(repo, "konveyor: stage complete"); err != nil {
		logging.Warn("final commit: %v", err)
	}
	if err := git.Push(ctx, creds, repo, creds.Branch); err != nil {
		logging.Warn("final push: %v", err)
	}

	// 13. Exit
	if stageFailed {
		logging.Err("stage failed")
		os.Exit(1)
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
		return "", nil, fmt.Errorf("no skills found at %s", pattern)
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

func buildPrompt(skillContent string) string {
	var b strings.Builder

	if v := os.Getenv("KONVEYOR_PROMPT"); v != "" {
		b.WriteString(v)
		b.WriteString("\n\n")
	}

	if v := os.Getenv("KONVEYOR_PLAYBOOK_INSTRUCTIONS"); v != "" {
		b.WriteString("## Migration Context\n\n")
		b.WriteString(v)
		b.WriteString("\n\n")
	}

	b.WriteString("## Skill Instructions\n\n")
	b.WriteString(skillContent)
	b.WriteString("\n\n")

	if v := os.Getenv("KONVEYOR_INSTRUCTIONS"); v != "" {
		b.WriteString("## Stage Task\n\n")
		b.WriteString(v)
	}

	return b.String()
}

func skillName(path string) string {
	return filepath.Base(filepath.Dir(path))
}

func writeHandoff(workDir string, skills []string, status string, repo *gogit.Repository) error {
	handoffPath := filepath.Join(workDir, ".konveyor", "handoff.md")
	if err := os.MkdirAll(filepath.Dir(handoffPath), 0o755); err != nil {
		return fmt.Errorf("create .konveyor dir: %w", err)
	}

	existing, _ := os.ReadFile(handoffPath)

	var b strings.Builder

	if len(existing) > 0 {
		b.Write(existing)
		b.WriteString("\n---\n\n")
	}

	fmt.Fprintf(&b, "**Status:** %s  \n", status)
	fmt.Fprintf(&b, "**Completed:** %s\n", time.Now().UTC().Format(time.RFC3339))

	b.WriteString("\n### Skills\n\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "- %s\n", skillName(s))
	}

	if n := changedFileCount(repo); n > 0 {
		fmt.Fprintf(&b, "\n**Files changed:** %d\n", n)
	}

	if err := os.WriteFile(handoffPath, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write handoff.md: %w", err)
	}

	logging.Ok("wrote %s", handoffPath)
	return nil
}

var excludeDirs = []string{"graphify-out/", ".konveyor/", "target/", ".git/"}

func changedFileCount(repo *gogit.Repository) int {
	wt, err := repo.Worktree()
	if err != nil {
		return 0
	}
	status, err := wt.Status()
	if err != nil {
		return 0
	}
	n := 0
	for path := range status {
		excluded := false
		for _, prefix := range excludeDirs {
			if strings.HasPrefix(path, prefix) {
				excluded = true
				break
			}
		}
		if !excluded {
			n++
		}
	}
	return n
}

