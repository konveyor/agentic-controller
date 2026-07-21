# Harness + AgentPlaybookRun Integration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure the migration harness from a monolithic 5-stage orchestrator into a thin, uniform git plumbing wrapper so that AgentPlaybookRun sequences stages and goose skills carry migration intelligence.

**Architecture:** The harness binary becomes identical in every stage image: git clone → goose serve → single ACP prompt → filesystem watcher → final commit+push → exit. The AgentPlaybookRun controller creates one AgentRun per stage sequentially. Each stage image bakes in exactly one skill at `/opt/skills/<stage>/SKILL.md`. A new `watcher` package uses fsnotify to commit+push progress in the background while goose works.

**Tech Stack:** Go 1.26, fsnotify, go-git/v5, cobra, gorilla/websocket, Containerfile (UBI 10), YAML CRDs

**Spec:** `docs/superpowers/specs/2026-07-15-harness-agentplaybookrun-design.md`

## Global Constraints

- Go 1.26.2 (harness), Go 1.24.2 (controller — per go.mod)
- fsnotify must be added to `harness/go.mod`
- Git credentials must never be visible to goose — harness only
- Skills live at `/opt/skills/<stage>/SKILL.md` — exactly one per image
- `git add -A` is forbidden in the watcher; use `git add -u` + known patterns
- Java-only scope — no other language images in this plan
- Existing controller and ACP tests must continue to pass

---

## File Structure

### New files

| File | Responsibility |
|---|---|
| `harness/internal/watcher/watcher.go` | Filesystem watcher: fsnotify → quiet-period detection → git add/commit/push |
| `harness/internal/watcher/watcher_test.go` | Unit tests for watcher |
| `harness/internal/watcher/patterns.go` | Known source file patterns for safe staging |
| `harness/internal/watcher/patterns_test.go` | Tests for pattern matching |
| `images/agent-base/Containerfile` | Base image: UBI + goose + git + harness binary |
| `images/agent-plan/Containerfile` | Plan image: agent-base + graphify |
| `images/agent-execute-java/Containerfile` | Execute image: agent-base + JDK 21 + Maven |
| `images/agent-verify-java/Containerfile` | Verify image: agent-base + JDK 21 + Maven |
| `skills/plan/SKILL.md` | Plan skill (detect+plan) |
| `skills/execute-java/SKILL.md` | Java execute skill |
| `skills/execute-java/references/javaee-quarkus.md` | Java EE → Quarkus reference (copied from existing) |
| `skills/verify-java/SKILL.md` | Java verify+fix skill |
| `hack/harness-test/playbook-resources.yaml` | Example AgentPlaybook + AgentPlaybookRun CRs |

### Modified files

| File | What changes |
|---|---|
| `api/v1alpha1/agentplaybookrun_types.go` | Add `TargetBranch` field to spec |
| `internal/controller/agentplaybookrun_controller.go` | Inject `GIT_TARGET_BRANCH` env var |
| `harness/cmd/migration-harness/main.go` | Gut to single `run` command with thin wrapper flow |
| `harness/internal/config/config.go` | Remove file-based config, keep `LoadFromEnv` |
| `harness/go.mod` | Add fsnotify dependency |

### Deleted files/directories

| Path | Reason |
|---|---|
| `harness/internal/detect/` | Logic moves to plan skill |
| `harness/internal/plan/` | Logic moves to plan skill |
| `harness/internal/execute/` | Logic moves to execute skill |
| `harness/internal/verify/` | Logic moves to verify skill |
| `harness/internal/fixloop/` | Logic moves to verify skill |
| `harness/internal/handoff/` | Controller tracks stage status |
| `harness/internal/metrics/` | Controller records timing |
| `harness/internal/rundir/` | No multi-run tracking needed |
| `harness/internal/goose/goose.go` | Runner interface no longer needed |
| `harness/internal/goose/acp_runner.go` | Multi-turn recipe runner no longer needed |
| `harness/internal/goose/acp_runner_test.go` | Tests for deleted code |
| `harness/internal/goose/recipe.go` | Recipe system no longer needed |
| `harness/internal/goose/recipe_test.go` | Tests for deleted code |
| `harness/recipes/` | Recipe content folded into skills |
| `harness/skill-bundle/` | Skills now baked into per-stage images |
| `images/agent-base-goose-java/Containerfile` | Replaced by 4 new Containerfiles |

---

### Task 1: Make GIT_TARGET_BRANCH required (remove fallback)

**Files:**
- Modify: `harness/internal/git/credentials.go`

**Interfaces:**
- Consumes: `GIT_TARGET_BRANCH` env var (set by user in `AgentPlaybookRun.spec.env`)
- Produces: error if `GIT_TARGET_BRANCH` is not set (previously fell back to timestamp)

**Decision:** `GIT_TARGET_BRANCH` is a plain env var passed through `spec.env` on the AgentPlaybookRun. The controller already forwards `pbRun.Spec.Env` to every child AgentRun. No CRD field needed. If the user forgets to set it, the harness fails immediately.

- [x] **Step 1: Remove timestamp fallback in credentials.go**

In `harness/internal/git/credentials.go`, replace the fallback branch generation with an error:

```go
branch := os.Getenv("GIT_TARGET_BRANCH")
if branch == "" {
    return nil, fmt.Errorf("GIT_REPO_URL is set but GIT_TARGET_BRANCH is missing")
}
```

Remove the unused `time` import.

- [x] **Step 2: Verify build**

Run: `cd harness && go build ./... && go vet ./...`

---

### Task 2: Create filesystem watcher package

