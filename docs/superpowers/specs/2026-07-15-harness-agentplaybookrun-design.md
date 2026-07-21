# Harness + AgentPlaybookRun Integration Design

## Overview

Restructure the migration harness from a monolithic 5-stage orchestrator into a thin, uniform wrapper that runs inside AgentPlaybookRun-managed pods. Stage sequencing moves up to the controller (AgentPlaybookRun), migration intelligence moves down to goose skills, and the harness becomes pure git plumbing + goose lifecycle management.

## Principles (carried forward from AGENTS.md)

- **Git credentials stay in the harness** — goose never sees push credentials.
- **Constant pushing to git** — progress is committed and pushed regularly, not just at stage boundaries.
- **Proper handoff** — each stage reads prior-stage artifacts from the git repo (PLAN.md, migrated files, etc.).
- **Controller is domain-agnostic** — it sequences stages, it doesn't interpret migration logic.

## Sequence

```
User creates AgentPlaybookRun
  └─ AgentPlaybookRun controller
       ├─ Stage 1 (plan):
       │    ├─ Creates AgentRun (agentRef: migration-plan-agent)
       │    ├─ AgentRun controller creates Sandbox pod
       │    ├─ Pod: harness → git clone → goose serve → ACP prompt → watcher → commit+push → exit 0
       │    └─ Controller sees Succeeded → advances to stage 2
       ├─ Stage 2 (execute):
       │    ├─ Creates AgentRun (agentRef: migration-execute-java)
       │    ├─ Pod: harness → git clone (picks up PLAN.md) → goose → watcher → commit+push → exit
       │    └─ Controller sees Succeeded → advances to stage 3
       └─ Stage 3 (verify):
            ├─ Creates AgentRun (agentRef: migration-verify-java)
            ├─ Pod: harness → git clone (picks up migrated code) → goose → watcher → commit+push → exit
            └─ Controller sees Succeeded → marks run Succeeded
```

## Architecture

### Stage Sequencing

AgentPlaybookRun controller creates one AgentRun per stage, sequentially. Each AgentRun creates a Sandbox (pod) with the stage's agent image. The harness binary is the entrypoint in every image.

Three playbook stages:

1. **Plan** — runs graphify (detect), feeds output into planner skill, produces PLAN.md
2. **Execute** — goose iterates PLAN.md items, migrates each file/component
3. **Verify** — goose runs build/test, fixes errors iteratively

### Image Hierarchy (Java only)

All stage images extend a common base:

| Image | Base | Adds | Baked-in Skill |
|---|---|---|---|
| `agent-base` | ubi | goose CLI, git, harness binary, cred wiring | (none) |
| `agent-plan` | agent-base | graphify | `/opt/skills/plan/SKILL.md` |
| `agent-execute-java` | agent-base | JDK 21, Maven | `/opt/skills/execute/SKILL.md` |
| `agent-verify-java` | agent-base | JDK 21, Maven | `/opt/skills/verify/SKILL.md` |

Convention: each image places exactly one skill at `/opt/skills/<stage>/SKILL.md`. The harness discovers it via glob (`/opt/skills/*/SKILL.md`).

Adding a new language = new execute + verify images with language-specific toolchain and skills. No harness or controller changes.

### Harness Binary (Thin Wrapper)

Same binary, same behavior in every image. Does not know what stage it's running.

**Entrypoint flow (`migration-harness run`):**

1. Load config from env vars (`KONVEYOR_MODEL_PRIMARY_*`)
2. Read git creds from env (`GIT_REPO_URL`, `GIT_TOKEN`, `GIT_TARGET_BRANCH`)
3. Git clone, strip credentials from remote, checkout branch
4. Start goose serve with `--with-builtin developer` (gives goose shell access, file editing) and providerEnv wiring for LLM credentials
5. Connect via ACP WebSocket, create session (passing cloned repo path as `cwd` so goose operates in the right directory)
6. Discover skill: glob `/opt/skills/*/SKILL.md` (exactly one match)
7. Read env vars: `KONVEYOR_INSTRUCTIONS` (stage task), `KONVEYOR_PLAYBOOK_INSTRUCTIONS` (overall guide), `KONVEYOR_PROMPT` (agent-level standing instructions)
8. Start filesystem watcher (background goroutine) — must start BEFORE the blocking prompt call
9. Send single ACP prompt combining all four context layers:
   - `KONVEYOR_PROMPT` — agent-level standing instructions (from Agent CR)
   - `KONVEYOR_PLAYBOOK_INSTRUCTIONS` — overall migration context (from playbook Guide)
   - Skill content (harness reads `/opt/skills/*/SKILL.md` file and embeds it inline in the prompt)
   - `KONVEYOR_INSTRUCTIONS` — specific stage task (from playbook stage)
