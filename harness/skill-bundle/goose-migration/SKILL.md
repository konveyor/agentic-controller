---
name: goose-migration
description: >
  Umbrella migration skill for Goose AI agent sessions. Triggers on ANY of: "migrate
  this app", "migrate with goose", "goose migration", "clone and migrate", "java ee to
  quarkus", "python 2 to 3", "react class to hooks", "node upgrade", "goose keeps
  hitting rate limits", "set up goose for migration", "migration script". Handles the
  full end-to-end flow by routing to the correct sub-skill: first migration-plan (which
  discovers the project and gets user approval), then the appropriate execution skill
  (e.g. javaee-quarkus) once the plan is approved.
---

# Goose Migration — Umbrella Skill

This skill orchestrates the full migration workflow in two stages:

```
Stage 1: migration-plan     → discover → build plan → get approval
Stage 2: <execution skill>  → migrate one file at a time → compile → verify
```

---

## Supported Migration Types

| User says | Execution skill |
|---|---|
| Java EE → Quarkus | `skills/javaee-quarkus/SKILL.md` |
| Python 2 → Python 3 | `skills/python2-to-python3/SKILL.md` |
| React class → hooks | `skills/react-class-to-hooks/SKILL.md` |
| Node.js upgrade | `skills/react-class-to-hooks/SKILL.md` |

---

## How to Route

### Step 1 — Identify migration type

Ask the user if not already stated:
- What is the **source** stack? (e.g. Java EE 7, Python 2, React class components)
- What is the **target** stack? (e.g. Quarkus 3, Python 3, React hooks)
- Is the repo already cloned, or do you have a GitHub URL?

### Step 2 — Run migration-plan sub-skill

Always run `skills/migration-plan/SKILL.md` first regardless of migration type.
It handles discovery, plan generation, and approval gate.

### Step 3 — Run execution sub-skill

Only after the plan is approved, load the matching execution skill.
Pass the approved plan and project context to it.

---

## Token Safety Rules (apply throughout all sub-skills)

These rules are non-negotiable — enforce them in every stage:

- Read **ONE file at a time** — never `find | xargs cat` or for-loops
- After each file: **STOP and wait** for user to say `next`
- Do **NOT** re-read migrated files
- Do **NOT** reprint the full plan each turn
- Do **NOT** compile unless user types `compile`

---

## Session Commands (tell user at start)

| Command | Action |
|---|---|
| `next` | Migrate the next file, then stop |
| `compile` | Run build check, show first 30 lines only |
| `fix` | Fix first compiler error only, then stop |
| `status` | List completed vs remaining files |
| `retry` | Retry after rate limit (wait 60s first) |

---

## Rate Limit Guidance

| Model | Tokens/min | Best for |
|---|---|---|
| claude-sonnet-4-5 | 30,000 | Complex transforms (MDB, JNDI) |
| claude-haiku-4-5 | 100,000 | Mechanical imports, simple renames |

If rate limits hit: `goose configure` → switch to `claude-haiku-4-5-20251001`