**Files:**
- Create: `harness/internal/watcher/patterns.go`
- Create: `harness/internal/watcher/patterns_test.go`
- Create: `harness/internal/watcher/watcher.go`
- Create: `harness/internal/watcher/watcher_test.go`
- Modify: `harness/go.mod` (add fsnotify)

**Interfaces:**
- Consumes: `git.CommitAll(repo, msg)`, `git.Push(ctx, creds, repo, branch)` from `harness/internal/git`
- Produces: `watcher.New(dir string, commitFn func() error) *Watcher`, `(*Watcher).Start()`, `(*Watcher).Stop()`

- [ ] **Step 1: Add fsnotify dependency**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller/harness && go get github.com/fsnotify/fsnotify`

Expected: fsnotify added to go.mod and go.sum

- [ ] **Step 2: Write pattern matching (patterns.go)**

```go
package watcher

import (
	"path/filepath"
	"strings"
)

var sourceExts = map[string]bool{
	".java": true, ".xml": true, ".properties": true,
	".md": true, ".json": true, ".yaml": true, ".yml": true,
	".gradle": true, ".kt": true, ".groovy": true,
}

var excludeDirs = map[string]bool{
	".goose": true, "__pycache__": true, ".git": true,
	"node_modules": true, "target": true,
}

var excludeExts = map[string]bool{
	".tmp": true, ".swp": true, ".bak": true,
}

func ShouldStageNewFile(path string) bool {
	base := filepath.Base(path)

	if base == "pom.xml" || base == "result.json" {
		return true
	}

	for _, part := range strings.Split(filepath.Dir(path), string(filepath.Separator)) {
		if excludeDirs[part] {
			return false
		}
	}

	ext := filepath.Ext(base)
	if excludeExts[ext] {
		return false
	}

	return sourceExts[ext]
}
```

- [ ] **Step 3: Write pattern tests (patterns_test.go)**

```go
package watcher

import "testing"

func TestShouldStageNewFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"src/main/java/com/example/App.java", true},
		{"pom.xml", true},
		{"src/main/resources/application.properties", true},
		{".konveyor/result.json", true},
		{"PLAN.md", true},
		{"graph.json", true},
		{".goose/cache/foo.txt", false},
		{"__pycache__/mod.pyc", false},
		{"target/classes/App.class", false},
		{"scratch.tmp", false},
		{"file.swp", false},
		{"random.txt", false},
		{"src/main/java/.goose/internal.java", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := ShouldStageNewFile(tt.path); got != tt.want {
				t.Errorf("ShouldStageNewFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 4: Run pattern tests**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller/harness && go test ./internal/watcher/ -run TestShouldStage -v`

Expected: PASS

- [ ] **Step 5: Write the watcher (watcher.go)**

```go
package watcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/konveyor/migration-harness/internal/logging"
)

const quietPeriod = 30 * time.Second

type CommitPushFn func() error

type Watcher struct {
	dir      string
	commitFn CommitPushFn
	fsw      *fsnotify.Watcher
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func New(dir string, commitFn CommitPushFn) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		dir:      dir,
		commitFn: commitFn,
		fsw:      fsw,
	}, nil
}

func (w *Watcher) Start(ctx context.Context) error {
	if err := w.addDirRecursive(w.dir); err != nil {
		return err
	}

	ctx, w.cancel = context.WithCancel(ctx)
	w.wg.Add(1)
	go w.loop(ctx)
	logging.Info("filesystem watcher started (quiet period: %s)", quietPeriod)
	return nil
}

func (w *Watcher) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	w.fsw.Close()
}

func (w *Watcher) loop(ctx context.Context) {
	defer w.wg.Done()
	timer := time.NewTimer(quietPeriod)
	timer.Stop()
	dirty := false

	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			rel, err := filepath.Rel(w.dir, event.Name)
			if err != nil {
				continue
			}
			if !isRelevantChange(rel) {
				continue
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					w.fsw.Add(event.Name)
				}
			}
			dirty = true
			timer.Reset(quietPeriod)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			logging.Warn("watcher error: %v", err)
		case <-timer.C:
			if dirty {
				w.doCommit()
				dirty = false
			}
		}
	}
}

func isRelevantChange(relPath string) bool {
	for _, part := range strings.Split(filepath.Dir(relPath), string(filepath.Separator)) {
		if excludeDirs[part] {
			return false
		}
	}
	base := filepath.Base(relPath)
	ext := filepath.Ext(base)
	return !excludeExts[ext]
}

func (w *Watcher) doCommit() {
	if err := w.stageFiles(); err != nil {
		logging.Warn("watcher stage: %v", err)
		return
	}
	if err := w.commitFn(); err != nil {
		logging.Warn("watcher commit+push: %v", err)
	}
}

func (w *Watcher) stageFiles() error {
	cmd := exec.Command("git", "-C", w.dir, "add", "-u")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add -u: %s: %w", out, err)
	}

	entries, err := w.findNewFiles()
	if err != nil {
		return err
	}
	for _, f := range entries {
		cmd := exec.Command("git", "-C", w.dir, "add", "--", f)
		if out, err := cmd.CombinedOutput(); err != nil {
			logging.Warn("git add %s: %s", f, out)
		}
	}
	return nil
}

func (w *Watcher) findNewFiles() ([]string, error) {
	cmd := exec.Command("git", "-C", w.dir, "ls-files", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var staged []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && ShouldStageNewFile(line) {
			staged = append(staged, line)
		}
	}
	return staged, nil
}

func (w *Watcher) addDirRecursive(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if excludeDirs[name] || name == ".konveyor" {
				return filepath.SkipDir
			}
			return w.fsw.Add(path)
		}
		return nil
	})
}
```

- [ ] **Step 6: Write watcher unit tests (watcher_test.go)**

```go
package watcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatcherDetectsFileChange(t *testing.T) {
	dir := t.TempDir()

	// Initialize a git repo so git add -u works
	runGit(t, dir, "init")
	writeFile(t, filepath.Join(dir, "App.java"), "class App {}")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")

	var commitCount atomic.Int32
	commitFn := func() error {
		commitCount.Add(1)
		return nil
	}

	w, err := New(dir, commitFn)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	// Modify a tracked file
	writeFile(t, filepath.Join(dir, "App.java"), "class App { int x; }")

	// Wait for quiet period + buffer
	time.Sleep(quietPeriod + 5*time.Second)

	if commitCount.Load() == 0 {
		t.Error("expected at least one commit after quiet period")
	}
}

func TestWatcherIgnoresExcludedDirs(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	writeFile(t, filepath.Join(dir, "App.java"), "class App {}")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")

	var commitCount atomic.Int32
	commitFn := func() error {
		commitCount.Add(1)
		return nil
	}

	w, err := New(dir, commitFn)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	// Write to an excluded dir — should NOT trigger commit
	gooseDir := filepath.Join(dir, ".goose")
	os.MkdirAll(gooseDir, 0755)
	writeFile(t, filepath.Join(gooseDir, "cache.db"), "data")

	time.Sleep(quietPeriod + 5*time.Second)

	if commitCount.Load() != 0 {
		t.Error("expected no commits for changes in excluded dirs")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}
```

- [ ] **Step 7: Run watcher tests**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller/harness && go test ./internal/watcher/ -v -timeout 120s`

Expected: PASS (tests take ~35s each due to quiet period)

- [ ] **Step 8: Commit**

```bash
git add harness/go.mod harness/go.sum harness/internal/watcher/
git commit -m "feat: add filesystem watcher with quiet-period commit+push"
```

---

### Task 3: Gut main.go to thin wrapper

**Files:**
- Modify: `harness/cmd/migration-harness/main.go` (full rewrite)
- Modify: `harness/internal/config/config.go` (remove file-based config, Save, DefaultConfigPath)

**Interfaces:**
- Consumes: `config.LoadFromEnv()`, `git.ReadFromEnv()`, `git.Clone()`, `git.StripCredentials()`, `git.ClearEnvCredentials()`, `git.CheckoutBranch()`, `git.CommitAll()`, `git.Push()`, `goose.StartServe()`, `acp.WaitReadyDial()`, `acp.NewSessionClient()`, `(*SessionClient).CreateSession()`, `(*SessionClient).SendPrompt()`, `watcher.New()`, `(*Watcher).Start()`, `(*Watcher).Stop()`
- Produces: `main()` — single `run` command entrypoint for the harness binary

- [ ] **Step 1: Simplify config.go — remove file-based config**

Remove `DefaultHome()`, `DefaultConfigPath()`, `Load()`, `Save()`, `parseConfigLine()` from `harness/internal/config/config.go`. Keep only `LoadFromEnv()`, `Config` struct, and the `Default*` constants.

```go
package config

import (
	"os"
	"strconv"
)

const (
	DefaultMaxTurns         = 200
	DefaultMaxFixIterations = 3
)

type Config struct {
	Model            string
	Provider         string
	Endpoint         string
	APIKey           string
	MaxTurns         int
	MaxFixIterations int
}

func LoadFromEnv() *Config {
	model := os.Getenv("KONVEYOR_MODEL_PRIMARY_MODEL")
	provider := os.Getenv("KONVEYOR_MODEL_PRIMARY_PROVIDER")
	if model == "" || provider == "" {
		return nil
	}

	cfg := &Config{
		Model:            model,
		Provider:         provider,
		Endpoint:         os.Getenv("KONVEYOR_MODEL_PRIMARY_ENDPOINT"),
		APIKey:           os.Getenv("KONVEYOR_MODEL_PRIMARY_API_KEY"),
		MaxTurns:         DefaultMaxTurns,
		MaxFixIterations: DefaultMaxFixIterations,
	}

	if n, err := strconv.Atoi(os.Getenv("KONVEYOR_PARAM_MAX_TURNS")); err == nil && n > 0 {
		cfg.MaxTurns = n
	}
	if n, err := strconv.Atoi(os.Getenv("KONVEYOR_PARAM_MAX_FIX_ITERATIONS")); err == nil && n > 0 {
		cfg.MaxFixIterations = n
	}

	return cfg
}
```

- [ ] **Step 2: Rewrite main.go**

Replace the entire `harness/cmd/migration-harness/main.go` with the thin wrapper flow. The new `main.go` has a single `run` command:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

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

	workDir := os.Getenv("HARNESS_WORK_DIR")
	if workDir == "" {
		workDir = "/workspace"
	}
	cloneDir := filepath.Join(workDir, "repo")

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

	// 6. Discover skill
	skillContent, err := discoverSkill()
	if err != nil {
		return fmt.Errorf("discover skill: %w", err)
	}

	// 7. Build prompt from 4 context layers
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

	// 9. Send single ACP prompt (blocks until goose finishes)
	// NOTE: KONVEYOR_PARAM_MAX_TURNS is parsed by config but not enforced
	// here yet. The spec flags this as needing investigation: goose serve
	// may accept a turn limit via CLI flag, env var, or ACP session param.
	// If none exist natively, count tool_call notifications from the ACP
	// stream and terminate. This is deferred to a follow-up task.
	logging.Header("Running Stage")
	_, err = session.SendPrompt(ctx, sessionID, []acp.ContentBlock{
		{Type: "text", Text: prompt},
	})

	if err != nil {
		logging.Err("prompt failed: %v", err)
	}

	if !srv.Alive() {
		logging.Err("goose serve crashed")
	}

	// 10. Stop watcher
	w.Stop()

	// 11. Read result.json for exit status
	exitCode := readResultStatus(cloneDir)

	// 12. Final commit + push
	logging.Header("Final Push")
	if _, err := git.CommitAll(repo, "konveyor: stage complete"); err != nil {
		logging.Warn("final commit: %v", err)
	}
	if err := git.Push(ctx, creds, repo, creds.Branch); err != nil {
		logging.Warn("final push: %v", err)
	}

	// 13. Exit
	if exitCode != 0 {
		logging.Err("stage failed (result.json)")
		os.Exit(1)
	}
	logging.Ok("stage succeeded")
	return nil
}

func discoverSkill() (string, error) {
	matches, err := filepath.Glob("/opt/skills/*/SKILL.md")
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no skill found at /opt/skills/*/SKILL.md")
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("expected exactly one skill, found %d: %v", len(matches), matches)
	}
	content, err := os.ReadFile(matches[0])
	if err != nil {
		return "", fmt.Errorf("read skill %s: %w", matches[0], err)
	}
	logging.Info("discovered skill: %s", matches[0])
	return string(content), nil
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

type stageResult struct {
	Stage  string `json:"stage"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func readResultStatus(workDir string) int {
	path := filepath.Join(workDir, ".konveyor", "result.json")
	data, err := os.ReadFile(path)
	if err != nil {
		logging.Warn("no result.json found — treating as failure")
		return 1
	}

	var results []stageResult
	if err := json.Unmarshal(data, &results); err != nil {
		logging.Warn("invalid result.json: %v", err)
		return 1
	}

	if len(results) == 0 {
		logging.Warn("result.json is empty — treating as failure")
		return 1
	}

	last := results[len(results)-1]
	if last.Status == "succeeded" {
		return 0
	}

	logging.Err("stage %s failed: %s", last.Stage, last.Reason)
	return 1
}
```

- [ ] **Step 3: Verify the harness builds**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller/harness && go build ./cmd/migration-harness/`

Expected: builds without errors

- [ ] **Step 4: Commit**

```bash
git add harness/cmd/migration-harness/main.go harness/internal/config/config.go
git commit -m "feat: rewrite harness as thin wrapper with single run command"
```

---

### Task 4: Delete obsolete harness packages and files

**Files:**
- Delete: `harness/internal/detect/` (entire directory)
- Delete: `harness/internal/plan/` (entire directory)
- Delete: `harness/internal/execute/` (entire directory)
- Delete: `harness/internal/verify/` (entire directory)
- Delete: `harness/internal/fixloop/` (entire directory)
- Delete: `harness/internal/handoff/` (entire directory)
- Delete: `harness/internal/metrics/` (entire directory)
- Delete: `harness/internal/rundir/` (entire directory)
- Delete: `harness/internal/goose/goose.go`
- Delete: `harness/internal/goose/acp_runner.go`
- Delete: `harness/internal/goose/acp_runner_test.go`
- Delete: `harness/internal/goose/recipe.go`
- Delete: `harness/internal/goose/recipe_test.go`
- Delete: `harness/recipes/` (entire directory)
- Delete: `harness/skill-bundle/` (entire directory)
- Delete: `images/agent-base-goose-java/Containerfile`

**Interfaces:**
- Consumes: nothing (pure deletion)
- Produces: clean harness codebase with only: `git/`, `goose/lifecycle.go`, `config/`, `logging/`, `acp/`, `watcher/`

- [ ] **Step 1: Verify no imports of deleted packages remain in main.go**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller/harness && grep -rn 'internal/detect\|internal/plan\|internal/execute\|internal/verify\|internal/fixloop\|internal/handoff\|internal/metrics\|internal/rundir\|goose\.Runner\|goose\.NewACPRunner\|goose\.ParseRecipe' cmd/ internal/`

Expected: no matches (main.go was rewritten in Task 3)

- [ ] **Step 2: Delete obsolete packages**

```bash
rm -rf harness/internal/detect \
       harness/internal/plan \
       harness/internal/execute \
       harness/internal/verify \
       harness/internal/fixloop \
       harness/internal/handoff \
       harness/internal/metrics \
       harness/internal/rundir
```

- [ ] **Step 3: Delete obsolete goose files**

```bash
rm harness/internal/goose/goose.go \
   harness/internal/goose/acp_runner.go \
   harness/internal/goose/acp_runner_test.go \
   harness/internal/goose/recipe.go \
   harness/internal/goose/recipe_test.go
```

- [ ] **Step 4: Delete recipes and skill-bundle**

```bash
rm -rf harness/recipes \
       harness/skill-bundle
```

- [ ] **Step 5: Delete the monolithic Containerfile**

```bash
rm images/agent-base-goose-java/Containerfile
rmdir images/agent-base-goose-java 2>/dev/null || true
```

- [ ] **Step 6: Run go mod tidy to clean unused dependencies**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller/harness && go mod tidy`

Expected: unused dependencies (e.g., yaml.v3 from recipe.go) are removed from go.mod

- [ ] **Step 7: Verify harness builds and tests pass**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller/harness && go build ./cmd/migration-harness/ && go test ./...`

Expected: builds cleanly, all remaining tests pass

- [ ] **Step 8: Commit**

```bash
git add -u harness/ images/
git commit -m "chore: delete obsolete harness packages, recipes, skill-bundle, and monolithic image"
```

---

### Task 5: Create stage images (Containerfiles)

**Files:**
- Create: `images/agent-base/Containerfile`
- Create: `images/agent-plan/Containerfile`
- Create: `images/agent-execute-java/Containerfile`
- Create: `images/agent-verify-java/Containerfile`

**Interfaces:**
- Consumes: harness binary from `harness/cmd/migration-harness/`, skills from `skills/` directory (Task 6)
- Produces: 4 container images buildable with `podman build`

- [ ] **Step 1: Create agent-base Containerfile**

Create `images/agent-base/Containerfile`:

```dockerfile
# agent-base: Foundation image with goose CLI, git, and the migration harness binary.
# All stage images extend this base.
#
# Build context must be the repository root:
#   podman build -t agent-base -f images/agent-base/Containerfile .

# --- Build stage ---
FROM golang:1.26 AS builder

WORKDIR /src
COPY harness/go.mod harness/go.sum ./
RUN go mod download
COPY harness/cmd/ cmd/
COPY harness/internal/ internal/
RUN CGO_ENABLED=0 go build -o /migration-harness ./cmd/migration-harness/

# --- Runtime stage ---
FROM registry.access.redhat.com/ubi10/ubi:latest

RUN dnf install -y \
    curl \
    git \
    ca-certificates \
    && dnf clean all

# Install Goose
RUN ARCH=$(uname -m) \
    && if [ "$ARCH" = "x86_64" ]; then GOOSE_ARCH="x86_64-unknown-linux-gnu"; \
       elif [ "$ARCH" = "aarch64" ]; then GOOSE_ARCH="aarch64-unknown-linux-gnu"; \
       else echo "Unsupported architecture: $ARCH" && exit 1; fi \
    && curl -fsSL "https://github.com/block/goose/releases/download/stable/goose-${GOOSE_ARCH}.tar.bz2" -o /tmp/goose.tar.bz2 \
    && tar -xjf /tmp/goose.tar.bz2 -C /tmp \
    && mv /tmp/goose /usr/local/bin/goose \
    && chmod +x /usr/local/bin/goose \
    && rm -f /tmp/goose.tar.bz2

ENV PATH="/opt/migration-harness/bin:${PATH}"
ENV HARNESS_WORK_DIR=/workspace

COPY --from=builder /migration-harness /opt/migration-harness/bin/migration-harness

RUN mkdir -p /opt/skills /workspace /home/harness/.migration-harness \
    && useradd -r -d /home/harness -s /sbin/nologin harness \
    && chown -R harness:harness /home/harness /workspace /tmp

WORKDIR /workspace
USER harness

ENTRYPOINT ["migration-harness"]
CMD ["run"]
```

- [ ] **Step 2: Create agent-plan Containerfile**

Create `images/agent-plan/Containerfile`:

```dockerfile
# agent-plan: Plan stage image. Adds graphify for code graph generation.
#
# Build context must be the repository root:
#   podman build -t agent-plan -f images/agent-plan/Containerfile .

FROM agent-base AS base

USER root

RUN dnf install -y \
    python3 \
    python3-pip \
    python3-devel \
    gcc \
    gcc-c++ \
    && dnf clean all

RUN python3 -m pip install --no-cache-dir graphifyy==0.7.17

ENV PYTHONUNBUFFERED=1

COPY skills/plan/ /opt/skills/plan/

USER harness

ENTRYPOINT ["migration-harness"]
CMD ["run"]
```

- [ ] **Step 3: Create agent-execute-java Containerfile**

Create `images/agent-execute-java/Containerfile`:

```dockerfile
# agent-execute-java: Execute stage image for Java migrations.
# Adds JDK 21 and Maven for building/compiling Java projects.
#
# Build context must be the repository root:
#   podman build -t agent-execute-java -f images/agent-execute-java/Containerfile .

FROM agent-base AS base

USER root

RUN dnf install -y \
    java-21-openjdk-devel \
    maven \
    && dnf clean all

RUN JAVA_HOME_PATH=$(dirname $(dirname $(readlink -f $(which java)))) \
    && ln -sf "$JAVA_HOME_PATH" /usr/lib/jvm/java-current
ENV JAVA_HOME=/usr/lib/jvm/java-current
ENV PATH="${JAVA_HOME}/bin:${PATH}"

COPY skills/execute-java/ /opt/skills/execute/

USER harness

ENTRYPOINT ["migration-harness"]
CMD ["run"]
```

- [ ] **Step 4: Create agent-verify-java Containerfile**

Create `images/agent-verify-java/Containerfile`:

```dockerfile
# agent-verify-java: Verify stage image for Java migrations.
# Same toolchain as execute (JDK + Maven) for running builds and tests.
#
# Build context must be the repository root:
#   podman build -t agent-verify-java -f images/agent-verify-java/Containerfile .

FROM agent-base AS base

USER root

RUN dnf install -y \
    java-21-openjdk-devel \
    maven \
    && dnf clean all

RUN JAVA_HOME_PATH=$(dirname $(dirname $(readlink -f $(which java)))) \
    && ln -sf "$JAVA_HOME_PATH" /usr/lib/jvm/java-current
ENV JAVA_HOME=/usr/lib/jvm/java-current
ENV PATH="${JAVA_HOME}/bin:${PATH}"

COPY skills/verify-java/ /opt/skills/verify/

USER harness

ENTRYPOINT ["migration-harness"]
CMD ["run"]
```

- [ ] **Step 5: Verify Containerfile syntax**

Run: `for f in images/agent-base/Containerfile images/agent-plan/Containerfile images/agent-execute-java/Containerfile images/agent-verify-java/Containerfile; do echo "--- $f ---"; podman build --no-cache --layers=false -f "$f" --target=builder . 2>&1 | tail -5 || echo "syntax ok"; done`

Expected: builder stages parse without Dockerfile syntax errors (full builds require skills from Task 6)

- [ ] **Step 6: Commit**

```bash
git add images/agent-base/ images/agent-plan/ images/agent-execute-java/ images/agent-verify-java/
git commit -m "feat: add stage-specific Containerfiles (base, plan, execute-java, verify-java)"
```

---

### Task 6: Write skills (plan, execute-java, verify-java)

**Files:**
- Create: `skills/plan/SKILL.md`
- Create: `skills/execute-java/SKILL.md`
- Create: `skills/execute-java/references/javaee-quarkus.md` (copy from `harness/skill-bundle/goose-migration/references/javaee-quarkus.md`)
- Create: `skills/verify-java/SKILL.md`

**Interfaces:**
- Consumes: knowledge from deleted packages (`detect/`, `plan/`, `execute/`, `verify/`, `fixloop/`) and deleted recipes (`recipes/*.yaml`)
- Produces: 3 skill files baked into stage images, each instructing goose how to perform one stage

- [ ] **Step 1: Write plan skill**

Create `skills/plan/SKILL.md`:

```markdown
---
name: migration-plan
description: >
  Runs graphify to generate the code graph, analyzes the project, and produces
  PLAN.md with structured migration items. Does NOT modify source files.
---

# Plan Stage Skill

You are a migration planner. Your job is to analyze a project and produce
a detailed `PLAN.md` at the repo root. Do NOT modify any source files.

## Steps

### 1. Generate the code graph

Run graphify on the project:

```bash
graphify update
```

This produces `graph.json` in the repo root.

### 2. Read project structure

- Read `graph.json` to understand the project architecture:
  - Communities (architectural layers)
  - God nodes (high-degree, high-risk files)
  - File relationships (edges)
- Read the build manifest (`pom.xml`, `package.json`, etc.) — always one file
- Read 5-8 complex source files that need structural changes (MDB classes,
  security config, lifecycle listeners)

### 3. Read the migration context

Your overall migration goal is provided in the prompt above under
"Migration Context" and "Stage Task". Use these to understand:
- What needs to change (e.g., Java EE → Quarkus)
- Target state
- Constraints

### 4. Write PLAN.md

Write `PLAN.md` to the repo root with this structure:

```markdown
# PLAN.md

## Goal
<one sentence goal>

## Project Summary
- Type: <Maven/Node/Python/.NET/etc>
- Files affected: <N>
- Estimated complexity: <Low/Medium/High>
- Hardest steps: <list 1-3 most complex items>

## Steps

### Step 1: <title>
- File: <exact path>
- Action: CREATE | MODIFY | DELETE
- What to do: <specific instructions>
- Why: <reason>
- Depends on: <step numbers or "none">
- Verify: <how to confirm>

### Step 2: ...
```

#### Rules for steps

1. **One file per step** — never combine two files
2. **Exact paths** — real paths from graph.json, not placeholders
3. **Layer order** — build config → app config → utils → persistence → models → services → REST → cleanup
4. **Flag hard steps** — prefix with `⚠️ COMPLEX:` for MDB, JNDI, architectural changes
5. **DELETE steps last** — after all modifications
6. **Dependency order** — steps that others depend on come first

### 5. Write result

After writing PLAN.md, append your result to `.konveyor/result.json`:

```bash
mkdir -p .konveyor
```

If the file exists, read it, parse the JSON array, append your entry,
and write it back. If it doesn't exist, create it with a single-entry array.

Your entry:

```json
{"stage": "plan", "status": "succeeded"}
```

Or on failure:

```json
{"stage": "plan", "status": "failed", "reason": "<what went wrong>"}
```

## Important

- Do NOT modify source files — planning only
- Do NOT execute any migration steps
- Do NOT skip graphify — the graph is essential for later stages
- Read selectively — the graph gives you most of what you need
```

- [ ] **Step 2: Copy the Java EE → Quarkus reference**

```bash
mkdir -p skills/execute-java/references
cp harness/skill-bundle/goose-migration/references/javaee-quarkus.md skills/execute-java/references/
```

- [ ] **Step 3: Write execute-java skill**

Create `skills/execute-java/SKILL.md`:

```markdown
---
name: migration-execute-java
description: >
  Reads PLAN.md and executes each migration step sequentially. Applies
  Java EE → Quarkus transformations file by file. Writes modified source
  files to disk.
---

# Execute Stage Skill (Java)

You are a migration executor. Read `PLAN.md` (produced by the plan stage)
and execute every step in order. The reference file at
`/opt/skills/execute/references/javaee-quarkus.md` contains the pattern
catalog for Java EE → Quarkus transformations.

## Startup

1. Read `PLAN.md` from the repo root — read it ONCE, work through the list
2. Read `/opt/skills/execute/references/javaee-quarkus.md` for transformation patterns
3. Begin executing steps in order

## Execution Loop

For each step in PLAN.md:

1. Read the target file
2. Apply the transformation described in the step
3. Write the modified file
4. Move to the next step immediately — do NOT wait for confirmation

### Guardrails

- You MUST attempt every item in PLAN.md in order. Do not skip items.
- After completing each item, note it mentally before moving to the next.
- Do not re-read PLAN.md after every item — read it once, work through the list.
- If you cannot complete an item, note the reason and move to the next.
  Do not get stuck on one item.

## Common Java EE → Quarkus Transformations

### Import replacements
- `javax.ejb.*` → `jakarta.enterprise.context.*` + `jakarta.inject.*`
- `javax.persistence.*` → `jakarta.persistence.*`
- `javax.ws.rs.*` → `jakarta.ws.rs.*`
- `javax.inject.*` → `jakarta.inject.*`

### Annotation replacements
- `@Stateless` → `@ApplicationScoped`
- `@Stateful` → `@ApplicationScoped`
- `@EJB` → `@Inject`
- Remove `@Local`, `@Remote`

### MDB conversions (⚠️ COMPLEX)
See the reference file for before/after patterns. These require
structural changes, not just import swaps.

### pom.xml
- Change `<packaging>war</packaging>` → `<packaging>jar</packaging>`
- Remove `javaee-api` dependency
- Add Quarkus BOM and extensions

### Config files
- Create `application.properties` from `persistence.xml` and `web.xml` settings
- Delete legacy XML config files (`persistence.xml`, `web.xml`, `beans.xml`)

## Completion

After executing all steps, append your result to `.konveyor/result.json`:

Read the existing file (it should have the plan stage entry), parse the
JSON array, append your entry, and write it back.

Your entry:

```json
{"stage": "execute", "status": "succeeded"}
```

Or on failure:

```json
{"stage": "execute", "status": "failed", "reason": "<what went wrong>"}
```

## Important

- Work through ALL items — completeness matters more than perfection
- Follow the reference file for complex patterns (MDB, JNDI)
- Do NOT run builds or tests — that is the verify stage's job
- Do NOT modify PLAN.md
```

- [ ] **Step 4: Write verify-java skill**

Create `skills/verify-java/SKILL.md`:

```markdown
---
name: migration-verify-java
description: >
  Runs mvn clean compile, parses errors, applies conservative fixes,
  and iterates until the build passes or max iterations are reached.
---

# Verify Stage Skill (Java)

You are a migration verifier. Your job is to run the build, identify
errors, and fix them iteratively until the build passes.

## Steps

### 1. Run the build

```bash
mvn clean compile 2>&1 | tail -50
```

If the build succeeds (exit code 0), skip to step 4.

### 2. Fix errors

For each compiler error:

1. Read the error message to identify the file and issue
2. Read the source file
3. Apply a minimal, conservative fix
4. Do NOT change code that isn't related to the error

Common errors and fixes:

| Error | Fix |
|---|---|
| `package javax.* does not exist` | Replace remaining `javax.*` import with `jakarta.*` |
| `cannot find symbol` for CDI annotations | Add missing Quarkus extension to pom.xml |
| `cannot find symbol` for removed class | Check if a deleted interface/class is still referenced; update the reference |
| Missing `application.properties` keys | Add the required config property |

### 3. Re-verify

After fixing errors, run the build again:

```bash
mvn clean compile 2>&1 | tail -50
```

Repeat steps 2-3 up to the number of iterations specified by
`KONVEYOR_PARAM_MAX_FIX_ITERATIONS` (read from environment, default 3).

If the build still fails after max iterations, report failure.

### 4. Run tests (if build passes)

```bash
mvn test 2>&1 | tail -80
```

Report test results but do NOT attempt to fix failing tests.

### 5. Write result

Append your result to `.konveyor/result.json`:

Read the existing file (it should have plan and execute entries),
parse the JSON array, append your entry, and write it back.

Your entry on success:

```json
{"stage": "verify", "status": "succeeded"}
```

On failure:

```json
{"stage": "verify", "status": "failed", "reason": "mvn compile failed after N fix iterations"}
```

## Important

- Fixes must be minimal and conservative — don't rewrite working code
- Only fix compiler errors, not warnings
- Do NOT modify PLAN.md or files unrelated to the error
- Read KONVEYOR_PARAM_MAX_FIX_ITERATIONS from environment for iteration cap
```

- [ ] **Step 5: Verify skill files exist and are valid**

Run: `for f in skills/plan/SKILL.md skills/execute-java/SKILL.md skills/execute-java/references/javaee-quarkus.md skills/verify-java/SKILL.md; do echo "--- $f ---"; head -5 "$f"; done`

Expected: all 4 files exist with correct frontmatter

- [ ] **Step 6: Commit**

```bash
git add skills/
git commit -m "feat: add plan, execute-java, and verify-java skills"
```

---

### Task 7: Update hack/ with AgentPlaybook + AgentPlaybookRun example resources

**Files:**
- Create: `hack/harness-test/playbook-resources.yaml`
- Modify: `hack/harness-test/resources.yaml` (update Agent CRs for new images)

**Interfaces:**
- Consumes: CRD types from `api/v1alpha1/` (Agent, AgentPlaybook, AgentPlaybookRun)
- Produces: example YAML resources for testing the full playbook flow

- [ ] **Step 1: Create playbook example resources**

Create `hack/harness-test/playbook-resources.yaml`:

```yaml
# Example resources for testing the AgentPlaybook + AgentPlaybookRun flow.
# Migrates the coolstore Java EE app to Quarkus using 3 stages.
#
# Prerequisites:
#   - LLMProvider "gcp-vertex-ai" from resources.yaml
#   - git-credentials Secret from setup.sh
#
# Usage:
#   kubectl apply -f hack/harness-test/resources.yaml
#   kubectl apply -f hack/harness-test/playbook-resources.yaml

---
apiVersion: konveyor.io/v1alpha1
kind: Agent
metadata:
  name: migration-plan-agent
spec:
  image: quay.io/konveyor/agent-plan:dev
  providers:
    - ref: gcp-vertex-ai
  params:
    - name: max_turns
      type: number
      default: "200"

---
apiVersion: konveyor.io/v1alpha1
kind: Agent
metadata:
  name: migration-execute-java
spec:
  image: quay.io/konveyor/agent-execute-java:dev
  providers:
    - ref: gcp-vertex-ai
  params:
    - name: max_turns
      type: number
      default: "200"

---
apiVersion: konveyor.io/v1alpha1
kind: Agent
metadata:
  name: migration-verify-java
spec:
  image: quay.io/konveyor/agent-verify-java:dev
  providers:
    - ref: gcp-vertex-ai
  params:
    - name: max_turns
      type: number
      default: "200"
    - name: max_fix_iterations
      type: number
      default: "3"

---
apiVersion: konveyor.io/v1alpha1
kind: AgentPlaybook
metadata:
  name: java-ee-to-quarkus
spec:
  guide: "Migrate a Java EE application to Quarkus 3"
  stages:
    - name: plan
      agentRef: migration-plan-agent
      instructions: "Analyze the project structure using graphify and produce PLAN.md with migration steps"
    - name: execute
      agentRef: migration-execute-java
      instructions: "Execute each step in PLAN.md to migrate the code from Java EE to Quarkus"
    - name: verify
      agentRef: migration-verify-java
      instructions: "Run mvn clean compile, fix any compilation errors, then run tests"

---
apiVersion: konveyor.io/v1alpha1
kind: AgentPlaybookRun
metadata:
  name: coolstore-migration
spec:
  playbookRef: java-ee-to-quarkus
  models:
    - role: primary
      provider: gcp-vertex-ai
      model: claude-sonnet-4-5
  env:
    - name: GIT_REPO_URL
      value: "https://github.com/savitharaghunathan/coolstore.git"
    - name: GIT_TOKEN
      valueFrom:
        secretKeyRef:
          name: git-credentials
          key: token
    - name: GIT_TARGET_BRANCH
      value: "konveyor/coolstore-migration"
    - name: GCP_PROJECT_ID
      value: "__GCP_PROJECT_ID__"
    - name: GCP_LOCATION
      value: "global"
```

- [ ] **Step 2: Update resources.yaml — keep LLMProvider, keep legacy Agent for backwards compat**

The existing `resources.yaml` has LLMProvider and the monolithic Agent CR. Keep
the LLMProvider (it's used by both old and new flows). Add a comment noting the
new agents are in `playbook-resources.yaml`. Remove the old AgentRun CR since
users should now use AgentPlaybookRun.

In `hack/harness-test/resources.yaml`, remove the `AgentRun` CR (lines 39-60 starting at `---` before the AgentRun). Keep the LLMProvider and Agent. Add a comment at the top:

```yaml
# Harness integration test resources for Kind.
# Creates: Secret → LLMProvider → Agent (legacy monolithic agent)
#
# For the new playbook flow, also apply playbook-resources.yaml
# which creates per-stage Agents + AgentPlaybook + AgentPlaybookRun.
#
# Usage:
#   hack/harness-test/setup.sh
```

- [ ] **Step 3: Verify YAML syntax**

Run: `python3 -c "import yaml; [yaml.safe_load_all(open(f)) for f in ['hack/harness-test/playbook-resources.yaml', 'hack/harness-test/resources.yaml']]" && echo "YAML OK"`

Expected: "YAML OK"

- [ ] **Step 4: Commit**

```bash
git add hack/harness-test/playbook-resources.yaml hack/harness-test/resources.yaml
git commit -m "feat: add AgentPlaybook + AgentPlaybookRun example resources for coolstore"
```

---

### Task 8: End-to-end build verification

**Files:**
- No new files — verification only

**Interfaces:**
- Consumes: all files from Tasks 1-7
- Produces: confirmation that everything compiles, tests pass, and images build

- [ ] **Step 1: Run full controller tests**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller && go test ./... -count=1`

Expected: all tests pass

- [ ] **Step 2: Run full harness tests**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller/harness && go test ./... -count=1`

Expected: all tests pass

- [ ] **Step 3: Run go vet on both modules**

Run:
```bash
cd /Users/sraghuna/local_dev/konveyor/agentic-controller && go vet ./...
cd /Users/sraghuna/local_dev/konveyor/agentic-controller/harness && go vet ./...
```

Expected: no issues

- [ ] **Step 4: Build agent-base image**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller && podman build -t agent-base -f images/agent-base/Containerfile .`

Expected: image builds successfully

- [ ] **Step 5: Build agent-plan image**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller && podman build -t agent-plan -f images/agent-plan/Containerfile .`

Expected: image builds successfully (depends on agent-base)

- [ ] **Step 6: Build agent-execute-java image**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller && podman build -t agent-execute-java -f images/agent-execute-java/Containerfile .`

Expected: image builds successfully (depends on agent-base)

- [ ] **Step 7: Build agent-verify-java image**

Run: `cd /Users/sraghuna/local_dev/konveyor/agentic-controller && podman build -t agent-verify-java -f images/agent-verify-java/Containerfile .`

Expected: image builds successfully (depends on agent-base)

- [ ] **Step 8: Verify skill discovery in the built images**

Run:
```bash
podman run --rm agent-plan ls /opt/skills/plan/SKILL.md
podman run --rm agent-execute-java ls /opt/skills/execute/SKILL.md
podman run --rm agent-verify-java ls /opt/skills/verify/SKILL.md
```

Expected: each file exists

- [ ] **Step 9: Commit any fixes from verification**

If any issues were found and fixed, commit them:

```bash
git add -u
git commit -m "fix: address issues found during end-to-end verification"
```