10. `SendPrompt()` blocks until goose finishes (see Session Completion below)
11. Stop watcher
12. Read `.konveyor/result.json` for exit status (see Failure Propagation below)
13. Final commit + push
14. Exit 0 (success) or exit 1 (failure based on result.json)

Controller determines Succeeded/Failed from pod exit code. No session.json or handoff files needed.

### Session Completion

The harness needs to know when goose has finished its autonomous work.
The ACP WebSocket session delivers messages as goose works. When the
session prompt completes, the ACP protocol returns a final response to
the `session/prompt` JSON-RPC call. The harness's blocking
`SessionClient.Prompt()` call returns at that point. This is the same
mechanism used today — the difference is there's only one prompt call
instead of many.

If goose serve crashes (process exits), the harness detects this via
`ServeProcess.Alive()` and exits with code 1.

### Failure Propagation

Each skill appends its result to `.konveyor/result.json` as its final
action. The file is a JSON array — each stage adds an entry:

```json
[
  {"stage": "plan", "status": "succeeded"},
  {"stage": "execute", "status": "succeeded"},
  {"stage": "verify", "status": "failed", "reason": "mvn compile failed after 3 fix iterations"}
]
```

The harness reads the last entry after the ACP session completes. If the
file is missing or the last entry contains `"failed"`, the harness exits
with code 1. This gives the controller a clear Succeeded/Failed signal
without the harness needing to interpret skill-specific output.

The file accumulates across stages (each stage clones the repo and picks
up prior entries), providing a complete run history in a single file.

Skills must append to this file even on failure — it's part of the skill
contract.

### Filesystem Watcher (New Component)

Runs as a background goroutine while goose works. Provides the "constant
pushing to git" guarantee without goose needing git credentials.

**Behavior:**
- Watches working directory for file changes using `fsnotify`
- After detecting changes, waits for a quiet period (~30 seconds of no new writes). The longer quiet period avoids committing mid-write — goose may pause between file writes while waiting for LLM responses, and a short quiet period could fire during that gap.
- On quiet: `git add` (tracked files + known source patterns) → `git commit` → `git push`
- Commit message: `"konveyor: auto-commit progress"`
- If push fails, logs warning and retries on next quiet period
- Final commit+push after goose exits catches anything the watcher missed

