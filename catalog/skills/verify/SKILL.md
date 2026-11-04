---
name: verify
description: >
  Builds the migrated project and fixes build errors until it compiles, then runs
  tests and runtime verification, and confirms that every violation listed in
  .konveyor/analysis.json was addressed, using what the execute stage did. Re-runs
  the whole Verification section on the final tree before reporting. Appends its
  results to .konveyor/handoff.md.
---

# Verify Stage

Builds the migrated project, fixes build errors until it compiles, then runs
runtime verification to confirm the application actually starts and responds
correctly. Grounds its work in the analysis results and the decisions the execute
stage recorded. Does NOT re-execute migration steps.

---

## Startup

1. Read `docs/plan.md` — find the Verification section for build, test, and blackbox commands
2. Read the `## Execute` section of `.konveyor/handoff.md` — see which steps were applied or failed, and whether execution completed or was aborted
3. Read `.konveyor/analysis.json` — the original violations, so you can confirm the migration addressed them
4. If execution was aborted or steps failed, log a warning but still attempt verification

---

## Step 1 — Build Verification

Run the build command from `docs/plan.md`'s Verification section.

- **Build succeeds**: record `build: "passed"`, proceed to Step 2
- **Build fails**: enter fix loop

### Fix loop

For each build error:

1. Read the error message to identify the file and issue
2. Read the source file
3. Apply a fix
   - Never change business logic — if a fix would alter what the code does, record it and stop
   - Never remove or stub out methods to make the build pass

Re-run the build after each round of fixes and keep going until it compiles —
there is no limit on the number of rounds. Only stop early if an error genuinely
cannot be fixed without changing business logic, and record it.

A green build here is provisional: Step 4 re-runs it on whatever the tree looks
like after Steps 2 and 3, and that run is the one you report.

---

## Step 2 — Tests

If a test command exists in the `docs/plan.md`'s Verification section:

1. Run the test command
2. Record: passed count, failed count, total count
3. Do NOT fix failing tests — document them

If no test command exists, record `tests: "skipped"`.

---

## Step 3 — Runtime Verification

If the build passed, run runtime checks. If the build failed, skip this step
and record `runtime: "skipped"`.

### 3a. Start the application

Use the run command from the `docs/plan.md`'s Verification section, or
detect it from the framework.

Wait for the application to start (timeout: 60 seconds).

### 3b. Health check

Hit the application's health endpoint to verify it reached a ready state:
- Check the framework-specific health path (e.g. `/q/health/ready` for Quarkus, `/actuator/health` for Spring Boot)
- If no known health endpoint, try the application's root URL
- Record: `health_check: "passed"` or `"failed"` or `"skipped"` if no health check is defined

### 3c. Smoke tests

If the `docs/plan.md`'s Verification section lists endpoints or blackbox
steps (from README):
- Hit each listed endpoint
- Check HTTP status codes (expect 200 for GET endpoints)
- Record passed/failed count per endpoint

### 3d. Startup time

Record how long the application took to reach a healthy state (in milliseconds).
If `docs/plan.md` defines a startup threshold, flag if exceeded.

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

## Step 4 — Final Check

Every step above can edit the tree, and a fix made for one check can break
another — a lint or formatting edit is as capable of breaking the build as a
build fix is, and nothing in Steps 2 and 3 sends you back to the build.

After your last edit, re-measure every result on the tree as it now stands,
in this order:

1. Run the build command from `docs/plan.md`'s Verification section
2. Run the test command, if the Verification section has one
3. Redo Step 3's runtime checks — start the application, health check, smoke
   tests, startup time, log scan, clean shutdown

Then:

- **The build passes**: every result from this run replaces what you recorded
  earlier, and you go to `Write Output`
- **The build fails and the error can be fixed**: go back to Step 1's fix loop,
  then start this step again from the top
- **The build fails and the error genuinely cannot be fixed without changing
  business logic**: stop here — do not re-enter the fix loop for the same
  error. Go to `Write Output` and record `Build: failed` with that error,
  `Tests: skipped`, `Runtime: skipped` and `Status: failed`, so the stage hands
  off a failure instead of retrying one it cannot fix

The results you write in the next step come from this run. A command that
passed in an earlier round does not count, however late that round was: if you
edited a file afterwards, that result describes a tree that no longer exists.
If you touch a source file again after this run — including something you spot
while writing up — run this step again first.

---

## Write Output

You MUST complete this step.

Append a `## Verify` section to `.konveyor/handoff.md` (the execute stage created
the file). Do NOT overwrite the `## Execute` section or any earlier content.

Record the overall status, the build/test/runtime results, and a one-sentence
summary. Every line describes the tree as Step 4 left it, which is the tree
being handed on: `Build: passed` means the build command passed after your last
edit, not that it passed at the end of Step 1's fix loop. Overall status is
`passed` only if the build passed on that final run — test and runtime failures
are documented but do not block the stage. If Step 4 ended on a build that
cannot be fixed without changing business logic, that is `Status: failed`, with
`Tests: skipped` and `Runtime: skipped`: neither can be measured on a tree that
does not compile. `rounds` counts every build run in the stage — Step 1's
fix-loop rounds plus each Step 4 run. The harness reads the status line:
`failed` ends the run as refused and stops the workflow, with the `Summary`
line as the reason.

```markdown
## Verify
- Status: passed | failed
- Build: passed | failed (rounds: <n>, remaining errors: <list or none>)
- Tests: passed | failed | skipped (<passed>/<total>, failures: <list or none>)
- Runtime: passed | failed | skipped
  - Health check: passed | failed | skipped
  - Startup time: <ms>
  - Smoke tests: <passed>/<total>
  - Log warnings: <list or none>
  - Clean shutdown: yes | no
- Analysis follow-up: <which analysis incidents were confirmed resolved / still open>
- Summary: <one sentence: build, test, runtime status>
```

---

## Rules

- Do NOT re-execute migration steps — only verify
- Do NOT modify `docs/plan.md` or the `## Execute` section of `.konveyor/handoff.md`
- Fix build errors until it compiles, but never change business logic
- Document test failures, do not fix them
- Stop the application after runtime checks — do not leave it running
- Re-measure every result after your last edit — never report one from an
  earlier round
- Do NOT write `Status: passed` unless you have seen the build pass after your
  last edit; a build you cannot fix is `Status: failed`, not another trip
  through the fix loop
- You MUST append your `## Verify` section to `.konveyor/handoff.md` before finishing
