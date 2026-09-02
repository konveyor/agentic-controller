---
adr: "0007"
title: "Harness as Thin Single-Stage Runner with SkillCard-Based Skills"
description: "Defines the harness as a thin, single-stage runner responsible for git lifecycle, skill delivery, and agent runtime lifecycle."
status: accepted
date: "2026-07-21"
last_updated: "2026-09-02"
authors:
  - "Savitha Raghunathan"
last_reviewed: "2026-08-31"
implementation_status: amended
review_note: "Accepted and implemented as the minimal entry point contract. The 2026-08-27 amendment records that it does not create .gitignore or grounding-data commits."
---

# ADR 0007: Harness as Thin Single-Stage Runner with SkillCard-Based Skills

**Update (2026-08-27):** The harness was slimmed to a minimal entry point — it no longer injects `.gitignore` entries for build artifacts (§3, "Thin single-stage runner" step 3) or authors the grounding-data commit (Alternatives, "Harness commits instead of agent"). See `docs/entry-point.md` for the current contract.

**Update (2026-09-02):** The current plan skill does not run Graphify, and the
base image no longer installs `graphifyy`. The stage and image descriptions
below are retained as historical context where applicable; current planning
uses repository files and optional Hub analysis results.

## Context

The migration harness was a monolithic Go binary that orchestrated five
sequential stages (detect, plan, execute, verify, fix-loop) internally.
Each stage was a Go package that constructed multi-turn ACP prompts from
YAML recipe files and an embedded skill bundle. The harness owned both
stage sequencing and migration intelligence.

With the AgentWorkflowRun controller (ADR 0001) handling stage
sequencing, the harness no longer needs orchestration logic. Meanwhile,
the SkillCard CRD and OCI-based skill packaging provide a clean
mechanism for delivering migration knowledge to agent pods at runtime.

The harness needed to become a thin, uniform wrapper — identical in
every stage image — that handles only git plumbing and goose lifecycle.
Migration intelligence needed to move from Go code and YAML recipes into
standalone skills that can be versioned, shared, and composed
independently.

## Decision

### Thin single-stage runner

The harness binary exposes a single `run` command. It does not know what
stage it is running, what language is being migrated, or what migration
patterns to apply. Its responsibilities are:

1. Load configuration from environment variables (`config.LoadFromEnv`)
2. Resolve application metadata, git credentials, and analysis results
   from the Konveyor Hub API (`HUB_BASE_URL`, `HUB_TOKEN`, `APP_ID`)
3. Clone the git repo using Hub-provided credentials, configure git
   author for agent commits, strip push credentials from the remote,
   inject `.gitignore` entries for build artifacts, checkout the
   controller-provided target branch (`TARGET_BRANCH`)
4. Write analysis insights to `.konveyor/analysis.json` (if available)
5. Clear Hub credentials from the environment
6. Start `goose serve` with LLM provider credentials
7. Connect via ACP WebSocket, create a session
8. Discover skills from `/opt/skills/*/SKILL.md` (glob)
9. Build a single prompt from four context layers:
   - `KONVEYOR_PROMPT` — agent-level standing instructions
   - `KONVEYOR_WORKFLOW_GUIDE` — workflow guide context
   - Skill content (concatenated from all discovered skills)
   - `KONVEYOR_INSTRUCTIONS` — stage-specific task
10. Start a filesystem watcher for incremental push
11. Send one ACP prompt and block until completion
12. Stop watcher, determine exit status from ACP completion (error =
    failure, clean return = success)
13. Final push — push failure is fatal (exit 1)

No interactive commands, no file-based config, no multi-turn recipe
execution.

#### Credential isolation

Hub credentials (`HUB_BASE_URL`, `HUB_TOKEN`, `APP_ID`) are cleared
from the environment after resolution via `hub.ClearEnv()`. Git push
credentials are stripped from the cloned repo's remote. Both happen
before goose starts. This is a deliberate security boundary — goose
and any skill content it executes cannot access Hub or push
credentials. The agent commits locally (a credential-free operation);
only the harness binary pushes to the remote.

### Two kinds of skills: stage and domain

Skills are classified into two types with distinct responsibilities:

