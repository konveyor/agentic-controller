# Spec Output Format

The plan stage writes `.konveyor/spec.md` — a summary of what will be migrated,
the approach, and key decisions applied.

## Schema

```markdown
# Migration Spec

## Goal
<restate the migration goal in one sentence>

## Source → Target
<source framework/version> → <target framework/version>

## Scope
- Files affected: <N>
- Estimated complexity: Low/Medium/High
- Hardest areas: <list 1-3 most complex>

## Key Decisions Applied
<from questionnaire.json — list each decision, chosen option, and reasoning>

## Approach
<phase-by-phase summary from domain skill>

## Domain Skill
<name and description of the domain skill being used, or "none">
```

## Fields

| Section | Description |
|---|---|
| `Goal` | One-sentence restatement of the migration goal |
| `Source → Target` | Source and target framework/version pair |
| `Scope` | File count, complexity estimate, and hardest areas |
| `Key Decisions Applied` | Decisions from questionnaire.json with chosen options and reasoning |
| `Approach` | Phase-by-phase migration summary from the domain skill |
| `Domain Skill` | Name and description of the domain skill, or "none" if not used |
