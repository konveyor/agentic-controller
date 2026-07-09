# Port migration-harness from Bash/Python to Go

## Context

migration-harness is a 5-step AI migration CLI (detect → plan → execute → verify → fix-loop) currently implemented as ~1,200 lines of bash + 120 lines of Python. Porting to Go gives us a single distributable binary, proper error handling, testability, and maintainability. Beyond the port, the Go harness adds credential-isolated git lifecycle management and ACP transport integration (stubbed for now).

**Key decisions:**
- `graphify extract <path>` via `os/exec` replaces `build-graph.py` (no Python in the Go binary)
- **Goose serve** (`goose serve --port 4000`) is the runtime model — single long-lived process, harness sends instructions via ACP HTTP. **Stubbed for now** — someone else implements the ACP client; we define the interface.
- Recipes stay as YAML files on disk — the ACP client reads them and sends instructions to goose
- Harness owns the git lifecycle: clone with credentials, strip credentials from workspace, incremental commits, push on exit. **Agent never receives git credentials.**
- Harness owns `.konveyor/session.json` and `.konveyor/handoff.md` schema
- Context budget computation is deferred (skip in POC)
- Single cobra binary with subcommands

## Go Project Layout

```
go.mod
cmd/migration-harness/main.go       # cobra root + subcommands
internal/
  config/config.go                   # load/save config
  logging/logging.go                 # colored stderr output
  rundir/rundir.go                   # run directory management

  git/git.go                         # credential-isolated git lifecycle
  git/credentials.go                 # read credentials from env vars
  git/git_test.go

  acp/client.go                      # ACP client interface (STUB — implemented by others)

  detect/detect.go                   # step 1: manifests + graphify CLI + detect.json
  plan/plan.go                       # step 2: orchestrator
  plan/context.go                    # pre-gather context
  plan/recipe.go                     # render plan recipe YAML
  plan/turns.go                      # calculate turn budget
  plan/parser.go                     # PLAN.md → plan.json
  plan/hints.go                      # write .goosehints
  plan/approval.go                   # human approval gate
  execute/execute.go                 # step 3: per-item loop with resume + git commit per item
  verify/verify.go                   # step 4: interactive verification
  fixloop/fixloop.go                 # step 5: bounded fix iteration
  metrics/metrics.go                 # timing + metrics.json
  handoff/handoff.go                 # .konveyor/session.json + handoff.md generation
recipes/                             # kept as-is (execute.yaml, verify.yaml, fix.yaml)
skill-bundle/                        # kept as-is
```

Dependencies: `github.com/spf13/cobra`, `golang.org/x/term`, `github.com/go-git/go-git/v5`. Everything else is stdlib.

---

## Phase 1: Skeleton + Foundation

**Packages:** `config`, `logging`, `rundir`, cobra root command

| Component | What to port | Notes |
|-----------|-------------|-------|
| `logging` | `info()`, `ok()`, `warn()`, `err()`, `fatal()`, `header()` from `common.sh` | ANSI colors to stderr, detect TTY via `x/term` |
| `config` | `load_config()`, `save_config()` from `common.sh` | Parse `MH_KEY="value"` format, `Config` struct with `Model`, `Provider`, `MaxTurns`, `MaxFixIterations` |
| `rundir` | `new_run_dir()`, `latest_run_dir()` from `common.sh` | Creates `~/.migration-harness/runs/<repo>-<timestamp>/logs/` |
| `main.go` | `cmd_init` from `bin/migration-harness` | Auto-detect provider/model from `~/.config/goose/config.yaml`, interactive prompts, `checkDeps()` for goose/graphify/git |

**Tests:** config round-trip, rundir creation, latest-run lookup.

**Deliverable:** `migration-harness init` and `--help` work.

---

## Phase 2: Git Credential-Isolated Lifecycle

**Package:** `git` — the harness manages all git operations; agent never touches credentials.

### Credential Model

Credentials come from environment variables:
- `GIT_USERNAME` — git username (or token username like `x-access-token`)
- `GIT_TOKEN` — personal access token or app installation token
- `GIT_REPO_URL` — source repo HTTPS URL (e.g., `https://github.com/org/repo.git`)
- `GIT_TARGET_BRANCH` — branch to create/checkout for the migration work (default: `konveyor-migrate-<timestamp>`)

### Git Lifecycle (in order)