- **Stage skills** define *process* — what to do. They encode the
  workflow for a stage (plan, execute, verify) without
  language-specific knowledge. Stage skills reference domain skills
  with directives like "follow the migration phases from any loaded
  domain skill."

- **Domain skills** define *knowledge* — how to do it. They provide
  language-specific migration intelligence: annotation maps,
  dependency maps, pattern catalogs, phased migration guides.
  Example: `javaee-to-quarkus`.

An agent loads one stage skill and one or more domain skills. The
harness concatenates all discovered skills into the prompt. The stage
skill's process instructions frame the work; the domain skill's
knowledge fills in the specifics.

Three stage skills encode what was previously spread across Go packages:

| Skill | Replaces | Purpose |
|-------|----------|---------|
| `plan` | `detect/`, `plan/`, `recipes/plan.yaml` | Analyze the project and produce PLAN.md |
| `execute` | `execute/`, `recipes/execute.yaml` | Read PLAN.md, apply transformations file by file |
| `verify` | `verify/`, `fixloop/`, `recipes/verify.yaml` | Build, fix errors iteratively, report result |

Skills are packaged as scratch-based OCI images (`FROM scratch; COPY . /`)
and referenced by SkillCard CRs. The controller mounts them as init
container volumes at `/opt/skills/<name>/` in the agent pod.

### Skill discovery at runtime (not baked into images)

The original design specified baking exactly one skill into each stage
image via `COPY skills/plan/ /opt/skills/plan/`. We chose instead to
keep stage images skill-free and mount skills at runtime via SkillCards.

This means:
- Stage images contain only the runtime and language toolchain (JDK, Maven,
  or another language-specific toolchain)
- Skills are versioned and released independently of images
- The same stage image works with different skill combinations
- The harness discovers all mounted skills via glob, not exactly one

### Image hierarchy

One base image, one image per language. All stages for a language
share the same image:

```
agent-base (UBI + goose + git + harness binary)
├── agent-java (+ JDK 21, Maven)
├── agent-csharp (+ .NET SDK)
├── agent-go (+ Go toolchain)
├── agent-nodejs (+ Node.js)
```

Adding a new language means a new `agent-<lang>` image. No harness
or controller changes required.

### Filesystem watcher

A background goroutine watches the working directory using fsnotify and
pushes after a 30-second quiet period (no new file writes). The agent
makes commits with descriptive messages as part of its skill
instructions; the watcher pushes those commits to the remote using
credentials the agent cannot access.

The watcher excludes irrelevant directories (`.goose/`, `__pycache__/`,
`node_modules/`) to avoid triggering on noise.

A final push after goose exits catches anything the watcher missed.

The 30-second quiet period is a reasonable default for all stages.

### Cross-stage state via git

Git is the shared state boundary. Each stage clones the repo and reads
artifacts left by prior stages:

- Plan writes and commits: `PLAN.md`, `graph.json`
- Execute reads: `PLAN.md`. Writes and commits: migrated source files
- Verify reads: migrated source. Writes and commits: fix patches

#### Exit status

The harness determines success or failure from ACP/goose signals:
`SendPrompt` returning without error and goose still alive = success
(exit 0). Any error, goose crash, or final push failure = failure
(exit 1). Push failure is fatal because the next stage clones the
branch — if the push didn't land, the next stage has no artifacts to
work with. The harness does not read any skill-written status file —
git commits are the primary cross-stage context. Quality assessment
is the eval stage's responsibility, not the harness's.

### Context window scaling

A single prompt per stage means the LLM's context window is the primary
scaling constraint. On large projects (hundreds of files, 50+ plan
steps), the combined prompt + skill content + tool call history can
exceed the context window mid-stage.

The mitigation is skill-level chunking: the execute skill processes N
steps at a time, committing between chunks, so each chunk fits in
context. This keeps the complexity in the skill (where domain knowledge
lives) rather than in the harness. The harness remains a single-prompt
sender regardless of project size.

## Deleted code

