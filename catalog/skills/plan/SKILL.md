---
name: plan
description: >
  Analyzes a project by reading its build manifest, layout, and source, and uses
  the Konveyor analysis results (.konveyor/analysis.json) to work out the migration
  approach and dependency-ordered steps toward the target named in the prompt, and
  produces a single migration plan for approval. Produces docs/plan.md.
---

# Plan Stage

Analyzes the project deeply and writes a single migration plan for approval. The
migration goal comes from the prompt. Does NOT modify any source files — planning
only.

## Inputs

- `.konveyor/analysis.json` — Kantra rule violations and patterns, **if present**


---

## Phase 1 — Analyze

### 1a. Read Prior Stage Outputs (if present)

1. If `.konveyor/analysis.json` exists, read it for rule violations and patterns

If it doesn't exist, derive the source stack and target directly from the prompt
and the code.

### 1b. Understand the Project Architecture

Read the build manifest and walk the source layout to understand the project.
Do this by reading files — do not build or execute the project.

1. **Build manifest** (pom.xml, build.gradle, package.json, *.csproj, etc.):
   dependencies, modules/sub-projects, plugins, packaging type.
2. **Directory layout and layers**: identify how the code separates into layers —
   data models/entities, services/business logic, controllers/API, persistence,
   configuration.
3. **Dependency flow**: from imports and package structure, work out who depends
   on what (typically Models → Services → Controllers).
4. **High-risk abstractions**: files that many others import, or that carry the
   most framework-specific patterns. Mark these as COMPLEX in the plan — changes
   here ripple across many files.

### 1c. Match Patterns to Files

Use the target's migration patterns to identify which files need migration. Check
each file's imports, declarations, and configuration against the target's
transformation rules to classify it:

- Simple (mechanical import or declaration replacement only)
- Complex (structural changes needed)
- Delete (file will be removed)
- Create (new file needed)

Use the analysis results as well: if `.konveyor/analysis.json` is present, each
incident pins a `file`, `line`, and `message` for a required change — cross-check
your classification against it and make sure every flagged incident maps to a step.

### 1d. Build Migration Order

Map the layers you identified to the migration phase order:

```
Build manifest (1 file)          → Phase 1: Build config
Data models (5 files)            → Phase 2: Models
Services (8 files)               → Phase 3: Services
Controllers / API (12 files)     → Phase 4: API
```

This gives you the migration sequence WITHOUT reading every file in full.

### 1e. Selectively Read Complex Source Files (max 5-8)

Read files where the layout and manifest alone aren't enough — structural changes,
high-risk abstractions, complex migration patterns. Don't fully read
files that only need mechanical import or declaration changes.

---

## Phase 2 — Write the Plan

Write a single `docs/plan.md` — a summary of what will be migrated followed by
step-by-step migration instructions the execute stage follows. Use the structure
below exactly; do NOT invent your own headings.

```markdown
# Migration Plan

## Goal
<restate the migration goal in one sentence>

## Source → Target
<source framework/version> → <target framework/version>

## Scope
- Files affected: <N>
- Estimated complexity: Low/Medium/High
- Hardest areas: <list 1-3 most complex>

## Key Decisions Applied
<only where the prompt, target, or source left something unclear or unspecified
and you had to choose — list each such decision, the option chosen, and the
reasoning. If everything was clear, state "none".>

## Approach
<phase-by-phase summary of the migration approach>

## Steps

### Step 1: <title>
- Phase: <migration-phase-name>
- File: <exact path from repo root>
- Action: CREATE | MODIFY | DELETE
- What to do: <specific instructions>
- Why: <what pattern is being changed>
- Depends on: <step numbers, or "none">
- Verify: <how to know this step is done>

...

## Verification
- Build: <build command, e.g. mvn clean compile, dotnet build, npm run build>
- Test: <test command if tests exist, e.g. mvn test, npm test, pytest — omit if none>
- Blackbox: if a README exists with run instructions, include steps to start the app and verify key business flows still work

## Notes
<gotchas, special cases>
```

### Rules for writing steps

1. **Phase on every step** — every step must have a `Phase:` matching a migration phase
2. **One file per step** — never combine two files in one step
3. **Exact paths** — use real paths from the repo, not placeholders
4. **Dependency order** — steps that others depend on come first
5. **Phase order** — follow the migration phase ordering
6. **Hard steps flagged** — add `COMPLEX:` prefix for structural changes
7. **DELETE steps last** — after all modifications are done

### Step detail levels

Match the detail to the change. Examples:

**Mechanical (simple find-replace):**

```markdown
### Step 5: Migrate imports in <file>
- Phase: <migration-phase-name>
- File: <exact path>
- Action: MODIFY
- What to do: Replace all old namespace imports with new namespace imports
- Why: Target framework uses different namespace
- Depends on: Step 1
- Verify: No old namespace imports remain
```

**Complex (structural/architectural — use migration patterns):**

```markdown
### Step 14: COMPLEX — Convert message listener
- Phase: <migration-phase-name>
- File: <path>
- Action: MODIFY
- What to do:
    - BEFORE: <old framework pattern>
    - AFTER: <new framework pattern>
    - Specific changes:
        1. Remove: <old imports/annotations/methods>
        2. Add: <new imports/annotations>
        3. Replace: <method signatures, configuration>
- Why: <why the old pattern isn't supported by the target>
- Depends on: Step X, Step Y
- Verify: <grep checks, compile commands>
```

If a complex change also requires config file updates, create a separate step
for each config file — one file per step, always.

**CREATE (new file):**

```markdown
### Step 3: Create Quarkus application.properties
- Phase: <migration-phase-name>
- File: src/main/resources/application.properties
- Action: CREATE
- What to do: Create file with <specific content for the target>
- Why: <target framework requires this config file>
- Depends on: Step 1
- Verify: File exists with required properties
```

**DELETE (remove file):**

```markdown
### Step 20: Remove legacy deployment descriptor
- Phase: <migration-phase-name>
- File: src/main/webapp/WEB-INF/web.xml
- Action: DELETE
- What to do: Delete this file — no longer needed by target framework
- Why: <target framework does not use deployment descriptors>
- Depends on: Step 14, Step 15
- Verify: File no longer exists
```

---

## Phase 3 — Approval

You MUST write `docs/plan.md` — planning is not done until it is written. Create
the `docs/` directory if it does not exist.

If the harness runs this stage in an approval mode that surfaces the plan to a
human, present `docs/plan.md` for approval before finishing.

- If the plan is **approved**: the stage is complete.
- If the plan is **rejected**: ask what needs to change, revise `docs/plan.md`, and
  re-present for approval. Repeat until approved.

Otherwise, finish once `docs/plan.md` is written.

---

## Rules

- Do NOT modify source files — planning only
- Do NOT execute any migration steps
- Analyze by reading files — do not build or execute the project
- You MUST write `docs/plan.md` before finishing
