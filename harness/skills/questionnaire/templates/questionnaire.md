# Questionnaire Output Format

The questionnaire stage writes `.konveyor/questionnaire.json` with two sections:
detection results and migration decisions.

## Schema

```json
{
  "detection": {
    "language": "<detected language>",
    "version": "<detected version>",
    "frameworks": ["<framework-1>", "<framework-2>"],
    "build_tool": "<build tool>",
    "app_server": "<app server or null>",
    "source_file_count": 0
  },
  "decisions": [
    {
      "id": 1,
      "question": "<decision that needs to be made>",
      "options": ["A) ...", "B) ...", "C) ..."],
      "recommendation": "<recommended option letter>",
      "chosen": "<chosen option letter>",
      "reasoning": "<why this option was chosen>"
    }
  ],
  "mode": "interactive|non-interactive"
}
```

## Fields

### detection

| Field | Description |
|---|---|
| `language` | Primary language (java, javascript, go, python, rust, csharp, etc.) |
| `version` | Language version detected from build manifest or source |
| `frameworks` | List of frameworks and libraries detected |
| `build_tool` | Build tool (maven, gradle, npm, cargo, dotnet, etc.) |
| `app_server` | Application server if detected, otherwise null |
| `source_file_count` | Approximate count of source files |

### decisions

Each decision captures a choice where multiple valid approaches exist.

| Field | Description |
|---|---|
| `id` | Sequential decision number |
| `question` | The decision stated clearly |
| `options` | 2-4 concrete options with brief trade-offs |
| `recommendation` | The option letter the agent recommended |
| `chosen` | The option letter that was selected |
| `reasoning` | Why this option was chosen over alternatives |

### mode

- `interactive` — decisions were presented to the user one at a time
- `non-interactive` — agent chose the best option for each decision autonomously
