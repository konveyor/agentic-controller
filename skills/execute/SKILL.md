---
name: execute
description: >
  Reads PLAN.md and executes each migration step sequentially. Applies
  transformations file by file, consulting domain-specific reference patterns
  provided by loaded migration skills. Use after the plan stage has produced
  PLAN.md.
---

# Execute Stage

Executes the approved migration plan from `PLAN.md`, one file at a time.
Works autonomously — processes all items in sequence without waiting.

## References

- Check `/opt/skills/*/references/` for domain-specific transformation patterns
  (import maps, annotation replacements, file-type handling rules) from loaded
  migration skills

## Startup Sequence

1. Read `PLAN.md` from the repo root — read it ONCE
2. Check for reference files in other loaded skills at `/opt/skills/*/references/`
   for domain-specific transformation patterns
3. Begin executing steps in order, starting with Step 1

Do NOT read any source files before starting Step 1.

Domain-specific transformation patterns (import maps, annotation replacements,
file-type handling rules) are provided by migration skills loaded alongside this
stage skill. Consult their reference files for detailed patterns.

---

## Per-File Execution Loop

For each step in PLAN.md, follow this exact sequence:

```
1. Read the target file
2. Apply transformations per the step's instructions and reference patterns
3. Write the modified file
4. Move to the next step immediately
```

### Guardrails

- You MUST attempt every item in PLAN.md in order. Do not skip items.
- After completing each item, note it mentally before moving to the next.
- Do not re-read PLAN.md after every item — read it once, work through the list.
- If you cannot complete an item, note the reason and move to the next.
  Do not get stuck on one item.

---

## Important

- Work through ALL items — completeness matters more than perfection
- Do NOT run builds or tests — that is the verify stage's job
- Do NOT modify PLAN.md