```
1. ReadCredentials()       — read GIT_USERNAME, GIT_TOKEN, GIT_REPO_URL from env
2. Clone(credURL, dir)     — git clone using credential-embedded URL
                             (https://user:token@github.com/org/repo.git)
3. StripCredentials(dir)   — reconfigure remote origin to bare URL (no credentials)
                             so any git push from inside the workspace fails
4. CheckoutBranch(dir, branch) — create or checkout the target branch
5. CommitStep(dir, msg)    — git add -A && git commit (called after each plan item)
6. CommitHandoff(dir)      — commit .konveyor/handoff.md + .konveyor/session.json
7. Push(dir, branch)       — temporarily re-inject credentials into remote URL,
                             git push, then strip credentials again
8. WriteResults(dir)       — write .konveyor/results.json (pod-local, not pushed)
```

### Implementation Details (using go-git)

Uses `github.com/go-git/go-git/v5` — pure Go, no `git` binary dependency. Auth is passed per-operation (clone, push), never stored in remote URLs.

**`git/credentials.go`:**
```go
type Credentials struct {
    Username string
    Token    string
    RepoURL  string   // bare HTTPS URL
    Branch   string
}

func ReadFromEnv() (*Credentials, error)   // reads env vars, validates
func (c *Credentials) Auth() *http.BasicAuth {
    return &http.BasicAuth{Username: c.Username, Password: c.Token}
}
```

**`git/git.go`:**
```go
func Clone(ctx context.Context, cred *Credentials, destDir string) (*git.Repository, error)
    // git.PlainCloneContext(ctx, destDir, false, &git.CloneOptions{
    //     URL: cred.RepoURL,   // bare URL — no creds in URL ever
    //     Auth: cred.Auth(),   // auth passed per-operation
    // })

func StripCredentials(repo *git.Repository) error
    // Set remote origin URL to bare URL (defense-in-depth).
    // With go-git auth is per-call so this is mostly symbolic,
    // but ensures any external `git push` from the agent also fails.
    // repo.Remote("origin").Config().URLs = []string{bareURL}

func CheckoutBranch(repo *git.Repository, branch string) error
    // worktree.Checkout(&git.CheckoutOptions{
    //     Branch: plumbing.NewBranchReferenceName(branch),
    //     Create: true,  // try create first, fall back to checkout existing
    // })

func CommitAll(repo *git.Repository, message string) (plumbing.Hash, error)
    // worktree.Add(".") then worktree.Commit(message, &git.CommitOptions{All: true})
    // Returns zero hash + nil if working tree is clean (no-op)

func Push(ctx context.Context, cred *Credentials, repo *git.Repository, branch string) error
    // repo.PushContext(ctx, &git.PushOptions{
    //     Auth:       cred.Auth(),   // credentials injected only here
    //     RemoteName: "origin",
    //     RefSpecs:   []config.RefSpec{"refs/heads/<branch>:refs/heads/<branch>"},
    // })
```

Credentials never appear in URLs or logs. Auth is passed as `*http.BasicAuth` per-operation — the remote URL stays bare at all times. No need for inject/strip dance on push.

### Security Notes (POC limitations)
- In the POC (single-container Sandbox), the credential exists in env vars and a determined agent could read `/proc/self/environ`. This reduces **accidental** exposure (logging, prompt injection) but does not provide full isolation.
- Full isolation requires OpenShell filesystem policy enforcement (future).
- The harness should `os.Unsetenv("GIT_TOKEN")` after reading credentials to reduce the window, but this is defense-in-depth, not a security boundary.

---

## Phase 3: ACP Client Interface (STUB)

**Package:** `acp` — defines the interface for communicating with goose serve. **Implementation deferred** — someone else builds this.

```go
// Client is the interface for ACP communication with the agent runtime.
// The harness launches `goose serve --port <port>` and uses this client
// to send instructions and receive responses.
type Client interface {
    // SendInstruction sends a message/instruction to the agent and returns
    // the agent's response. The instruction content comes from recipe YAML
    // files or dynamically rendered plan recipes.
    SendInstruction(ctx context.Context, instruction string, params map[string]string) (Response, error)

    // StreamEvents returns a channel of SSE events from the agent for
    // observability (tool calls, thinking, progress).
    StreamEvents(ctx context.Context) (<-chan Event, error)

    // Close shuts down the connection.
    Close() error
}

type Response struct {
    Content json.RawMessage
    Done    bool
}

type Event struct {
    Type string          // "tool_call", "thinking", "progress", "error"
    Data json.RawMessage
}
```

**Stub implementation:** `StubClient` that returns `ErrNotImplemented` for all methods. The pipeline steps call through this interface — when the real ACP client is ready, it drops in.

**Launch helper (implemented now):**
```go
// LaunchGooseServe starts `goose serve --port <port>` as a background process.
// Returns the process so the harness can shut it down on exit.
func LaunchGooseServe(ctx context.Context, port int) (*os.Process, error)
```

---

## Phase 4: Detect Step

**Package:** `detect` — replaces `step-detect.sh` + `build-graph.py`

