# Verify Output Format

The verify stage writes `.konveyor/verify.json` with build, test, and runtime
verification results.

## Schema

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

## Fields

### Top-level

| Field | Description |
|---|---|
| `stage` | Always `"verify"` |
| `status` | `passed` only if build passes; test and runtime failures are documented but do not block |
| `summary` | One-sentence summary of build status, test results, and runtime status |

### build

| Field | Description |
|---|---|
| `status` | `passed` or `failed` |
| `fix_iterations` | Number of fix loop iterations attempted |
| `remaining_errors` | Array of compiler errors that could not be fixed |

### tests

| Field | Description |
|---|---|
| `status` | `passed`, `failed`, or `skipped` (if no test command exists) |
| `passed` | Count of passing tests |
| `failed` | Count of failing tests |
| `total` | Total test count |
| `failures` | Array of test failure details |

### runtime

| Field | Description |
|---|---|
| `status` | `passed`, `failed`, or `skipped` (if build failed) |
| `health_check` | `passed`, `failed`, or `skipped` |
| `startup_time_ms` | Time in milliseconds for the application to reach healthy state |
| `smoke_tests.passed` | Count of smoke test endpoints that returned expected status |
| `smoke_tests.failed` | Count of smoke test endpoints that failed |
| `log_warnings` | Array of warnings found in startup logs (errors, deprecations, missing beans) |
| `clean_shutdown` | Whether the application shut down without errors |
