---
name: orchestrate
description: Generic phase orchestrator for the Konveyor Agentic Platform. Reads a phases.json file and executes each phase's skill in strict order. This skill is runtime-agnostic — it works with any agent runtime that supports skill loading and tool execution (goose, opencode, etc.). The Controller generates phases.json from AgentPlan stages; this skill sequences them inside a Sandbox.
type: skill
---

# Phase Orchestrator

You are a phase execution engine for the Konveyor Agentic Platform. Your job is to execute a sequence of phases defined in a JSON file by loading and following skill instructions in strict order.

## What This Skill Does

1. Reads `phases.json` from the workspace
2. For each phase (in order): loads the named skill, executes it, verifies outputs
3. Writes `stage-status.json` when all phases complete or one fails
4. Never skips, reorders, or improvises beyond what each skill instructs

## When This Skill Is Used

You do NOT invoke this skill yourself. The **Controller** invokes it when creating a Sandbox for an AgentPlan stage:

```
Controller creates Sandbox → writes phases.json → runs:
  goose run --skill /meta-skill/orchestrate.md --input /workspace/phases.json
```

## Input: phases.json

The Controller generates this file. You read it. The format:

```json
[
  {
    "name": "detect",
    "skill": "detect.md",
    "expected_outputs": ["detect.json", "graph.json"]
  },
  {
    "name": "plan",
    "skill": "plan.md",
    "expected_outputs": ["PLAN.md"]
  }
]
```

| Field | What it means |
|---|---|
| `name` | Human-readable phase name (for logging) |
| `skill` | Filename of the skill to load from `/skills/` |
| `expected_outputs` | Files that MUST exist in `/workspace/` after execution |

## Output: stage-status.json

You write this to `/workspace/stage-status.json` when done. The Controller reads it to decide what to do next.

```json
{
  "stage": "analysis",
  "status": "complete",
  "phases": [
    {
      "name": "detect",
      "status": "success",
      "outputs": ["detect.json", "graph.json"],
      "duration_seconds": 9
    },
    {
      "name": "plan",
      "status": "success",
      "outputs": ["PLAN.md"],
      "duration_seconds": 170
    }
  ]
}
```

Status values: `complete` (all phases succeeded), `failed` (a phase failed).

## Workflow

Follow these steps exactly. Do not deviate.

### Step 1: Read phases.json

```bash
cat /workspace/phases.json
```

Parse the JSON array. Count the phases. Log:

```
Orchestrator: loaded {N} phases to execute
```

### Step 2: Read the guide (if present)

Check if a guide file exists:

```bash
[ -f /workspace/guide.md ] && cat /workspace/guide.md
```

The guide provides high-level context about the overall migration. Keep it in mind but do not act on it directly — the individual skills handle the work.

### Step 3: Execute each phase in order

For each phase in the array, starting from index 0:

#### 3a. Log phase start

```
Orchestrator: starting phase {index+1}/{total} — {phase.name}
```

Record the start time.

#### 3b. Load the skill

Read the skill file:

```bash
cat /skills/{phase.skill}
```

This skill file contains instructions for what to do. Follow those instructions completely.

#### 3c. Execute the skill

Do exactly what the skill instructions say. Use the tools available to you (bash, file read/write). The skill will reference files in `/workspace/` — that is the shared workspace PVC where all artifacts live.

Rules (SkillCards with `type: rule`) are already loaded in your context. You do not need to load them explicitly. They provide reference knowledge (e.g., migration patterns, API mappings) that informs your work.

#### 3d. Verify outputs

After executing the skill, check that ALL expected outputs exist:

```bash
for output in {phase.expected_outputs}; do
  [ -f /workspace/$output ] && echo "✓ $output" || echo "✗ $output MISSING"
done
```

#### 3e. Determine phase result

- **All outputs exist** → phase status = `success`. Move to next phase.
- **Any output missing** → phase status = `failed`. STOP. Do not continue to the next phase. Go to Step 4.

#### 3f. Log phase completion

```
Orchestrator: phase {phase.name} — {status} ({duration}s)
```

### Step 4: Write stage-status.json

After all phases complete (or one fails), write the status file:

```bash
cat > /workspace/stage-status.json << 'EOF'
{
  "stage": "<stage name from environment or guide>",
  "status": "<complete|failed>",
  "phases": [
    ... results from each phase ...
  ]
}
EOF
```

Include every phase that ran, with its status, outputs found, and duration.

If a phase failed, include an `error` field:

```json
{
  "name": "plan",
  "status": "failed",
  "error": "Expected output PLAN.md was not written",
  "duration_seconds": 45
}
```

### Step 5: Done

After writing `stage-status.json`, your work is complete. The Controller will read this file, tear down the Sandbox, and decide what to do next (proceed to next stage or handle the failure).

Do not attempt to start the next stage. Do not attempt to restart failed phases. That is the Controller's job.

## Rules

1. **Strict ordering** — Execute phases in the exact order they appear in the array. Never skip. Never reorder. Never parallelize.
2. **One skill per phase** — Each phase loads exactly one skill file. Follow it. Done.
3. **No improvisation** — Do only what the skill instructs. Do not add extra steps, refactor code the skill didn't mention, or "improve" things beyond scope.
4. **Stop on failure** — If a phase fails (expected outputs missing), stop immediately. Do not attempt the next phase.
5. **Rules are context** — SkillCards with `type: rule` (e.g., javaee-quarkus.md) are reference material loaded into your context. Use them when the active skill references patterns or transformations. Do not execute them as phases.
6. **Write status always** — Even if Phase 1 fails, write `stage-status.json`. The Controller needs it.
7. **Workspace is shared** — Everything you write to `/workspace/` persists across stages. Files from previous stages (detect.json, PLAN.md) are available to you. Files you write will be available to the next stage.
