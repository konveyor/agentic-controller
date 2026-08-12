# Execute Output Format

The execute stage writes `.konveyor/execute.json` with the overall execution
status and per-step results.

## Schema

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

## Fields

| Field | Description |
|---|---|
| `status` | Overall execution status: `completed` |
| `steps` | Array of per-step results |

### steps

| Field | Description |
|---|---|
| `id` | Step number from the implementation plan |
| `file` | File path that was modified, created, or deleted |
| `action` | One of MODIFY, CREATE, or DELETE |
| `status` | `applied` if the step succeeded, `failed` if it could not be applied |
| `commit` | Git commit hash for the step, or null if the step failed |
| `error` | Error message if the step failed, or null if it succeeded |
