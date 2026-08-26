---
name: execute
description: >
  Reads docs/plan.md and executes each migration step in order.
  Does not build or test — that is the verify stage's job.
---

# Execute Stage

Executes the migration by following the steps in `docs/plan.md` in order.

---

## Startup

1. Read `docs/plan.md`

---

## Execution Loop

Work through the steps in `docs/plan.md` in order.

### For each step:

1. Read the target file
2. Apply the transformation described in the step
3. Write the modified file
4. Record step status as `applied`
5. If the step cannot be applied (file missing, transformation impossible):
   record step status as `failed` with the error, and continue to the next step

---

## Write Output

You MUST complete this step.

Create `.konveyor/handoff.md` (make the `.konveyor/` directory first if it does
not exist) and write an `## Execute` section into it. This stage owns the creation
of the handoff file; later stages append their own sections to it.

Record the overall status, then one line per step from `docs/plan.md` — the step
`id`, the `file` touched, the `action` (MODIFY/CREATE/DELETE), the result
(`applied` or `failed`), and the error message for any failure.

```markdown
## Execute
- Status: completed

| Step | File | Action | Result | Error |
|------|------|--------|--------|-------|
| 1 | src/main/java/... | MODIFY | applied | — |
| 2 | src/main/resources/application.properties | CREATE | applied | — |
| 5 | src/main/java/... | MODIFY | failed | <error message> |
```

---

## Rules

- Follow the steps in `docs/plan.md` in order
- Do NOT fix build errors — that is the verify stage's job
- Do NOT run tests — that is the verify stage's job
- Do NOT modify `docs/plan.md`
- You MUST create `.konveyor/handoff.md` with your `## Execute` section before finishing