**Safe staging (not `git add -A`):**
The watcher must not blindly stage everything. Goose and its extensions
may create temp files, cache directories, or internal state in the
working directory. The watcher uses a targeted approach:
- Stage changes to files already tracked by git (`git add -u`)
- Stage new files only if they match known source patterns (`.java`, `.xml`, `.properties`, `.md`, `pom.xml`, etc.)
- Respect `.gitignore` — the repo's `.gitignore` (and a harness-managed `.gitignore` entry for `.konveyor/tmp/`) excludes goose internals and harness temp files
- Never stage files matching: `*.tmp`, `*.swp`, `.goose/`, `__pycache__/`
- `.konveyor/result.json` IS staged (it's a tracked contract file). Only `.konveyor/tmp/` is excluded.

### Inter-Stage State Passing

Git repo is the shared state boundary. Each stage clones the repo and reads artifacts left by prior stages:

- Plan stage writes: `PLAN.md`, `graph.json`, `.konveyor/result.json`
- Execute stage reads: `PLAN.md`, `graph.json` (for project structure context). Writes: migrated source files, `.konveyor/result.json`
- Verify stage reads: `PLAN.md` (to know what was migrated and the migration type), migrated source files. Writes: fix patches, `.konveyor/result.json`

Each stage can read any artifact from prior stages via git. The key
contract files are `PLAN.md` (migration plan) and `graph.json` (project
structure). Skills should reference these explicitly in their
instructions.

No PVCs, no shared volumes. Clean, auditable, recoverable.

### Branch Strategy

All stages must operate on the same git branch. `GIT_TARGET_BRANCH` is
a plain environment variable set by the user in `AgentPlaybookRun.spec.env`.
The controller forwards it to every child AgentRun unchanged (via
`pbRun.Spec.Env`). No CRD field — if the user forgets to set it, the
harness fails immediately.

**Full env var injection flow in `createAgentRunForStage()`:**

```
AgentPlaybookRun
  ├─ KONVEYOR_PLAYBOOK_INSTRUCTIONS  ← playbook.Spec.Guide
  ├─ pbRun.Spec.Env                   ← user env vars (GIT_REPO_URL, GIT_TOKEN, GIT_TARGET_BRANCH, etc.)
  └─ All set on → AgentRun.spec.env
       └─ AgentRun controller buildEnvVars()
            ├─ KONVEYOR_PARAM_*       ← from Agent params
            ├─ KONVEYOR_ACP_SECRET_KEY ← generated per run
            ├─ KONVEYOR_INSTRUCTIONS  ← stage instructions
            ├─ KONVEYOR_PROMPT        ← Agent CR prompt
            ├─ KONVEYOR_MODEL_*       ← LLM provider/model/endpoint/apiKey
            └─ All forwarded env      ← GIT_TARGET_BRANCH, GIT_REPO_URL, etc.
                 └─ Injected into Sandbox pod container
```

- Stage 1 (plan) creates the branch from the repo's default branch and
  pushes. Stages 2+ clone and checkout the existing branch, picking up
  prior-stage artifacts.
- The harness reads `GIT_TARGET_BRANCH` and fails if it's not set (no
  timestamp fallback).
- Concurrent playbook runs against the same repo use different branches
  (user sets different `GIT_TARGET_BRANCH` values).

## Skills

We author three skills. Each encodes the migration knowledge that was previously spread across harness Go packages and recipe YAML files.

### Knowledge Migration

| Deleted Harness Package | Knowledge Moves To |
|---|---|
| `detect/` | **Plan skill** — run graphify, read graph.json, identify manifests |
| `plan/` | **Plan skill** — context gathering, plan structure (items with path/action/risk/layer), PLAN.md format |
| `execute/` | **Execute skill** — iterate PLAN.md items, migration rules, per-item approach |
| `verify/` + `fixloop/` | **Verify skill** — run `mvn clean compile`, parse errors, iterative fix loop |
| `recipes/*.yaml` | Folded directly into corresponding skills as instructions |
| `handoff/` | Not needed — controller tracks stage status, git carries artifacts |
| `metrics/` | Not needed — controller records start/completion times per stage |

### Plan Skill (`/opt/skills/plan/SKILL.md`)

Absorbs: `detect/`, `plan/`, `recipes/plan.yaml` logic.

Instructs goose to:
1. Run `graphify update` on the repo to generate the code graph
2. Read `graph.json`, identify manifests (pom.xml, etc.), count source files
3. Analyze the project structure and migration requirements
4. Produce `PLAN.md` with structured items (number, path, action, risk level, layer)
5. Append result to `.konveyor/result.json`

### Execute Skill (`/opt/skills/execute/SKILL.md`)

Absorbs: `execute/`, `recipes/execute.yaml` logic.

Instructs goose to:
1. Read `PLAN.md` from the repo
2. For each plan item, migrate the file/component following migration rules
3. Work through items sequentially, one at a time
4. Apply language-specific migration patterns (Java EE → Quarkus for the Java variant)
5. Write `.konveyor/result.json` with final status

Reference docs (javaee-quarkus.md, etc.) are bundled alongside the skill.

