# Migration-Plan Skill Updates

## What Changed

The `migration-plan/SKILL.md` has been updated to work with the new reference system.

---

## New: Reference-Driven Planning (Added to Top)

**Before**: Generic description
**After**: Clear explanation of the reference-driven approach:

```markdown
## How This Works

1. **Reference-Driven**: Read a migration reference (javaee-quarkus.md)
   - Migration order, transformations, patterns

2. **Graph-Powered**: Use code graph for architecture
   - Communities, edges, god nodes

3. **Selective Reading**: Only read complex files (5-8 max)
   - Not every file, just structurally complex ones

4. **Output**: Detailed PLAN.md with specific paths and transformations
```

---

## Phase 1 Updates: Check Available References

**Added**:
```markdown
**Check if we have a reference for this**:
- Java EE → Quarkus? → references/javaee-quarkus.md
- Spring Boot 2 → 3? → references/springboot-2-to-3.md
- .NET Framework → .NET 8? → references/dotnet-framework-to-core.md
- Python 2 → 3? → references/python2-to-python3.md
- Something else? → Proceed with generic planning
```

**Why**: Helps planner know what to look for in detect.json + graph.json

---

## Phase 2 Updates: Reference Selection Logic

**Before**: Generic "read the reference that matches"
**After**: Specific selection algorithm:

```markdown
### 2a. Read Pre-Gathered Context
- detect.json (manifest flags, file counts)
- graph.json (code structure)

### 2b. Select Migration Reference

**How to select:**
1. Check detect.json.manifests (pom_xml? package_json?)
2. Check graph patterns (javax.ejb? System.Web?)
3. Match against reference frontmatter
4. Read the matching reference
5. Report which reference you used
```

**Why**: Explicit instructions for auto-selection based on frontmatter

---

## Phase 3 Updates: Graph + Reference Integration

**Before**: Generic "use graph to understand dependencies"
**After**: Specific instructions on combining graph + reference:

```markdown
### 3a. Use Graph to Understand Architecture
- Communities = architectural layers
- God nodes = high-risk (mark as ⚠️)
- Edges = dependency flow

### 3b. Match Reference Patterns to Graph
Example: Reference says "@MessageDriven needs conversion"
→ Query graph for nodes with @MessageDriven annotation
→ Mark those files as ⚠️ COMPLEX

### 3c. Build Migration Order from Reference
Map reference layers to graph communities:
- Community 0 → Build layer
- Community 28 → Models layer
- Community 91 → Services layer
```

**Why**: Shows how to USE the reference patterns with graph data

---

## Phase 4 Updates: When to Read Source Files

**Before**: "Read complex files (max 5)"
**After**: Explicit criteria for reading vs using graph:

```markdown
### When to Read a Source File

**READ these**:
- MDB conversions (structural change)
- Security config changes
- Lifecycle listeners
- JNDI lookups
- HTTP modules → middleware
- God nodes using complex patterns

**DON'T READ these** (graph is enough):
- Simple import changes (javax → jakarta)
- Simple annotation changes (@Stateless → @ApplicationScoped)
- Entity/model classes
- Basic CRUD controllers
```

**Why**: Clear decision criteria reduces unnecessary file reads

---

## Phase 5 Updates: Reference Structure

**Added** before the PLAN.md template:

```markdown
**IMPORTANT**: The reference you read contains:
- **Migration Order** - Layer dependencies
- **Import/Package Transformations** - Find-and-replace
- **Pattern Catalog** - Before/after examples
- **Files to DELETE/CREATE**
- **Build File Changes**
- **Verification Commands**

Use these sections to write specific, detailed steps.
```

**Also added to Goal section**:
```markdown
## Goal
<restate the goal>
- Reference used: <name of reference file, or "none">
  Example: "Reference used: javaee-quarkus.md"
```

**Why**: Reminds planner to USE the reference sections and REPORT which was used

---

## Phase 5 Updates: Complex Step Template

**Before**: Generic before/after template
**After**: Reference-driven template:

```markdown
**TIP**: For each complex step, find the matching pattern in the 
reference's "Pattern Catalog" section and adapt it to this file.

### Step 14: ⚠️ COMPLEX — Convert message listener
- What to do:
    - BEFORE: <copy from reference pattern catalog>
    - AFTER: <copy from reference pattern catalog>
    - Specific changes (from reference):
        1. Remove: <from reference>
        2. Add: <from reference>
        3. Replace: <from reference>
```

**Why**: Explicit instruction to copy/adapt from reference patterns

---

## Phase 6 Updates: Structured Response

**Before**: Simple text output
**After**: Required JSON schema:

```json
{
  "plan_written": true,
  "step_count": 34,
  "complex_count": 5,
  "reference_used": "javaee-quarkus",  // ← REQUIRED
  "summary": "Migrate 28 Java files..."
}
```

**Why**: Ensures the harness can track which reference was used

---

## Summary of Changes

| Phase | Before | After |
|-------|--------|-------|
| Header | Generic description | Reference-driven workflow explanation |
| Phase 1 | Parse goal | Parse goal + check available references |
| Phase 2 | "Read reference that matches" | Explicit selection algorithm with frontmatter |
| Phase 3 | "Use graph for dependencies" | Graph + reference pattern matching |
| Phase 4 | "Read max 5 files" | Clear criteria: WHEN to read vs use graph |
| Phase 5 | Generic template | Reference-aware template with sections |
| Phase 6 | Text output | Structured JSON with `reference_used` |

---

## Key Benefits

1. **Reference Selection**: Planner knows HOW to pick the right reference (frontmatter matching)
2. **Graph Integration**: Planner knows HOW to use graph + reference together
3. **Selective Reading**: Clear criteria for when to read source files
4. **Pattern Reuse**: Explicit instruction to copy/adapt from reference catalog
5. **Tracking**: Required to report which reference was used

---

## Testing the Updated Skill

```bash
cd ~/migration-harness
./install.sh  # Reinstall with updated skill

# Test with a Java EE project
migration-harness ~/coolstore "Migrate to Quarkus 3"

# Check the output for:
✓ 2e2. reference used: javaee-quarkus
```

The planner should now:
1. Auto-select `javaee-quarkus.md` based on pom.xml + javax.ejb patterns
2. Use the reference's pattern catalog for complex steps
3. Report "Reference used: javaee-quarkus.md" in PLAN.md Goal section
4. Return `"reference_used": "javaee-quarkus"` in JSON response