| Deleted | Reason |
|---------|--------|
| `internal/detect/`, `internal/plan/`, `internal/execute/`, `internal/verify/`, `internal/fixloop/` | Logic moved to skills |
| `internal/handoff/`, `internal/metrics/`, `internal/rundir/` | Controller handles lifecycle |
| `internal/goose/recipe.go`, `internal/goose/acp_runner.go` | Multi-turn recipe execution replaced by single prompt |
| `harness/recipes/` | Content folded into skills |
| `harness/skill-bundle/` | Replaced by OCI-packaged SkillCards |
| `images/agent-base-goose/`, `images/agent-java-goose/` | Superseded by new image hierarchy |

## Alternatives Considered

### Keep skills baked into images

Each stage image copies its skill at build time
(`COPY skills/plan/ /opt/skills/plan/`). Simpler build, no SkillCard
dependency.

Rejected because: couples skill content to image releases. Updating a
skill prompt requires rebuilding and redeploying the image. SkillCard
mounting allows skill iteration without image changes and enables
composing multiple skills per stage.

### Keep multi-turn recipe execution

The harness constructs multiple ACP prompts per stage using recipe YAML
files, sending them sequentially with context management.

Rejected because: the recipe system duplicated orchestration that the
LLM handles naturally. A single well-composed prompt with skill
instructions produces equivalent or better results, and the LLM
manages its own context within the session. The multi-turn approach
also required complex Go code for recipe parsing, context windowing,
and turn management.

### Harness as a library, not a binary

Ship the harness as a Go library that custom agent images import and
call. Each image would have its own `main.go`.

Rejected because: the harness behavior is identical across all stages.
A single binary with env-var configuration is simpler to maintain,
test, and debug. Custom behavior belongs in skills, not in the
harness binary.

### Harness-level session splitting for large projects

The harness sends multiple prompts per stage (e.g., "execute steps
1-10", then "execute steps 11-20"), re-reading PLAN.md each time.

Rejected because: adds orchestration complexity back into the harness.
Skill-level chunking (the skill itself decides how to batch work)
keeps the harness thin and puts the complexity where domain knowledge
lives.

### Harness commits instead of agent

The harness commits on behalf of the agent using go-git with a fixed
author signature, triggered by a filesystem watcher after a quiet
period.

Rejected because: produces generic commit messages
(`"konveyor: auto-commit progress"`) that obscure what changed. The
agent has the context to write meaningful commit messages per step.
Commit is a local operation that requires no credentials, so the
security boundary (push credentials stay in the harness) is preserved.
The harness injects `user.name` and `user.email` via
`git.ConfigureAuthor` and `.gitignore` entries for build artifacts so
the agent can commit safely.

### Controller reads status files from git

The controller clones the git branch or reads status files from the
pod filesystem to get structured stage results.

Rejected because: adds git or filesystem dependencies to the
controller. The harness exit code (derived from ACP completion
status) is sufficient. The controller stays a standard stateless
reconciler that watches pod exit codes.

## Consequences

- **Skill quality is critical.** Migration intelligence now lives
  entirely in markdown skill files, not in deterministic Go code.
  The LLM interprets skill instructions, which means skill authoring
  quality directly impacts migration quality. Poor skill instructions
  produce poor migrations.

- **Single prompt per stage.** The harness sends one prompt. For large
  projects, skills must implement their own chunking strategy to stay
  within the context window. The harness has no automatic recovery if
  the LLM loses context mid-stage.

- **Exit status from ACP, not files.** The harness determines
  success/failure from the ACP completion signal, not from any
  skill-written file. Git commits are the cross-stage context.
  Quality assessment is deferred to the eval stage.

- **SkillCard dependency.** Stage images are non-functional without
  mounted SkillCards. A misconfigured Agent CR (missing skillCards
  ref) produces a pod that starts, finds no skills, and exits
  immediately with an error.

- **Image builds require dependency ordering.** CI must build
  `agent-base` before any language image (`agent-java`, `agent-go`,
  etc.). The Makefile encodes these dependencies.

- **Multi-language via image naming.** Image names include the
  language suffix (`agent-java`, `agent-go`). The base image is
  shared across all languages.

- **Test harness scaffolding.** `hack/harness-test/setup.sh` builds
  skill OCI images locally, loads them into Kind, and applies
  SkillCard + Agent + AgentWorkflow + AgentWorkflowRun CRs for
  end-to-end testing.
