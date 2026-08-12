---
name: plan
description: >
  Analyzes a project using graphify, reads questionnaire decisions and
  analysis results, produces a migration spec for approval, then generates
  a detailed implementation plan. Produces .konveyor/spec.md and .konveyor/implementation.md.
---

# Plan Stage

Analyzes the project deeply, produces a spec for approval, then writes the
implementation plan. The migration goal comes from the prompt; questionnaire
decisions supplement it. Does NOT modify any source files — planning only.

## Inputs

- `.konveyor/questionnaire.json` — decisions from prior stage
- `.konveyor/analysis.json` — Kantra rule violations and patterns (if present)
- Domain skills (`tags: [domain]`) — migration knowledge (phases, modules, references)

---

## Phase 1 — Analyze

### 1a. Read Prior Stage Outputs

1. Read `.konveyor/questionnaire.json` for detection results and decisions
2. Read `.konveyor/analysis.json` if present for rule violations and patterns

### 1b. Generate Code Graph

Run graphify on the project:

```bash
graphify update
```

This produces `graphify-out/graph.json` and `graphify-out/GRAPH_REPORT.md`.


### 1c. Discover Domain Skills

Scan `/opt/skills/` for skills with `tags: [domain]` in their frontmatter. Read
their SKILL.md, modules, and references to understand:

- Phase order (which transformations come first)
- What patterns to look for in the graph
- Mapping tables (dependencies, APIs, config, patterns)
- Build command (`metadata.build_command`)

### 1d. Understand the Project Architecture

Read `graphify-out/graph.json` to understand the project:

1. **Communities (architectural layers)**:
   - Community 0 might be build files (pom.xml, package.json)
   - Smaller communities often = data models (few dependencies)
   - Medium communities = services, business logic
   - Large, high-degree communities = API/controllers

2. **God nodes (high-risk abstractions)**:
   - Nodes with degree > 20 are central to the system
   - Mark these as COMPLEX in the plan
   - Changes here ripple across many files

3. **Dependency flow**:
   - Use edges to understand: who depends on what?
   - Models → Services → Controllers (typical layering)

### 1e. Match Patterns to Graph

Use the domain skill's patterns to identify which graph nodes need migration.
Check node attributes (imports, annotations) against the domain skill's
transformation rules to classify each file:

- Simple (import/annotation replacement only)
- Complex (structural changes needed)
- Delete (file will be removed)
- Create (new file needed)

### 1f. Build Migration Order

Map graph communities to the domain skill's phase order:

```
Community 0  (1 file: build manifest)    → Phase 1: Build config
Community 28 (5 files: data models)      → Phase 2: Models
Community 91 (8 files: services)         → Phase 3: Services
Community 164 (12 files: controllers)    → Phase 4: API
```

This gives you the migration sequence WITHOUT reading every file.

### 1g. Selectively Read Complex Source Files (max 5-8)

Read files where the graph alone isn't enough — structural changes, god nodes,
complex patterns from the domain skill. Don't read files that only need
import or annotation changes.

---

## Phase 2 — Spec

Write `.konveyor/spec.md` — a summary of what will be migrated, the approach, and key
decisions applied. See [templates/spec.md](templates/spec.md) for the output format.

### Interactive mode

If `KONVEYOR_PARAM_INTERACTIVE` is `true`: present `.konveyor/spec.md` for
approval.

- If the user **approves**: proceed to Phase 3.
- If the user **rejects**: ask what needs to change, revise the spec, and
  re-present for approval. Repeat until approved.

### Non-interactive mode (default)

If `KONVEYOR_PARAM_INTERACTIVE` is unset or not `true`: proceed directly to
Phase 3. This is the default — interactive mode requires explicit opt-in.

---

## Phase 3 — Implementation Plan

Write `.konveyor/implementation.md` — step-by-step migration instructions.
See [templates/implementation.md](templates/implementation.md) for the output
format, step field definitions, detail levels, and rules for writing steps.

---

## Phase 4 — Write Output and Commit

You MUST complete this phase — planning is not done until outputs are written
and committed.

1. Create the `.konveyor/` directory if it does not exist
2. Write `.konveyor/spec.md`
3. Write `.konveyor/implementation.md`
4. Commit the outputs:

```bash
git add .konveyor/spec.md .konveyor/implementation.md
git commit -m "Add migration plan and spec"
```

Do NOT push.

The stage is NOT complete until both files are written and committed.

---

## Rules

- Do NOT modify source files — planning only
- Do NOT execute any migration steps
- Do NOT skip graphify — the graph is essential for dependency ordering
- Follow the domain skill's phase order when structuring steps
- Honor questionnaire decisions — if a decision was made, do not re-derive your own preference
- You MUST write `.konveyor/spec.md` and `.konveyor/implementation.md` before finishing
- Commit plan outputs when done — do NOT push
