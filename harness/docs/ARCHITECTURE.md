# migration-harness — Architecture & Implementation

Deep dive into how the migration pipeline works, step-by-step implementation details, session management, and design decisions.

---

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Step 1 — Detect](#step-1--detect-graphify-ast-analysis)
3. [Step 2 — Plan](#step-2--plan-dynamic-recipe-reference-tracking)
4. [Step 3 — Execute](#step-3--execute-context-aware-execution)
5. [Step 4 — Verify](#step-4--verify-auto-fix--interactive-resumption)
6. [Step 5 — Fix-loop](#step-5--fix-loop-conditional-iteration)
7. [Session Management](#session-management)
8. [Artifacts & State](#artifacts--state)
9. [Design Decisions](#design-decisions)

---

## Architecture Overview

```
migration-harness  (bash CLI)               ←  orchestration, gates, resume
    │
    ├── lib/                                ←  one bash file per step
    │     step-detect.sh                        graphify invocation
    │     step-plan.sh                          renders recipe dynamically
    │     step-execute.sh                       loops over plan items
    │     step-verify.sh                        interactive resumption
    │     step-fix-loop.sh                      bounded verify/fix iterations
    │     common.sh                             goose_run(), goose_run_interactive()
    │
    ├── recipes/                            ←  static goose recipe files
    │     execute.yaml                          one file per migration item
    │     verify.yaml                           build + test + auto-fix
    │     fix.yaml                              fix one compiler error
    │     (no plan.yaml — rendered dynamically)
    │
    └── skill-bundle/goose-migration/       ←  migration expertise
          SKILL.md                              umbrella skill
          skills/migration-plan/SKILL.md        planner skill (how to plan)
          skills/javaee-quarkus/SKILL.md        Java EE execution rules
          references/javaee-quarkus.md          transformation patterns
          references/migration-phases.md        general migration phases

State on disk:
  ~/.migration-harness/config               ←  model, provider, limits
  ~/.migration-harness/runs/<id>/           ←  per-run artifacts + logs
    ├── graph.json                          ←  code graph from graphify
    ├── GRAPH_REPORT.md                     ←  graph analysis summary
    ├── plan.json                           ←  structured migration plan
    ├── execution-log.md                    ←  lessons learned + errors
    ├── verification-report.md              ←  build status + auto-fix attempts
    └── fix-loop-report.md                  ←  fix iteration history
  <repo>/PLAN.md                            ←  human-readable migration plan
  <repo>/execution-log.md                   ←  execution progress
  <repo>/verification-report.md             ←  verification results
```

---

## Step 1 — Detect (graphify AST analysis)

**File:** `lib/step-detect.sh`  
**LLM tokens:** 0 (pure Python AST analysis)

### What it does

This step uses **graphify** (not goose) to build a complete code graph. Zero LLM tokens spent.

```
1a. Check manifest files      → pom.xml? package.json? pyproject.toml?
1b. Build code graph           → AST extraction, dependency edges, community detection
1c. Analyze file types         → extract counts from graph nodes
1d. Write detect.json          → structured metadata for planning
```

### Graphify vs Traditional Grep

| Traditional grep | Graphify |
|------------------|----------|
| Text pattern matching | AST-based parsing |
| No relationships | Full dependency graph |
| No architecture insight | Community detection (clusters) |
| Fragile (misses obfuscated code) | Robust (parses syntax) |

### Output artifacts

**`graph.json`** — Full code graph:
```json
{
  "nodes": [
    {
      "id": "OrderService.placeOrder",
      "type": "method",
      "source_file": "services/OrderService.java",
      "degree": 23
    }
  ],
  "links": [
    {"source": "OrderService.placeOrder", "target": "InventoryService.reserve"}
  ],
  "communities": [
    {"id": 1, "nodes": ["OrderService", "InventoryService", ...]}
  ]
}
```

**`GRAPH_REPORT.md`** — Human-readable summary:
- Architecture communities (related code clusters)
- God nodes (high-degree nodes needing special attention)
- Key dependencies

**`detect.json`** — Metadata summary:
```json
{
  "repo": "/path/to/project",
  "manifests": { "pom_xml": true, "package_json": false, ... },
  "files": { "java": 30, "python": 1, "javascript": 1001, ... },
  "graph": { "nodes": 245, "edges": 687, "communities": 12, "god_nodes": 3 },
  "graph_file": "graph.json"
}
```

### Why graphify?

- **Zero LLM tokens**: All analysis is Python AST parsing
- **Structured understanding**: Graph gives planner architectural insights
- **Community detection**: Identifies related code clusters for phased migration
- **Dependency awareness**: Knows which files depend on which (execution ordering)
- **God node detection**: Highlights high-degree nodes needing special attention

### User output

```
── Step 1/5 — Detect ──
ℹ Step 1/5: detecting project structure
ℹ   1a. checking manifest files...
✓   1a. manifests: pom=true pkg=false
ℹ   1b. building code graph (AST extraction, edges, communities)...
ℹ        this may take 10-30s for large codebases (parallelized)...
       Analyzing repository...
       Found 245 nodes, 687 edges
       Detected 12 communities
✓   1b. graph: 245 nodes, 687 edges, 12 communities (3 high-degree)
✓ Step 1/5 complete → detect.json + graph.json
```

---

## Step 2 — Plan (dynamic recipe, reference tracking)

**File:** `lib/step-plan.sh`  
**Recipe:** Generated dynamically (no static `recipes/plan.yaml`)

### The Dynamic Recipe Problem

Unlike other steps, plan has **no static recipe file**. Why?

1. The planner skill (`skills/migration-plan/SKILL.md`) is **large** (5-10KB)
2. Reference docs (`javaee-quarkus.md`) are also large (10-20KB each)
3. Pre-gathered context (detect.json, file tree) varies per project
4. Baking all this into the instructions saves 3-5 tool calls

So `step-plan.sh` **renders the recipe at runtime** using `_render_plan_recipe()`.

### What gets baked into the recipe

```
┌─────────────────────────────────────────────────────┐
│  instructions:                                       │
│    ┌─ PLANNER SKILL ──────────────────────────────┐ │
│    │  skills/migration-plan/SKILL.md (full text)   │ │  ← hardcoded: always loaded
│    └──────────────────────────────────────────────┘ │
│    ┌─ PRE-GATHERED CONTEXT ───────────────────────┐ │
│    │  detect.json (from step 1)                    │ │  ← pre-fed: already collected
│    │  graph.json summary (stats only)              │ │  ← pre-fed: node/edge counts
│    │  file tree (source + config filenames only)   │ │  ← pre-fed: just a listing
│    │  available references (filenames only):       │ │  ← listed, NOT content
│    │    - javaee-quarkus.md                        │ │
│    │    - migration-phases.md                      │ │
│    └──────────────────────────────────────────────┘ │
│    YOUR JOB:                                         │
│    1. Read the pre-gathered context                  │
│    2. Pick and read relevant reference file(s)       │  ← goose decides
│    3. Read graph.json for architecture insights      │  ← goose decides
│    4. Read complex source files (MDBs, JNDI, etc.)  │  ← goose decides
│    5. Read relevant config files                     │  ← goose decides
│    6. Write PLAN.md                                  │
│                                                      │
│  extensions: [developer]                             │  ← goose has shell/cat/write
│  response: { json_schema: {reference_used, ...} }    │  ← proof of reference usage
└─────────────────────────────────────────────────────┘
```

### Hardcoded vs LLM-decided

| Hardcoded (pre-fed) | Goose decides at runtime |
|----------------------|--------------------------|
| Planner skill (always loaded) | Which reference to read (`javaee-quarkus.md`? `none`?) |
| detect.json (file counts, graph stats) | Which graph details to examine |
| File tree (names only) | Which source files to read (MDBs, JNDI, etc.) |
| Reference filenames (not content) | Which config files to read (`pom.xml`? `web.xml`?) |

### Reference tracking (2e2)

To prove which reference the LLM uses, the response schema requires:

```yaml
response:
  json_schema:
    properties:
      reference_used: { type: string, description: "Name of reference file read, or 'none'" }
```

After goose returns, `step-plan.sh` extracts this:

```bash
ref_used=$(jq -r '.reference_used // "unknown"' "$out")
if [[ "$ref_used" != "unknown" && "$ref_used" != "none" ]]; then
  ok "  2e2. reference used: ${ref_used}"
fi
```

**User sees:**
```
✓   2e2. reference used: javaee-quarkus.md
       path: ~/.config/goose/skills/goose-migration/references/javaee-quarkus.md
```

### Sub-steps in detail

```
2a. Pre-gather context
    Bash collects detect.json + file tree + reference filenames.
    Baked into the rendered recipe so goose has it on turn 1.

2b. Load planner skill
    Reads skills/migration-plan/SKILL.md and indents it into recipe instructions.

2c. Render recipe
    _render_plan_recipe() generates plan-recipe.yaml in $RUN_DIR.

2d. Run goose (the LLM step)
    goose_run() invokes goose with the dynamic recipe.
    Typical tool calls (5-10 turns):
      - cat javaee-quarkus.md         (picks the reference)
      - cat pom.xml                   (reads build manifest)
      - cat graph.json                (examines architecture)
      - cat persistence.xml           (reads config)
      - cat OrderServiceMDB.java      (complex source file)
      - write PLAN.md                 (the output)
      - recipe__final_output          (JSON confirmation)

2e. Validate PLAN.md
    Checks that PLAN.md exists and counts steps.

2e2. Extract reference tracking
    Proves which reference file was used.

2f. Parse PLAN.md → plan.json
    _plan_md_to_json() converts human-readable PLAN.md to structured JSON.
    Handles multiple markdown formats.

2g. Human approval gate
    Shows PLAN.md, prompts: "Approve and execute? [y/edit/N]"
    User can edit in $EDITOR, script re-parses after save.

2h. Write .goosehints
    Generates <repo>/.goosehints with migration discipline rules.
```

### Turn limits

- **Initial turns:** 15
- **Session type:** Ephemeral (`--no-session`)
- **Why 15 is enough:** Pre-gathered context saves 3-5 turns. Goose typically finishes in 5-8.

---

## Step 3 — Execute (context-aware execution)

**File:** `lib/step-execute.sh`  
**Recipe:** `recipes/execute.yaml`

### What changed (context awareness)

Previously, execute ran blind — each item got just its file path and action.

Now:
1. **Reads PLAN.md for context**: Gets Goal and Project Summary
2. **Creates execution-log.md**: Tracks lessons learned and errors

### Why this matters

**Before:**
```
Item 23: Modify OrderService.java
LLM sees: "Modify OrderService.java"
LLM thinks: "What am I supposed to do with this?"
```

**After:**
```
Item 23: Modify OrderService.java
LLM sees:
  - Goal: "Migrate Java EE 7 to Quarkus 3"
  - Summary: "EJB-based microservices app, uses JPA + JMS"
  - Action: "Modify OrderService.java"
LLM thinks: "Ah, replace @Stateless with @ApplicationScoped, update imports"
```

### Recipe enhancements

**New parameter:**
```yaml
- key: plan_md_path
  input_type: string
  requirement: required
  description: "Path to PLAN.md file with goal and steps"
```

**Instructions emphasize:**
- Read PLAN.md Goal section to understand migration intent
- Execute ONE step only (no over-engineering)
- Document lessons learned (what worked, what to watch out for)
- Document errors (syntax errors, compilation issues encountered)

**Response schema:**
```yaml
response:
  json_schema:
    required: [n, path, status, files_touched, lesson, error_log]
    properties:
      lesson: { type: string, description: "What was learned (can be empty)" }
      error_log: { type: string, description: "Syntax/compile errors (empty if none)" }
```

### execution-log.md format

```markdown
# Execution Log

**Migration:** javaee-quarkus
**Started:** 2026-05-17 10:23:15

---

## Step #1: MODIFY - src/main/java/services/OrderService.java

**Status:** ok
**Files touched:** OrderService.java, pom.xml

**Lesson learned:**
Changed `javax.ejb.Stateless` to `jakarta.enterprise.context.ApplicationScoped`.
Also needed to add `quarkus-arc` dependency to pom.xml for CDI support.

---

## Step #2: MODIFY - src/main/resources/META-INF/persistence.xml

**Status:** ok
**Files touched:** persistence.xml

**Lesson learned:**
Quarkus auto-configures datasources. Removed explicit JNDI lookups.

**Errors:**
Initial attempt had syntax error in application.properties (missing =).
Fixed by correcting property format.

---
```

### Why execution-log.md matters

When **verify** finds compilation errors, it reads `execution-log.md` to understand:
- What changes were attempted
- What patterns were already tried
- What known issues execution ran into

This prevents verify from blindly re-attempting the same failed approaches.

### Turn limits

- **Per-item turns:** 10
- **Session type:** Ephemeral (`--no-session`)
- **Why 10 is enough:** Each item is focused (one file or one config change). If it can't finish in 10 turns, the step is too complex (should be split in planning).

---

## Step 4 — Verify (auto-fix + interactive resumption)

**File:** `lib/step-verify.sh`  
**Recipe:** `recipes/verify.yaml`

### The Turn Limit Problem

Complex migrations can have **28+ compilation errors**. Auto-fixing them requires:

1. Run build (1-2 turns)
2. Read 10 error files (10 turns)
3. Fix 10 files (10 turns)
4. Re-compile (1 turn)
5. Repeat 3 times (×3)

**Total: 60-90 turns** for a single session.

If goose hits the 50-turn limit, it stops mid-work. The `goose_run()` function extracts output by looking for `recipe__final_output` — if goose never reaches that tool call, you get **zero output** even though it made progress.

### The Solution: Interactive Resumption

**New function:** `goose_run_interactive()` in `lib/common.sh`

How it works:

```
Session 1 (initial 50 turns):
  - Run verification commands
  - Identify 28 errors
  - Fix 15 errors
  - Turn 50 reached → stops

Check for recipe__final_output:
  - Not found → incomplete

Prompt user:
  ⚠ "session incomplete (used 50 turns so far)"
  "Continue with more turns? [Y/n]": y
  "How many more turns to add? [default: 30, max: 90]": 40

Session 2 (resume with +40 turns = 90 total):
  - Continues from turn 51 (preserves ALL context)
  - Fixes remaining 13 errors
  - Turn 90 → completes
  - Returns recipe__final_output → success

Cleanup:
  goose session remove --name <session_name>
```

### Three-Phase Auto-Fix

**Phase 1 — Read verification steps:**
1. Read `PLAN.md` → locate "## Verification" section
2. Extract build commands (e.g., `mvn compile`, `dotnet build`)

**Phase 2 — Run verification:**
3. Execute the verification commands
4. Capture build status, test results, compilation errors

**Phase 3 — Auto-fix (0-3 iterations):**
5. If compilation **fails**:
   - Read `execution-log.md` to understand what execution attempted
   - Identify root cause of failures
   - Make targeted fixes
   - Re-run verification
   - Repeat up to **3 times**
6. If still failing after 3 attempts → document remaining errors

### Recipe enhancements

**New parameters:**
```yaml
- key: plan_md_path
  input_type: string
  requirement: required
  description: "Path to PLAN.md with verification section"

- key: execution_log_path
  input_type: string
  requirement: required
  description: "Path to execution-log.md with execution details"
```

**Instructions:**
- Read ONLY the Verification section from PLAN.md (ignore Goal, Steps)
- Run verification commands
- If build fails, read execution-log.md for context
- Attempt fixes (max 3 iterations)
- Track fix attempts in response

**Response schema:**
```yaml
response:
  json_schema:
    required: [build_ok, fix_attempts, fixes_applied]
    properties:
      fix_attempts: { type: integer, description: "0-3" }
      fixes_applied: { type: string, description: "What was fixed" }
```

### verification-report.md format

```markdown
# Verification Report

**Migration:** javaee-quarkus
**Timestamp:** 2026-05-17 10:45:32

## Build Status

- ✅ Compilation: **SUCCESS**
- Tests: 45/50 passed

## Auto-Fix Attempts

- Fix iterations: 2
- Fixes applied: Fixed missing jakarta.inject imports, updated EJB annotations

## Summary

Build successful after 2 auto-fix iterations. 5 tests still failing (require manual review).
```

### Turn limits

- **Initial turns:** 50
- **Max total:** 140 (user can add 30-50 at a time)
- **Session type:** Persistent (with auto-cleanup)
- **Why resumable:** Complex migrations can easily need 80-100 turns

### User experience

```
ℹ   running goose session 'verify-1234567890' (turns: 50 / 140)...
[... progress indicators ...]
⚠   session incomplete (used 50 turns so far)
Continue with more turns? [Y/n]: y

How many more turns to add? [default: 30, max: 90]: 40

ℹ   running goose session 'verify-1234567890' (turns: 90 / 140)...
[... continues from turn 51 ...]
✓   session completed successfully
✓   build: OK | tests: 45/50 passed | errors: 0
```

---

## Step 5 — Fix-loop (conditional iteration)

**File:** `lib/step-fix-loop.sh`  
**Recipe:** `recipes/fix.yaml`

### Conditional Execution

**Key change:** Fix-loop only runs if Step 4 verification **failed**.

```bash
# Read verify.json from Step 4
build_ok=$(jq -r '.build_ok // false' "$RUN_DIR/verify.json")

if [[ "$build_ok" == "true" ]]; then
  ok "  build status: SUCCESS (from verification report)"
  ok "  fix loop not needed — skipping"
  return 0
fi

warn "  build status: FAILED (from verification report)"
info "  starting fix loop (max 3 iterations)..."
```

### Why conditional?

If Step 4 auto-fix succeeded (build clean), there's no point running fix-loop. This saves time and tokens.

### What it does (if build failed)

Bounded loop (max 3 iterations):

```
Iteration 1:
  - Re-run verification → verify-fix-1.json
  - If clean → success, write report, done
  - If errors → pick first error
  - Invoke goose with fix.yaml (max 8 turns)
  - Fix one error
  
Iteration 2:
  - Re-run verification → verify-fix-2.json
  - ...

Iteration 3:
  - Re-run verification → verify-fix-3.json
  - ...

If still failing after 3 iterations:
  - Write report: "Manual intervention needed"
  - Return failure
```

### Recipe enhancements

**New parameter:**
```yaml
- key: verification_report_path
  input_type: string
  requirement: required
  description: "Path to verification-report.md with build errors"
```

**Instructions:**
- Read `verification-report.md` to see:
  - Full list of compilation errors
  - Context from verification
  - What Step 4 auto-fix already attempted
- Fix **exactly ONE** error (minimal changes)
- Only touch the error file (or build manifest if missing dependency)
- Do NOT re-run compile/tests (fix-loop will re-verify)

### fix-loop-report.md format

**Success:**
```markdown
# Fix Loop Report

**Migration:** javaee-quarkus
**Status:** ✅ **SUCCESS**
**Iterations:** 2

## Fixes Applied

### Iteration 1
- **File:** OrderService.java
- **Summary:** Fixed missing @Inject annotation

### Iteration 2
- **File:** pom.xml
- **Summary:** Added missing quarkus-hibernate-orm dependency

## Result

All compilation errors resolved. Build is now successful.
```

**Max iterations:**
```markdown
# Fix Loop Report

**Iterations:** 3
**Status:** Manual intervention needed (max iterations reached)

## Attempted Fixes

[... list of 3 attempts ...]

## Next Steps

Manual intervention is required. Review remaining errors in verification-report.md
```

### Why fix-loop after verify auto-fix?

**Division of labor:**
- **Verify auto-fix** (Step 4): Fixes obvious patterns in one smart session (has full context). Fast. Catches 60-80% of errors.
- **Fix-loop** (Step 5): Handles remaining tricky errors one at a time. Slower but focused. Needed only if verify couldn't finish.

### Turn limits

- **Per-fix turns:** 8
- **Max iterations:** 3 (configurable via `MH_MAX_FIX_ITERATIONS`)
- **Session type:** Ephemeral (`--no-session`)
- **Why 8 is enough:** Fixing one error should be quick: read error context (1 turn), read file (1 turn), fix (1-3 turns), return (1 turn).

---

## Session Management

### Ephemeral vs Persistent

| Step | Function | Session Type | Resumable? | Auto-cleanup? |
|------|----------|--------------|------------|---------------|
| Detect | (graphify) | N/A | N/A | N/A |
| Plan | `goose_run` | Ephemeral (`--no-session`) | No | Automatic (goose) |
| Execute | `goose_run` | Ephemeral (`--no-session`) | No | Automatic (goose) |
| Verify | `goose_run_interactive` | Persistent | **Yes** | Manual (scripted) |
| Fix-loop | `goose_run` | Ephemeral (`--no-session`) | No | Automatic (goose) |

### Why only verify uses persistent sessions?

**Turn count reality:**
- **Plan**: 15 turns max (pre-gathered context makes it fast)
- **Execute per-item**: 10 turns (focused, one change at a time)
- **Fix per-error**: 8 turns (one error, minimal fix)
- **Verify**: 50-140 turns (complex auto-fix can need many iterations)

Only verify risks hitting turn limits, so only verify gets resumption.

### Session cleanup

**Verify cleanup strategy:**

Every exit path in `goose_run_interactive()` calls:
```bash
goose session remove --name "$session_name" >/dev/null 2>&1 || true
```

Paths:
- Success → cleanup session → return 0
- Max turns hit → cleanup session → return 1
- User aborts → cleanup session → return 1
- API error → cleanup session → return 1

**Net result:** Zero persistent sessions accumulate, even though verify uses one temporarily.

### How resumption works

**Initial call:**
```bash
goose run \
  --recipe verify.yaml \
  --name "verify-1234567890" \    # Creates named session
  --max-turns 50 \
  --output-format json \
  ...
```

**Resume call:**
```bash
goose run \
  --name "verify-1234567890" \    # Same session name
  --resume \                       # Resume flag
  --max-turns 40 \                 # Additional turns (not total)
  --output-format json \
  ...
```

The resume call:
- Continues from where the first session stopped
- Preserves **all conversational context** (LLM remembers everything)
- Adds 40 more turns to the budget
- Appends to the same log file

---

## Artifacts & State

### Per-run directory structure

```
~/.migration-harness/runs/<timestamp>/
├── detect.json                 # Step 1 output
├── graph.json                  # Code graph from graphify
├── GRAPH_REPORT.md             # Graph analysis summary
├── plan-recipe.yaml            # Step 2 dynamically rendered recipe
├── plan.json                   # Structured plan (parsed from PLAN.md)
├── execution-log.md            # Step 3 lessons + errors
├── item-1.json                 # Per-item execution results
├── item-2.json
├── ...
├── verify.json                 # Step 4 verification results
├── verification-report.md      # Build status, auto-fix attempts
├── verify-fix-1.json           # Step 5 re-verification results
├── verify-fix-2.json
├── fix-1.json                  # Per-fix results
├── fix-2.json
├── fix-loop-report.md          # Fix history and final status
└── logs/
    ├── plan-1234567890.json    # Raw goose session transcript
    ├── execute-1234567891.json
    ├── verify-1234567892.json
    └── ...
```

### Files copied to repo

```
<repo>/
├── PLAN.md                     # From Step 2
├── execution-log.md            # From Step 3
├── verification-report.md      # From Step 4
├── fix-loop-report.md          # From Step 5 (if applicable)
└── .goosehints                 # From Step 2 (migration discipline)
```

### Artifact ownership

| File | Created by | Format | Purpose |
|------|-----------|--------|---------|
| `detect.json` | Step 1 | JSON | Project metadata (manifests, file counts, graph stats) |
| `graph.json` | Step 1 (graphify) | JSON | Full code graph (nodes, edges, communities) |
| `GRAPH_REPORT.md` | Step 1 (graphify) | Markdown | Human-readable graph analysis |
| `plan-recipe.yaml` | Step 2 | YAML | Dynamically rendered goose recipe |
| `plan.json` | Step 2 | JSON | Structured migration plan |
| `PLAN.md` | Step 2 (LLM) | Markdown | Human-readable migration plan |
| `execution-log.md` | Step 3 | Markdown | Lessons learned + errors per execution step |
| `item-N.json` | Step 3 | JSON | Per-item execution results |
| `verification-report.md` | Step 4 | Markdown | Build status, auto-fix attempts, remaining errors |
| `verify.json` | Step 4 | JSON | Structured verification results |
| `verify-fix-N.json` | Step 5 | JSON | Re-verification results per iteration |
| `fix-N.json` | Step 5 | JSON | Per-fix results |
| `fix-loop-report.md` | Step 5 | Markdown | Fix iteration history and final status |
| `logs/*.json` | All steps | JSONL | Raw goose conversation transcripts |

---

## Design Decisions

### Why per-step invocations?

**Alternative:** One giant goose session for the entire migration.

**Problems with monolithic approach:**
1. **Unbounded token budget**: Step 1 context bleeds into step 5
2. **No crash resilience**: If goose dies on item 17 of 34, you lose everything
3. **Expensive detection**: LLM wastes turns on `find` and `grep`
4. **No inspection**: Can't examine intermediate state without parsing one huge log

**Benefits of per-step:**
- Bounded token budget per step
- Crash = only that step fails, rest preserved
- Zero-token detection (graphify)
- Inspectable state at each phase
- Can resume mid-pipeline

### Why dynamic recipe for plan?

**Alternative:** Static `recipes/plan.yaml` that tells goose to read the planner skill.

**Problem:** Goose would spend 2-3 turns reading:
1. `cat skills/migration-plan/SKILL.md` (turn 1)
2. `cat detect.json` (turn 2)
3. `ls -R <repo>` to get file tree (turn 3)

**Solution:** Bake skill + context into the recipe instructions. Goose has it on turn 1.

**Trade-off:** Recipe generation is ~50 lines of bash, but saves 2-3 turns and guarantees consistency.

### Why graphify instead of grep?

**Grep approach:**
```bash
javax_count=$(grep -rl "javax\." src/ | wc -l)
```

**Problems:**
- Text-based (misses obfuscated code)
- No relationships (doesn't know which files depend on which)
- No architecture insight (can't detect god nodes or communities)

**Graphify approach:**
- AST-based (parses syntax, robust)
- Full dependency graph (knows edges)
- Community detection (identifies related code clusters)
- God node detection (finds high-degree nodes)

**Cost:** 10-30s of Python processing vs instant grep.

**Benefit:** Gives the planner **structured architectural insight** instead of just counts.

### Why auto-fix in verify instead of separate step?

**Alternative:** Verify just reports errors, separate step 4b does fixes.

**Problem:** If verification and fixing are separate:
- Verify runs build → errors
- User sees errors
- User runs fix
- Fix runs build again (duplicate work)

**Current approach:**
- Verify runs build → errors
- Verify attempts fixes (up to 3 iterations)
- Verify runs build again → reports final state
- If still failing, fix-loop takes over

**Benefit:** Fewer build invocations (builds are slow). Auto-fix catches 60-80% of errors immediately.

### Why interactive resumption instead of just increasing max-turns to 200?

**Alternative:** Set verify max-turns to 200 and hope it finishes.

**Problems:**
1. **All-or-nothing**: If it hits 200, you get zero output (no partial progress)
2. **Wasted tokens**: Simple migrations pay for 200 turns even if they only need 20
3. **No user control**: Can't decide whether to continue based on progress

**Current approach:**
- Start with 50 (enough for most migrations)
- Ask user if more turns needed
- User sees progress so far ("15/28 errors fixed")
- User decides whether to continue
- Continues with same session (preserves context)

**Benefit:** User control + context preservation + no waste on simple migrations.

### Why fix-loop after verify auto-fix?

**Alternative:** No fix-loop, verify does all fixes.

**Problem:** Verify's 3-iteration auto-fix is **smart but bounded**. For migrations with 30+ errors, verify might:
- Fix 20 errors in iteration 1
- Fix 8 errors in iteration 2
- Fix 5 errors in iteration 3
- Still have 5 errors remaining

Fix-loop gives those last 5 errors another chance (3 more iterations, 1 error at a time).

**Alternative approach:** Make verify unlimited iterations.

**Problem:** No bound on turns. A migration with 100 errors could burn 500 turns.

**Current approach:** Verify does **smart batch fixes** (multiple errors per iteration, uses context). Fix-loop does **focused single fixes** (one error, minimal scope).

**Benefit:** Verify handles the bulk efficiently. Fix-loop handles the stragglers carefully.

---

## Future Enhancements

### Parallel execution (Step 3)

Currently, execute processes items sequentially. For independent items (e.g., updating 10 different service classes), we could parallelize:

```bash
# Current
for item in plan.json items:
  goose_run execute.yaml item

# Future
for item in plan.json items (if independent):
  goose_run execute.yaml item &
wait
```

**Benefit:** 3-5x faster execution for large migrations.

**Challenge:** Dependency tracking (which items can run in parallel?).

### Checkpoint/resume within execute (Step 3)

If a single execute item is too complex (needs >10 turns), allow resumption like verify does.

**Benefit:** Can handle complex multi-file refactorings.

**Drawback:** Adds complexity. Better to split complex items in planning phase.

### Smart error grouping (Step 5)

Fix-loop currently fixes errors one-by-one. Could batch similar errors:

```bash
# Current
fix error 1 (missing import)
verify
fix error 2 (missing import)
verify

# Future
fix all "missing import" errors in one session
verify
```

**Benefit:** Fewer re-compilations, faster fix-loop.

**Challenge:** Error classification (which errors are "similar"?).

### Graph-guided execution ordering (Step 3)

Use `graph.json` to execute items in dependency order:

```bash
# Execute leaf nodes first (no dependents)
# Then their parents
# Then god nodes last
```

**Benefit:** Reduces cascading errors (if A depends on B, fix B first).

**Challenge:** Plan already attempts layer ordering, but doesn't use the graph directly.

---

## Appendix: goose_run() vs goose_run_interactive()

### goose_run() — Ephemeral sessions

**Use:** Plan, Execute, Fix-loop

```bash
goose_run() {
  goose run \
    --recipe "$recipe" \
    --no-session \              # Ephemeral, auto-cleanup
    --max-turns "$max_turns" \
    --output-format json \
    ...
  > "$log" 2>/dev/null &
  
  # Wait, extract recipe__final_output, done
}
```

**Characteristics:**
- No session persistence
- Fixed turn budget
- Returns immediately on completion or failure
- Zero cleanup needed (goose auto-deletes)

### goose_run_interactive() — Resumable sessions

**Use:** Verify only

```bash
goose_run_interactive() {
  # Initial call
  goose run \
    --recipe "$recipe" \
    --name "$session_name" \    # Persistent session
    --max-turns 50 \
    --output-format json \
    ...
  
  # Check for recipe__final_output
  if not found:
    ask user: "Continue with more turns?"
    if yes:
      # Resume call
      goose run \
        --name "$session_name" \
        --resume \
        --max-turns $additional \
        ...
  
  # Cleanup
  goose session remove --name "$session_name"
}
```

**Characteristics:**
- Creates named persistent session
- Variable turn budget (50 → 80 → 110 → 140)
- Prompts user for continuation
- Explicit cleanup required
- Preserves full conversational context across resume calls

---

**End of Architecture Documentation**

For user-facing quickstart, see [../README.md](../README.md).
