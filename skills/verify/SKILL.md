---
name: verify
description: >
  Runs the project build command, parses compiler errors, applies conservative
  fixes, and iterates until the build passes or max iterations are reached.
  Use after the execute stage has finished migrating source files.
---

# Verify Stage

Verifies the migrated codebase compiles and tests pass. Can attempt
targeted fixes for compilation errors, up to a configurable iteration limit.

## References

- Check `/opt/skills/*/references/` for domain-specific error-fix mappings
  from loaded migration skills.

---

## Phase 1 — Initial Build

Run the build command from the PLAN.md Verification section.

If the build succeeds (exit code 0), skip to Phase 4 (Run Tests).

If the build fails, extract the errors from the build output.

---

## Phase 2 — Fix Errors

For each compiler error:

1. Read the error message to identify the file and issue
2. Read the source file
3. Apply a minimal, conservative fix
4. Do NOT change code that is not related to the error

Consult reference files from loaded migration skills at
`/opt/skills/*/references/` for common error-fix mappings specific to
this migration type.

### Fix Rules

- Fix ONLY compiler errors, not warnings
- Minimal changes only — do not refactor working code
- Only touch the file reported in the error

---

## Phase 3 — Re-verify

After fixing errors, run the build command again.

Repeat Phases 2-3 up to the number of iterations specified by
`KONVEYOR_PARAM_MAX_FIX_ITERATIONS` (read from environment, default 3).

If the build still fails after max iterations, report failure with
the remaining errors.

---

## Phase 4 — Run Tests (if build passes)

Run the test command from the PLAN.md Verification section.

Report test results (passed/failed/total counts) but do NOT attempt
to fix failing tests. Test failures are expected after a migration and
are documented in the result, not fixed here.

---

## Phase 5 — Write Result

Append your result to `.konveyor/result.json`:

Read the existing file (it should have plan and execute entries),
parse the JSON array, append your entry, and write it back.

Your entry on success:

```json
{"stage": "verify", "status": "succeeded", "summary": "<1-2 sentences: build/test results and any fixes applied>"}
```

On failure:

```json
{"stage": "verify", "status": "failed", "reason": "build failed after N fix iterations: <remaining errors>", "summary": "<what was tried and what errors remain>"}
```

---

## Important

- Fixes must be minimal and conservative — do not rewrite working code
- Only fix compiler errors, not warnings
- Do NOT modify PLAN.md or files unrelated to the error
- Read `KONVEYOR_PARAM_MAX_FIX_ITERATIONS` from environment for iteration cap
- Track how many fix iterations you have attempted
- Report remaining errors in the result reason if build still fails