| Sub-step | Bash equivalent | Go implementation |
|----------|----------------|-------------------|
| Check manifests | `test -f pom.xml` etc. | `os.Stat` for each manifest, return `map[string]bool` |
| Build code graph | `python3 build-graph.py` | `exec.Command("graphify", "extract", repoPath)`, stream stderr |
| Count files by ext | `jq` queries on graph.json | Parse graph.json nodes, count by `.source_file` extension |
| Write detect.json | `jq -n` | Marshal `DetectResult` struct to JSON |

After graphify: copy `graphify-out/graph.json` and `GRAPH_REPORT.md` to run dir. Parse graph.json for stats.

---

## Phase 5: Plan Step

**Package:** `plan` — the most complex step. Replaces `step-plan.sh`.

| Component | Bash/Python equivalent | Notes |
|-----------|----------------------|-------|
| `context.go` | `_pregather_context()` | Read detect.json + graph.json, format sections: detection summary, graph overview, communities, god nodes, cross-boundary edges, file tree, references |
| `recipe.go` | `_render_plan_recipe()` | Read `skills/migration-plan/SKILL.md`, generate recipe YAML with skill text + context inlined as block scalars |
| `turns.go` | `_calc_plan_turns()` | Base 10, +N for patterns, +2 if no reference, +1 per 50 files, +3 if multi-language. Clamp [12, 50]. |
| `parser.go` | `_plan_md_to_json()` (Python) | **Line-by-line state machine** (Go RE2 lacks lookaheads). Parse 3 PLAN.md formats. Detect migration type. Assign layers from paths. |
| `hints.go` | `write_hints()` | Write `.goosehints` with discipline rules + checklist |
| `approval.go` | Interactive approval gate | Print PLAN.md, prompt y/edit/N. Launch `$EDITOR` on "edit". |

Note: The plan step currently calls `goose_run()` to generate the plan. In the new architecture, it will call `acp.Client.SendInstruction()` with the rendered recipe content. Until the ACP client is implemented, this step is blocked on the stub — can be tested by temporarily wiring up `goose run --recipe` via `os/exec` as a fallback.

---

## Phase 6: Execute Step

**Package:** `execute` — replaces `step-execute.sh`

Loop over `plan.json` items. For each:
1. Skip if `item-NNN.json` exists with `status: "ok"` (resume)
2. Send instruction to agent via `acp.Client.SendInstruction()` (or fallback)
3. Write `item-NNN.json` (3-digit zero-padded)
4. Append to `execution-log.md`
5. Update `.goosehints` checklist (`- [ ] N.` → `- [x] N.`)
6. **`git.CommitAll(repoDir, "migrate: <item_path>")`** — incremental commit after each item
7. At end: write `execute-summary.json` with ok/failed/skipped counts
8. Copy `execution-log.md` to repo

---

## Phase 7: Verify + Fix-Loop Steps

**Packages:** `verify`, `fixloop`

**verify:**
- Send verification instruction via ACP client (or fallback)
- Parse output: `build_ok`, test counts, `errors[]`, `fix_attempts`
- Generate `verification-report.md`, copy to repo

**fixloop:**
- If `build_ok == true`, skip
- Loop up to `MaxFixIterations` (default 3):
  - Re-verify -> if clean, done
  - Extract first error, send fix instruction via ACP
  - Write `fix-N.json`
  - **`git.CommitAll(repoDir, "fix: <error_file>")`**
  - If not fixed, break
- Write `fix-loop-report.md`

---

## Phase 8: Handoff Schema + Session Artifacts

