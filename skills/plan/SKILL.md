---
name: plan
description: >
  Reads a project and a goal statement, then produces PLAN.md in the repo root.
  The plan is specific to THIS project — real file paths, real dependencies, real
  layer ordering. Uses graphify to generate a code graph, then selectively reads
  complex files. Does NOT execute any changes. Output is always PLAN.md and
  nothing else. Use when starting a new migration to create the migration plan
  before any code changes.
---

# Plan Stage

Reads the project, understands the goal, writes `PLAN.md`.
Does NOT modify any source files — planning only.

## References

- [references/migration-plan-skill.md](references/migration-plan-skill.md) —
  detailed planning methodology with graph-powered discovery, selective reading
  strategy, and reference-driven plan generation
- [references/migration-phases.md](references/migration-phases.md) — generic
  migration phase guidance for migration types without a specific reference

## How This Works

1. **Reference-Driven**: You read a migration reference file (e.g., `javaee-quarkus.md`) that contains:
   - Migration order (layer dependencies)
   - Import/package transformations
   - Pattern catalog (before/after examples for complex changes)
   - Files to delete/create
   - Verification commands

2. **Graph-Powered**: The code graph (graph.json) provides:
   - Architectural layers (communities)
   - File relationships (edges)
   - High-risk files (god nodes)
   - Which files match which patterns (imports, annotations)

3. **Selective Reading**: You DON'T read every file. You:
   - Read the build manifest (1 file)
   - Read the reference (1 file)
   - Read 5-8 complex source files that need structural changes
   - Use the graph for everything else (imports, annotations, counts)

4. **Output**: Detailed PLAN.md with:
   - Specific file paths (from graph)
   - Specific transformations (from reference)
   - Correct layer order (from reference + graph communities)
   - Complex patterns marked with COMPLEX (from reference + god nodes)

---

## Phase 1 — Generate Code Graph

Run graphify on the project:

```bash
graphify update
```

This produces `graph.json` in the repo root.

---

## Phase 2 — Understand the Goal

Your overall migration goal is provided in the prompt above under
"Migration Context" and "Stage Task". Parse these to extract:

- **What** needs to change (e.g. javax to jakarta, Python 2 to 3, .NET Framework to .NET 8)
- **Scope** — all files? specific layers? specific patterns?
- **Target state** — what does "done" look like?
- **Constraints** — anything to preserve, avoid, or be careful about?

---

## Phase 3 — Discover the Project

### 3a. Read the Graph

Read `graph.json` to understand the project architecture:

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

### 3b. Match Patterns to Graph

Check `/opt/skills/*/references/` for domain-specific migration patterns
from loaded migration skills. These references contain migration order,
import mappings, and pattern catalogs for the specific migration type.

Use these patterns to identify which graph nodes need migration:

**Example (if a Java EE migration skill is loaded)**:
- Look for nodes where `attrs.annotations` contains `@MessageDriven` → Mark as COMPLEX
- Look for nodes where `attrs.imports` contains `javax.ejb` → EJB conversion needed
- Count nodes where `attrs.imports` contains `javax.persistence` → simple import replacement

### 3c. Build Migration Order

Map graph communities to migration layers:
- Community 0 (1 file: pom.xml) → Layer 1: Build
- Community 28 (5 files: *Entity.java) → Layer 2: Models
- Community 91 (8 files: *Service.java) → Layer 3: Services
- Community 164 (12 files: *Controller.java) → Layer 4: API

This gives you the migration sequence WITHOUT reading every file.

---

## Phase 4 — Read Selectively (max 5-8 files)

### When to Read a Source File

**READ these**:
- Build manifest (pom.xml, package.json, .csproj) — ALWAYS
- Files matching complex patterns:
  - MDB conversions (before/after structure is very different)
  - Security config changes
  - Lifecycle listeners (e.g., WebLogic ApplicationLifecycleListener)
  - JNDI lookups that need refactoring
- God nodes (high-degree) that use complex patterns

**DON'T READ these** (the graph is enough):
- Files that only need import changes (javax → jakarta)
- Files that only need annotation changes (@Stateless → @ApplicationScoped)
- Simple entity/model classes
- Simple REST controllers with basic CRUD

**Rules:**
- Read ONE file at a time
- Read ONLY files where the graph is not enough
- If uncertain about a file, mark the step COMPLEX and move on
- Total: ~8-10 file reads across all phases

---

## Phase 5 — Write PLAN.md

Write `PLAN.md` to the project root with this structure:

```markdown
# PLAN.md

## Goal
<restate the goal in one sentence>
- Reference used: <name of reference file, or "none">

## Project Summary
- Type: <Maven/Node/Python/.NET/etc>
- Files affected: <N>
- Estimated complexity: <Low/Medium/High>
- Hardest steps: <list the 1-3 most complex items>

## Steps

### Step 1: <title>
- File: <exact path from repo root>
- Action: <CREATE | MODIFY | DELETE>
- What to do: <specific instructions for this file>
- Why: <reason — what pattern is being changed>
- Depends on: <step numbers this must come after, or "none">
- Verify: <how to know this step is done correctly>

### Step 2: <title>
...

## Verification
<exact command(s) to run after all steps are done>

## Notes
<gotchas, special cases, decisions made>
```

### Rules for writing steps

1. **One file per step** — never combine two files in one step
2. **Exact paths** — use real paths from graph.json, not placeholders
3. **Dependency order** — steps that others depend on come first
4. **Layer order** — build config → app config → utils → persistence → models → services → REST/controllers → tests → cleanup/deletions
5. **Hard steps flagged** — add `COMPLEX:` prefix to title for MDB, JNDI, architecture changes, lifecycle listeners
6. **DELETE steps last** — after all modifications are done

### Step detail levels

**Mechanical** (simple find-replace changes):
```markdown
### Step 5: Migrate imports in Order.java
- File: src/main/java/com/example/model/Order.java
- Action: MODIFY
- What to do: Replace all `javax.persistence.*` → `jakarta.persistence.*`
- Why: Quarkus uses Jakarta EE namespace
- Depends on: Step 1
- Verify: No `javax.` imports remain in file
```

**Complex** (structural/architectural changes):
```markdown
### Step 14: COMPLEX — Convert message listener to new API
- File: <path>
- Action: MODIFY
- What to do:
    - BEFORE: <old pattern — e.g., @MessageDriven listener with JMS API>
    - AFTER: <new pattern — e.g., @Incoming reactive consumer>
    - Specific changes:
        1. Remove: <old imports/annotations/methods>
        2. Add: <new imports/annotations>
        3. Replace: <method signatures, configuration>
    - Affected files: <list config files that also need updates>
- Why: <why the old pattern is not supported>
- Depends on: Step X (prerequisite changes), Step Y (configuration)
- Verify: <grep checks, compile commands>
```

---

## Important

- Do NOT modify source files — planning only
- Do NOT execute any migration steps
- Do NOT skip graphify — the graph is essential for later stages
- Read selectively — the graph gives you most of what you need
- Report which reference you used in the Goal section of PLAN.md
