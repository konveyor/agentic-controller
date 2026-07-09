---
name: migration-plan
description: >
  Sub-skill of migration-harness. Reads a project and a goal statement, then produces
  PLAN.md in the repo root. The plan is specific to THIS project — real file paths,
  real dependencies, real layer ordering. Does NOT execute any changes.
  Output is always PLAN.md and nothing else.
---

# Planner Sub-Skill

Reads the project, understands the goal, writes `PLAN.md`.
Does NOT modify any source files — planning only.

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
   - Complex patterns marked ⚠️ (from reference + god nodes)

---

## Phase 1 — Understand the Goal

Parse the goal statement to extract:

- **What** needs to change (e.g. javax → jakarta, Python 2 → 3, .NET Framework → .NET 8)
- **Scope** — all files? specific layers? specific patterns?
- **Target state** — what does "done" look like?
- **Constraints** — anything to preserve, avoid, or be careful about?

**Check if we have a reference for this**:
- Java EE → Quarkus? → references/javaee-quarkus.md
- Spring Boot 2 → 3? → references/springboot-2-to-3.md
- .NET Framework → .NET 8? → references/dotnet-framework-to-core.md
- Python 2 → 3? → references/python2-to-python3.md
- Something else? → Proceed with generic planning (use migration-phases.md)

This helps you know what patterns to look for in Phase 2.

---

## Phase 2 — Discover the Project & Select Reference

**If detect.json, graph.json, and file tree are provided in your context, skip discovery.**
Those are already collected — do NOT re-run discovery commands.

### 2a. Read Pre-Gathered Context

You should already have:
- **detect.json** - Manifest flags, file counts, graph stats
- **graph.json** - Full code graph with nodes, edges, communities, god nodes
- **File tree** - List of source and config files

Read these files using developer tools:
```bash
cat detect.json
cat graph.json  # May be large - read selectively if needed
```

### 2b. Select Migration Reference

**AVAILABLE REFERENCES** are listed in your context. Each reference file has frontmatter with `applies_to` rules.

**How to select:**
1. Check `detect.json.manifests` - which build files exist?
   - `pom_xml: true` → Java/Maven project
   - `package_json: true` → Node.js project
   - `csproj: true` → .NET project
   - `requirements_txt` or `pyproject_toml: true` → Python project

2. Check graph patterns from `graph.json`:
   - For Java: Look for `javax.ejb`, `javax.jms`, `@Stateless`, `@MessageDriven` imports/annotations
   - For Spring: Look for `org.springframework` imports
   - For .NET: Look for `System.Web` imports
   - For Python: Look for `__future__` imports, `print` statements

3. Match against reference frontmatter:
   ```yaml
   # Example: javaee-quarkus.md
   applies_to:
     manifests:
       pom_xml: true
     graph_patterns:
       - "imports contains javax.ejb"
       - "annotations contains @MessageDriven"
   ```

4. **Read the matching reference** using developer tools:
   ```bash
   cat references/javaee-quarkus.md
   # OR
   cat references/springboot-2-to-3.md
   # OR
   cat references/dotnet-framework-to-core.md
   # etc.
   ```

5. **If no reference matches**, proceed with generic migration planning (use migration-phases.md as guide).

**IMPORTANT**: You MUST report which reference you used (or "none") in your final response.

---

## Phase 3 — Identify What Needs Changing

### 3a. Use Graph to Understand Architecture

The code graph (graph.json) reveals:

1. **Communities (architectural layers)**:
   - Community 0 might be build files (pom.xml, package.json)
   - Smaller communities often = data models (few dependencies)
   - Medium communities = services, business logic
   - Large, high-degree communities = API/controllers

2. **God nodes (high-risk abstractions)**:
   - Nodes with degree > 20 are central to the system
   - Mark these as ⚠️ COMPLEX in the plan
   - Changes here ripple across many files

3. **Dependency flow**:
   - Use edges to understand: who depends on what?
   - Models → Services → Controllers (typical layering)

### 3b. Match Reference Patterns to Graph

The reference you read lists patterns. Check if they exist in the graph:

**Example (Java EE → Quarkus)**:
- Reference says: "Files with `@MessageDriven` need complex conversion"
- Graph query: Look for nodes where `attrs.annotations` contains `@MessageDriven`
- Result: `OrderServiceMDB.java`, `InventoryNotificationMDB.java` → Mark as ⚠️ COMPLEX

**Example (Spring Boot 2 → 3)**:
- Reference says: "All files with `javax.persistence` imports need update"
- Graph query: Count nodes where `attrs.imports` contains `javax.persistence`
- Result: 15 entity files → Simple import replacement (not complex)

### 3c. Build Migration Order from Reference

The reference specifies layer order (e.g., build → config → models → services → API).
Map this to graph communities:
- Community 0 (1 file: pom.xml) → Layer 1: Build
- Community 28 (5 files: *Entity.java) → Layer 2: Models
- Community 91 (8 files: *Service.java) → Layer 3: Services
- Community 164 (12 files: *Controller.java) → Layer 4: API

This gives you the migration sequence WITHOUT reading every file.

---

## Phase 4 — Read Selectively (max 5-8 files)

The reference identifies **complex patterns** that need structural changes. Read those files now.

### When to Read a Source File

