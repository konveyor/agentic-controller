---
name: execute
description: >
  Reads the implementation plan and executes migration steps phase by phase.
  Does not build or test — that is the verify stage's job.
---

# Execute Stage

Executes the migration by following the implementation plan and domain skill
phases. Runs the build gate after each phase and fixes errors before moving on.

---

## Startup

1. Read `.konveyor/implementation.md`
2. Read `.konveyor/questionnaire.json` for context on decisions made

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
5. Record step status as `applied`
6. If the step cannot be applied (file missing, transformation impossible):
   record step status as `failed` with the error, and continue to the next step

---

## Write Output

You MUST complete this step.

Write `.konveyor/execute.json` following the schema below exactly. Do NOT invent
your own output format.

```json
{
  "status": "completed",
  "steps": [
    {
      "id": 1,
      "file": "<path>",
      "action": "MODIFY|CREATE|DELETE",
      "status": "applied|failed",
      "error": "<error message or null>"
    }
  ]
}
```

Fields: `status` is the overall execution status. Each `steps` entry records the
step `id` from the implementation plan, the `file` touched, the `action`
(MODIFY/CREATE/DELETE), `status` (`applied` or `failed`), and an `error` message
(or null if it succeeded).

---

## Rules

- Follow the domain skill's phase order exactly
- Select steps by `Phase:` field — only run steps matching the current phase
- Do NOT fix build errors — that is the verify stage's job
- Do NOT run tests — that is the verify stage's job
- Do NOT modify `.konveyor/implementation.md` or `.konveyor/spec.md`
- You MUST write `.konveyor/execute.json` before finishing
