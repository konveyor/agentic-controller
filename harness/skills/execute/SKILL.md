---
name: execute
tags: [stage]
description: >
  Reads the implementation plan and executes migration steps phase by phase.
  Runs the build gate after each phase, fixes compiler errors before proceeding.
inputs:
  - .konveyor/implementation.md
  - .konveyor/questionnaire.json
  - domain skills
outputs:
  - modified source files
  - .konveyor/execute.json
---

# Execute Stage

Executes the migration by following the implementation plan and domain skill
phases. Runs the build gate after each phase and fixes errors before moving on.

---

## Startup

1. Read `.konveyor/implementation.md`
2. Scan `/opt/skills/` for skills with `tags: [domain]` in their frontmatter
3. Read the domain skill's phases, modules, and references
4. Read `.konveyor/questionnaire.json` for context on decisions made

If `.konveyor/execute.json` already exists with `status: "aborted"`, this is a
resume — see the Resume section below.

---

## Execution Loop

Follow the domain skill's phase order. For each phase, select only the steps
from `.konveyor/implementation.md` whose `Phase:` field matches.

If no domain skill is loaded, treat the implementation plan as a flat step list
and run the build after all steps are complete.

### For each step in the current phase:

1. Read the target file
2. Apply the transformation described in the step
3. Use domain skill references (dependency-map, api-map, config-map, pattern-map) for mappings
4. Write the modified file
5. Commit:
   ```bash
   git add -A && git commit -m "Step <N>: <short description>"
   ```
6. Record step status as `applied` with the commit hash
7. If the step cannot be applied (file missing, transformation impossible):
   record step status as `failed` with the error, and continue to the next step

### Resume

If `.konveyor/execute.json` already exists, check for steps already marked
`applied` — do not re-run them. Continue from the first pending step.

---

## Write Output and Commit

You MUST complete this step.

Write `.konveyor/execute.json`:

```json
{
  "status": "completed",
  "steps": [
    {
      "id": 1,
      "file": "<path>",
      "action": "MODIFY|CREATE|DELETE",
      "status": "applied|failed",
      "commit": "<hash or null>",
      "error": "<error message or null>"
    }
  ]
}
```

Commit:

```bash
git add .konveyor/execute.json
git commit -m "Add execute results"
```

Do NOT push.

---

## Rules

- Follow the domain skill's phase order exactly
- Select steps by `Phase:` field — only run steps matching the current phase
- Commit after each step — do NOT push
- Do NOT fix build errors — that is the verify stage's job
- Do NOT run tests — that is the verify stage's job
- Do NOT modify `.konveyor/implementation.md` or `.konveyor/spec.md`
- You MUST write `.konveyor/execute.json` before finishing