**Iteration guardrails.** Moving plan-item iteration from deterministic
Go code to an LLM-driven skill is the riskiest change. The skill must
include explicit guardrails:
- "You MUST attempt every item in PLAN.md in order. Do not skip items."
- "After completing each item, mark it done in your working notes before moving to the next."
- "Do not re-read PLAN.md after every item — read it once, work through the list."
- "If you cannot complete an item, note the reason and move to the next. Do not get stuck."

These rules prevent the LLM from skipping items, burning context on one
item, or losing track of progress.

### Verify Skill (`/opt/skills/verify/SKILL.md`)

Absorbs: `verify/`, `fixloop/`, `recipes/verify.yaml`, `recipes/fix.yaml` logic.

Instructs goose to:
1. Run `mvn clean compile` (or equivalent build command)
2. If build succeeds, run tests
3. If errors found, apply minimal conservative fixes
4. Re-verify after each fix
5. Repeat up to N iterations (configurable via `KONVEYOR_PARAM_MAX_FIX_ITERATIONS`)
6. Append result to `.konveyor/result.json`

## Harness Codebase Changes

### Packages Kept

| Package | What stays |
|---|---|
| `git/` | Clone, StripCredentials, CommitAll, Push, CheckoutBranch, ClearEnvCredentials |
| `goose/` | StartServe, Stop, providerEnv, writeADCFile (lifecycle only) |
| `config/` | LoadFromEnv |
| `logging/` | Header, Info, Ok, Warn, Err |
| `acp/` | WSClient, SessionClient (connect, session/new, single prompt) |

### Packages Deleted

`detect/`, `plan/`, `execute/`, `verify/`, `fixloop/`, `handoff/`, `metrics/`, `rundir/`

### Files Deleted

- `harness/recipes/` (all YAML recipe files)
- `harness/skill-bundle/` (replaced by per-image skills)
- `goose/recipe.go` (recipe rendering)
- `goose/acprunner.go` (multi-turn ACP runner)

### New Package

`watcher/` — filesystem watcher with quiet-period detection and git commit+push.

### main.go

Simplified to a single `run` command. Remove `init`, `status`, `resume`, `step` subcommands. Remove the 5-step pipeline. Replace with the thin wrapper flow described above.

## Example Resources (`hack/`)

Coolstore Java EE → Quarkus example showing the full CR chain:

```yaml
# Agent CRs
Agent: migration-plan-agent       (image: agent-plan)
Agent: migration-execute-java     (image: agent-execute-java)
Agent: migration-verify-java      (image: agent-verify-java)

# Playbook
AgentPlaybook: java-ee-to-quarkus
  guide: "Migrate a Java EE application to Quarkus"
  stages:
    - name: plan
      agentRef: migration-plan-agent
      instructions: "Analyze the project and produce PLAN.md"
    - name: execute
      agentRef: migration-execute-java
      instructions: "Execute each step in PLAN.md"
    - name: verify
      agentRef: migration-verify-java
      instructions: "Run mvn clean compile, fix any errors"

# Run (user creates this)
AgentPlaybookRun: coolstore-migration
  playbookRef: java-ee-to-quarkus
  models: [...]
  env:
    - name: GIT_REPO_URL
      value: "https://github.com/savitharaghunathan/coolstore.git"
    - name: GIT_TOKEN
      valueFrom: { secretKeyRef: { name: git-credentials, key: token } }
    - name: GIT_TARGET_BRANCH
      value: "konveyor/coolstore-migration"
```

## Turn Limits

With autonomous goose, a runaway stage could burn through API credits.
`KONVEYOR_PARAM_MAX_TURNS` (default 200) should cap how long goose runs
per stage. **Needs investigation during implementation:** how does goose
serve accept a turn limit? Options include a CLI flag, env var, or ACP
session parameter. If none exist natively, the harness may need to count
`tool_call` notifications from the ACP stream and terminate the session
when the limit is reached.

`KONVEYOR_PARAM_MAX_FIX_ITERATIONS` (default 3) is consumed by the
verify skill to cap its fix loop. It's not a harness concern — the skill
reads it and enforces it.

## Scope

This design covers Java only. Other languages follow the same pattern: new execute + verify images with language-specific toolchain and skills, new playbook YAML. No harness or controller changes required.
