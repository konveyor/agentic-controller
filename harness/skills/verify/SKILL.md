---
name: verify
tags: [stage]
description: >
  Builds the project, fixes compiler errors conservatively, then runs
  runtime verification — health check, smoke tests, log scanning, and
  clean shutdown. Produces a verification report.
inputs:
  - .konveyor/implementation.md
  - .konveyor/execute.json
  - modified source files
  - domain skills
outputs:
  - fix patches committed to branch
  - .konveyor/verify.json
---

# Verify Stage

Builds the migrated project, fixes compiler errors without changing business
logic, then runs runtime verification to confirm the application actually
starts and responds correctly. Does NOT re-execute migration steps.

---

## Startup

1. Read `.konveyor/implementation.md` — find the Verification section for build, test, and blackbox commands
2. Read `.konveyor/execute.json` — check if execution completed or was aborted
3. Scan `/opt/skills/` for domain skills — read `references/verify-errors.md` if it exists for error-to-fix mappings
4. If execution was aborted, log a warning but still attempt verification

---

## Step 1 — Build Verification

Run the build command from the implementation plan's Verification section.

- **Build succeeds**: record `build: "passed"`, proceed to Step 2
- **Build fails**: enter fix loop

### Fix loop

For each compiler error:

1. Read the error message to identify the file and issue
2. Read the source file
3. Consult domain skill's `references/verify-errors.md` for known error-fix mappings
4. Apply a minimal fix — compiler error only
   - Never change business logic — if a fix would alter what the code does, record it and stop
   - Never remove or stub out methods to make the build pass
   - If unsure, record the error and move on
5. Commit:
   ```bash
   git add -A && git commit -m "Verify fix: <describe what was fixed>"
   ```

Repeat up to `KONVEYOR_PARAM_MAX_FIX_ITERATIONS` times (default 3).

If still failing: record `build: "failed"` with remaining errors.

---

## Step 2 — Tests

If a test command exists in the implementation plan's Verification section:

1. Run the test command
2. Record: passed count, failed count, total count
3. Do NOT fix failing tests — document them

If no test command exists, record `tests: "skipped"`.

---

## Step 3 — Runtime Verification

If the build passed, run runtime checks. If the build failed, skip this step
and record `runtime: "skipped"`.

### 3a. Start the application

Use the run command from the implementation plan's Verification section, or
detect it from the framework:

```bash
# The plan's Verification section should have a run command
# If not, try common patterns based on detected framework
```

Wait for the application to start (timeout: 60 seconds).

### 3b. Health check

Hit the application's health endpoint to verify it reached a ready state:
- Check the framework-specific health path (e.g. `/q/health/ready` for Quarkus, `/actuator/health` for Spring Boot)
- If no known health endpoint, try the application's root URL
- Record: `health_check: "passed"` or `"failed"`

### 3c. Smoke tests

If the implementation plan's Verification section lists endpoints or blackbox
steps (from README):
- Hit each listed endpoint
- Check HTTP status codes (expect 200 for GET endpoints)
- Record passed/failed count per endpoint

### 3d. Startup time

Record how long the application took to reach a healthy state (in milliseconds).
If the spec defines a startup threshold, flag if exceeded.

### 3e. Log scanning

Scan the application's startup logs for signs of incomplete migration:
- Errors or exceptions during startup
- Deprecation warnings
- Missing bean or dependency warnings
- Any references to old framework patterns that should have been migrated

Record any warnings found.

### 3f. Clean shutdown

Stop the application and verify it shuts down without errors.

---

## Write Output and Commit

You MUST complete this step.

Write `.konveyor/verify.json`:

```json
{
  "stage": "verify",
  "status": "passed|failed",
  "build": {
    "status": "passed|failed",
    "fix_iterations": 0,
    "remaining_errors": []
  },
  "tests": {
    "status": "passed|failed|skipped",
    "passed": 0,
    "failed": 0,
    "total": 0,
    "failures": []
  },
  "runtime": {
    "status": "passed|failed|skipped",
    "health_check": "passed|failed|skipped",
    "startup_time_ms": 0,
    "smoke_tests": {
      "passed": 0,
      "failed": 0
    },
    "log_warnings": [],
    "clean_shutdown": true
  },
  "summary": "<one sentence: build status, test results, runtime status>"
}
```

Top-level `status` is `passed` only if build passes. Test and runtime failures
are documented but do not block the stage.

Commit:

```bash
git add .konveyor/verify.json
git commit -m "Add verification results"
```

Do NOT push.

---

## Rules

- Do NOT re-execute migration steps — only verify
- Do NOT modify `.konveyor/implementation.md`, `.konveyor/spec.md`, or `.konveyor/execute.json`
- Fix only compiler errors, never business logic
- Document test failures, do not fix them
- Stop the application after runtime checks — do not leave it running
- You MUST write `.konveyor/verify.json` before finishing
- Commit when done — do NOT push
