---
name: plan
tags: [stage]
description: >
  Analyzes a project using graphify, reads questionnaire decisions and
  analysis results, produces a migration spec for approval, then generates
  a detailed implementation plan.
inputs:
  - KONVEYOR_INSTRUCTIONS
  - .konveyor/questionnaire.json
  - .konveyor/analysis.json (optional)
  - domain skills
outputs:
  - .konveyor/spec.md
  - .konveyor/implementation.md
---

# Plan Stage

Analyzes the project deeply, produces a spec for approval, then writes the
implementation plan. `KONVEYOR_INSTRUCTIONS` is the primary goal; questionnaire
decisions supplement it. Does NOT modify any source files — planning only.

## Inputs

- `KONVEYOR_INSTRUCTIONS` — migration goal (primary)
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

Copy to `.konveyor/` for downstream stages:

```bash
mkdir -p .konveyor
cp graphify-out/graph.json .konveyor/graph.json
cp graphify-out/GRAPH_REPORT.md .konveyor/GRAPH_REPORT.md
```

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
decisions applied. Structure:

```markdown
# Migration Spec

## Goal
<restate KONVEYOR_INSTRUCTIONS in one sentence>

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

```markdown
# Implementation Plan

## Goal
<one sentence>
- Domain skill: <name of domain skill used, or "none">

## Project Summary
- Type: <build tool / framework>
- Files affected: <N>
- Estimated complexity: Low/Medium/High
- Hardest steps: <list the 1-3 most complex items>

## Steps

### Step 1: <title>
- Phase: <domain-skill-phase-name>
- File: <exact path from repo root>
- Action: CREATE | MODIFY | DELETE
- What to do: <specific instructions>
- Why: <what pattern is being changed>
- Depends on: <step numbers, or "none">
- Verify: <how to know this step is done>

...

## Verification
- Build: <build command, e.g. mvn clean compile, dotnet build, npm run build>
- Test: <test command if tests exist in the project, e.g. mvn test, npm test, pytest — omit if no tests found>
- Blackbox: if a README exists with instructions on how to run the application, include steps to start the app and verify key business flows still work (e.g. hit an endpoint, check a page loads, submit a form)

## Notes
<gotchas, special cases>
```

### Rules for writing steps

1. **Phase on every step** — every step must have a `Phase:` matching a domain skill phase
2. **One file per step** — never combine two files in one step
3. **Exact paths** — use real paths from graph.json, not placeholders
4. **Dependency order** — steps that others depend on come first
5. **Phase order** — follow the domain skill's phase ordering
6. **Hard steps flagged** — add `COMPLEX:` prefix for structural changes
7. **DELETE steps last** — after all modifications are done

### Step detail levels

**Mechanical** (simple find-replace):
```markdown
### Step 5: Migrate imports in <file>
- Phase: <domain-skill-phase-name>
- File: <exact path from graph.json>
- Action: MODIFY
- What to do: Replace all old namespace imports with new namespace imports
- Why: Target framework uses different namespace
- Depends on: Step 1
- Verify: No old namespace imports remain
```

**Complex** (structural/architectural changes — use domain skill patterns):
```markdown
### Step 14: COMPLEX — Convert message listener
- Phase: <domain-skill-phase-name>
- File: <path>
- Action: MODIFY
- What to do:
    - BEFORE: <old pattern from domain skill>
    - AFTER: <new pattern from domain skill>
    - Specific changes:
        1. Remove: <old imports/annotations/methods>
        2. Add: <new imports/annotations>
        3. Replace: <method signatures, configuration>
- Why: <from domain skill — why the old pattern isn't supported>
- Depends on: Step X, Step Y
- Verify: <from domain skill — grep checks, compile commands>
```

If a complex change also requires config file updates, create a separate step
for each config file rather than listing them as "Affected files" — one file
per step, always.

**CREATE** (new file):
```markdown
### Step 3: Create Quarkus application.properties
- Phase: <domain-skill-phase-name>
- File: src/main/resources/application.properties
- Action: CREATE
- What to do: Create file with <specific content from domain skill>
- Why: <target framework requires this config file>
- Depends on: Step 1
- Verify: File exists with required properties
```

**DELETE** (remove file):
```markdown
### Step 20: Remove legacy deployment descriptor
- Phase: <domain-skill-phase-name>
- File: src/main/webapp/WEB-INF/web.xml
- Action: DELETE
- What to do: Delete this file — no longer needed by target framework
- Why: <target framework does not use deployment descriptors>
- Depends on: Step 14, Step 15
- Verify: File no longer exists
```

---

## Phase 4 — Write Output and Commit

You MUST complete this phase — planning is not done until outputs are written
and committed.

1. Create the `.konveyor/` directory if it does not exist
2. Write `.konveyor/spec.md`
3. Write `.konveyor/implementation.md`
4. Commit the outputs:

```bash
git add .konveyor/spec.md .konveyor/implementation.md .konveyor/graph.json .konveyor/GRAPH_REPORT.md
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