**Package:** `handoff` — the harness owns these schemas (per controller enhancement PR #295).

### `.konveyor/session.json`

Machine-readable session record committed to the target branch for post-run audit.

```json
{
  "schema_version": "1.0",
  "session_id": "uuid",
  "started_at": "2026-07-09T10:00:00Z",
  "completed_at": "2026-07-09T10:45:00Z",
  "status": "completed|failed|partial",
  "migration_request": "Migrate Java EE to Quarkus",
  "source_repo": "https://github.com/org/app.git",
  "target_branch": "konveyor-migrate-20260709",
  "model": "gemini-2.5-pro",
  "provider": "gcp_vertex_ai",
  "pipeline": {
    "detect": {
      "status": "completed",
      "duration_seconds": 12,
      "nodes": 245,
      "edges": 380,
      "communities": 8
    },
    "plan": {
      "status": "completed",
      "duration_seconds": 35,
      "items_planned": 18,
      "reference_used": "javaee-quarkus.md"
    },
    "execute": {
      "status": "completed",
      "duration_seconds": 420,
      "items_succeeded": 16,
      "items_failed": 1,
      "items_skipped": 1
    },
    "verify": {
      "status": "completed",
      "duration_seconds": 180,
      "build_ok": true,
      "tests_passed": 42,
      "tests_failed": 0
    },
    "fix_loop": {
      "status": "skipped",
      "iterations": 0
    }
  },
  "commits": [
    {"sha": "abc123", "message": "migrate: pom.xml", "step": 1},
    {"sha": "def456", "message": "migrate: src/main/java/Foo.java", "step": 2}
  ],
  "errors": []
}
```

### `.konveyor/handoff.md`

Human-readable summary committed alongside session.json.

```markdown
# Migration Handoff

## Request
Migrate Java EE to Quarkus

## Status: Completed

## Summary
- 16 of 18 items migrated successfully
- Build passes, 42 tests passing
- 1 item failed: src/main/java/legacy/OldService.java (complex MDB pattern)
- 1 item skipped: src/test/java/OldServiceTest.java (depends on failed item)

## What Was Done
1. pom.xml — migrated to Quarkus BOM
2. src/main/java/model/User.java — javax to jakarta
...

## What Needs Manual Attention
- src/main/java/legacy/OldService.java — MDB to reactive messaging requires manual review
- src/test/java/OldServiceTest.java — skipped, depends on OldService

## Verification
- Build: passing
- Tests: 42 passed, 0 failed

## Files Changed
<git diff --stat output>
```

### Implementation

```go
// handoff/handoff.go

type Session struct { ... }  // maps to session.json schema above

func WriteSession(dir string, session *Session) error
func WriteHandoff(dir string, session *Session, plan *plan.Plan, items []execute.ItemResult) error
```

Called from the main pipeline after all steps complete (or on failure):
```go
handoff.WriteSession(repoDir, session)
handoff.WriteHandoff(repoDir, session, plan, itemResults)
git.CommitAll(repoDir, "konveyor: session handoff")
git.Push(ctx, creds, repoDir, branch)
```

---

## Phase 9: CLI Wiring + Metrics

**Packages:** `metrics`, wire up `main.go`

**metrics:**
- `TrackStepStart/End` — append to `.timings` file
- `GenerateMetrics` — read artifacts, build struct, write `metrics.json`
- Overall status: `failed` if detect/plan/execute failed, `partial` if !build_ok, else `success`

**CLI commands:**
- `run <repo-url> <request>`: full lifecycle — read creds, clone, strip creds, branch, detect->plan->execute->verify->fix-loop, handoff, push
- `run <local-path> <request>`: local mode (no git lifecycle, same as current bash behavior)
- `status`: show latest run status
- `resume`: continue from last checkpoint
- `step <name>`: run a single step

**Main pipeline orchestration (run command):**
```go
creds, err := git.ReadFromEnv()            // may be nil for local mode
if creds != nil {
    git.Clone(ctx, creds, workDir)
    git.StripCredentials(workDir)
    git.CheckoutBranch(workDir, creds.Branch)
    defer func() {
        handoff.WriteSession(workDir, session)
        handoff.WriteHandoff(workDir, session, ...)
        git.CommitAll(workDir, "konveyor: session handoff")
        git.Push(ctx, creds, workDir, creds.Branch)
    }()
}

// Pipeline steps (each updates session state)
detect.Run(ctx, workDir, runDir)
plan.Run(ctx, workDir, runDir, request, acpClient)
execute.Run(ctx, workDir, runDir, acpClient, creds)  // commits after each item
verify.Run(ctx, workDir, runDir, acpClient)
fixloop.Run(ctx, workDir, runDir, acpClient, creds)
metrics.Generate(runDir, ...)
```

---

## Implementation Order Summary

| Phase | Focus | Key packages |
|-------|-------|-------------|
| 1 | Skeleton: config, logging, rundir, cobra CLI | `config`, `logging`, `rundir` |
| 2 | **Git credential-isolated lifecycle** | `git` |
| 3 | ACP client interface (stub) | `acp` |
| 4 | Detect step (graphify CLI) | `detect` |
| 5 | Plan step (context, recipe, parser, approval) | `plan` |
| 6 | Execute step (per-item loop + git commit) | `execute` |
| 7 | Verify + fix-loop | `verify`, `fixloop` |
| 8 | **Handoff schema** (session.json, handoff.md) | `handoff` |
| 9 | CLI wiring, metrics, polish | `metrics`, `main.go` |

---

## Verification

1. `go build ./cmd/migration-harness`
2. `go test ./internal/...`
3. Git lifecycle test: clone a test repo, verify credentials stripped, commit, push with re-injected creds
4. Handoff test: verify session.json and handoff.md content after a run
5. End-to-end: full pipeline against a sample app
6. Update Dockerfile to compile Go binary