**READ these**:
- Files matching complex patterns from the reference:
  - MDB conversions (before/after structure is very different)
  - Security config changes (e.g., WebSecurityConfigurerAdapter → SecurityFilterChain)
  - Lifecycle listeners (e.g., WebLogic ApplicationLifecycleListener → Quarkus events)
  - JNDI lookups that need refactoring
  - HTTP modules → middleware conversions
  
- God nodes (high-degree) that use complex patterns
- Files the reference marks as "not straightforward"

**DON'T READ these** (the graph is enough):
- Files that only need import changes (javax → jakarta)
- Files that only need annotation changes (@Stateless → @ApplicationScoped)
- Simple entity/model classes
- Simple REST controllers with basic CRUD

### Reading Strategy

1. Read the **build manifest** first (pom.xml, package.json, .csproj) - ALWAYS
2. Read ONE reference file (already done in Phase 2)
3. Read 5-8 complex source files based on patterns
4. Total: ~8-10 file reads across all phases

**Rules:**
- Read ONE file at a time
- Read ONLY files where the graph + reference isn't enough
- If uncertain about a file, mark the step ⚠️ and move on
- You can draft PLAN.md first, then read to refine (see Phase 5)

---

## Phase 5 — Write PLAN.md

Write `PLAN.md` to the project root. Use this exact structure:

**IMPORTANT**: The reference you read contains:
- **Migration Order** - Layer dependencies (build → config → models → services → API → cleanup)
- **Import/Package Transformations** - Simple find-and-replace mappings
- **Pattern Catalog** - Before/after examples for complex structural changes
- **Files to DELETE/CREATE** - What to remove and what to add
- **Build File Changes** - Specific updates to pom.xml, package.json, .csproj, etc.
- **Verification Commands** - How to test the migration

Use these sections from the reference to write specific, detailed steps.

```markdown
# PLAN.md

## Goal
<restate the goal in one sentence>
- Reference used: <name of reference file you read, or "none" if no specific reference>
  Example: "Reference used: javaee-quarkus.md"

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

### Rules for writing steps:

1. **One file per step** — never combine two files in one step
2. **Exact paths** — use real paths from discovery, not placeholders
3. **Dependency order** — steps that others depend on come first
4. **Layer order** — build config → app config → utils → persistence → models → services → REST/controllers → tests → cleanup/deletions
5. **Hard steps flagged** — add `⚠️ COMPLEX:` prefix to title for MDB, JNDI, architecture changes, lifecycle listeners
6. **DELETE steps last** — after all modifications are done
7. **Test files last** — after source files they test are migrated

### Step detail levels:

**Mechanical** (simple find-replace changes):
```markdown
### Step 5: Migrate imports in Order.java
- File: src/main/java/com/example/model/Order.java
- Action: MODIFY
- What to do: Replace all `javax.persistence.*` → `jakarta.persistence.*`
- Why: <Framework> uses Jakarta EE namespace
- Depends on: Step 1
- Verify: No `javax.` imports remain in file
```

**Complex** (structural/architectural changes — use reference's pattern catalog):

The reference file contains a **Pattern Catalog** with before/after examples. Use these for complex steps.

```markdown
### Step 14: ⚠️ COMPLEX — Convert message listener to new API
- File: <path>
- Action: MODIFY
- What to do:
    - BEFORE: <copy from reference pattern catalog — e.g., @MessageDriven listener with JMS API>
    - AFTER: <copy from reference pattern catalog — e.g., @Incoming reactive consumer>
    - Specific changes (from reference):
        1. Remove: <old imports/annotations/methods>
        2. Add: <new imports/annotations>
        3. Replace: <method signatures, configuration>
    - Affected files: <list config files that also need updates>
- Why: <Copy explanation from reference — why the old pattern isn't supported>
- Depends on: Step X (prerequisite changes), Step Y (configuration)
- Verify: <Copy verification from reference — grep checks, compile commands>
```

**TIP**: For each complex step, find the matching pattern in the reference's "Pattern Catalog" section and adapt it to this specific file.

**Examples for different migration types:**
- Java: `javax.*` → `jakarta.*`, EJB → CDI, JMS → reactive messaging
- Python: `print x` → `print(x)`, `xrange` → `range`, `unicode` → `str`
- .NET: `System.Web.Mvc` → `Microsoft.AspNetCore.Mvc`, `app.config` → `appsettings.json`
- JavaScript: `var` → `const/let`, `React.createClass` → function components, CommonJS → ES modules
- Go: `io/ioutil` → `io` + `os` (Go 1.16+ deprecations)

---

## Phase 6 — Handoff

After writing PLAN.md, you must provide a structured response with:

1. **plan_written**: `true`
2. **step_count**: Number of steps in PLAN.md
3. **complex_count**: Number of steps marked with ⚠️ COMPLEX
4. **reference_used**: Name of reference file you read (e.g., "javaee-quarkus", "springboot-2-to-3", "dotnet-framework-to-core", "python2-to-python3") OR "none" if no specific reference
5. **summary**: One-line description of what the plan does

**Example response**:
```json
{
  "plan_written": true,
  "step_count": 34,
  "complex_count": 5,
  "reference_used": "javaee-quarkus",
  "summary": "Migrate 28 Java files from Java EE 7/WebLogic to Quarkus 3, including 3 MDB conversions and WebLogic lifecycle cleanup"
}
```

Do not proceed further. The harness handles the approval gate.
