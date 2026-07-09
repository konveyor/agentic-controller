# migration-harness

**AI-powered code migration CLI** that orchestrates [goose](https://github.com/block/goose) through a 5-step pipeline: **detect → plan → execute → verify → fix-loop**.

Written in Go. Manages credential-isolated git lifecycle — the LLM agent never sees git credentials.

---

## Quick Start

```bash
# Build
go build -o migration-harness ./cmd/migration-harness/

# Configure (one-time)
./migration-harness init

# Run a migration (local repo)
./migration-harness run /path/to/your/app "Migrate this Java EE app to Quarkus"

# Run with git clone + push (CI/automated)
GIT_REPO_URL=https://github.com/org/repo.git \
GIT_TARGET_BRANCH=migration-output \
GIT_TOKEN=ghp_xxx \
./migration-harness run --auto-approve https://github.com/org/repo.git "Migrate to Quarkus"
```

---

## How It Works

```
┌─────────────┐
│ 1. Detect   │  AST code graph via graphify (zero LLM tokens)
└─────────────┘
      ↓
┌─────────────┐
│ 2. Plan     │  LLM generates PLAN.md → human approval gate
└─────────────┘
      ↓
┌─────────────┐
│ 3. Execute  │  Per-item migration with git commit + push after each
└─────────────┘
      ↓
┌─────────────┐
│ 4. Verify   │  Build + test, auto-fixes compilation errors
└─────────────┘
      ↓
┌─────────────┐
│ 5. Fix-loop │  Iterative fixes if verify failed (up to N iterations)
└─────────────┘
```

---

## Prerequisites

- **Go 1.21+** (to build)
- **[goose](https://github.com/block/goose)** CLI (configured with a provider — run `goose configure`)
- **[graphify](https://github.com/graphify-ai/graphifyy)** CLI (`pip install graphifyy` or `uv tool install graphifyy`)
- **git**

---

## Installation

```bash
git clone https://github.com/konveyor/migration-harness.git
cd migration-harness
go build -o migration-harness ./cmd/migration-harness/
```

Place the binary somewhere in your `PATH`, alongside the `recipes/` and `skill-bundle/` directories (the binary resolves these relative to its own location).

---

## Configuration

### First-time setup

```bash
./migration-harness init
```

Prompts for:
- LLM provider (e.g. `anthropic`, `gcp_vertex_ai`, `openai`)
- Model name (e.g. `claude-sonnet-4-6`)
- Max turns per step (default: 200)
- Max fix iterations (default: 3)

Saves to `~/.migration-harness/config`.

### Environment variables (for CI / git-managed runs)

| Variable | Required | Description |
|----------|----------|-------------|
| `GIT_REPO_URL` | No | HTTPS URL to clone. If unset, uses local repo path. |
| `GIT_TOKEN` | If `GIT_REPO_URL` set | GitHub/GitLab token for clone + push. Cleared from env after reading. |
| `GIT_USERNAME` | No | Git username (default: `x-access-token`) |
| `GIT_TARGET_BRANCH` | No | Branch to push results to (default: `konveyor-migrate-<timestamp>`) |

---

## Usage

### Run a full migration

```bash
# Local repo, interactive (prompts for plan approval)
./migration-harness run /path/to/repo "Migrate from Spring Boot to Quarkus"

# Automated (skip approval prompt)
./migration-harness run --auto-approve /path/to/repo "Migrate from Java EE to Quarkus"
```

### Other commands

```bash
# Show status of latest run
./migration-harness status

# Resume incomplete migration (not yet implemented)
./migration-harness resume

# Run a single step (not yet implemented)
./migration-harness step verify /path/to/repo
```

---

## Git Lifecycle

When `GIT_REPO_URL` is set:

1. **Clone** — harness clones the repo to a temp directory
2. **Strip credentials** — removes auth from the git remote config
3. **Clear env** — `GIT_TOKEN` is unset from the process environment
4. **Checkout branch** — creates/checks out `GIT_TARGET_BRANCH`
5. **Commit after each item** — every execute step and fix iteration gets its own commit
6. **Push after each commit** — results are pushed incrementally
7. **Final handoff** — session metadata committed and pushed at exit

The goose agent only sees the working directory — never git credentials.

---

## Output Artifacts

**In the repo:**
- `PLAN.md` — migration plan
- `.goosehints` — execution hints for goose
- `execution-log.md` — step-by-step progress
- `verification-report.md` — build/test results
- `fix-loop-report.md` — fix iteration history
- `.konveyor/session.json` — machine-readable session state
- `.konveyor/handoff.md` — human-readable handoff summary

**In `~/.migration-harness/runs/<name>-<timestamp>/`:**
- `detect.json`, `graph.json` — code analysis
- `plan.json`, `PLAN.md` — structured + readable plan
- `item-*.json` — per-item execution results
- `verify.json` — verification results
- `logs/*.json` — raw goose conversation logs
- `metrics.json` — timing and status

---

## Architecture

```
cmd/migration-harness/main.go    CLI entry point (cobra)
internal/
├── config/        Config file (~/.migration-harness/config)
├── detect/        Step 1: graphify AST extraction + manifest check
├── plan/          Step 2: recipe rendering, PLAN.md parsing, approval
├── execute/       Step 3: per-item goose invocation + git commit
├── verify/        Step 4: build/test verification via goose
├── fixloop/       Step 5: iterative error fixing
├── goose/         goose CLI wrapper (os/exec, JSON extraction)
├── git/           Credential-isolated git operations (go-git)
├── handoff/       Session + handoff file generation
├── metrics/       Timing and status tracking
├── rundir/        Run directory management
└── logging/       Colored terminal output
recipes/
├── execute.yaml   Per-item migration recipe
├── verify.yaml    Build + test verification recipe
└── fix.yaml       Single-error fix recipe
skill-bundle/goose-migration/
├── SKILL.md       Planner skill
├── references/    Migration pattern docs
└── skills/        Execution sub-skills
```

### Key design decisions

- **goose via os/exec** — invokes `goose run --recipe <file> --output-format json` per step. Extracts `recipe__final_output` from the conversation JSON. Future: ACP client for direct API integration.
- **go-git** — all git operations use `github.com/go-git/go-git/v5` with `http.BasicAuth`. No shell-out to git CLI.
- **Credential isolation** — credentials are read from env, used only in go-git calls, never passed to goose or written to disk.
- **Force push** — target branch is force-pushed (migration output is ephemeral, not collaborative).

---

## License

Apache-2.0
