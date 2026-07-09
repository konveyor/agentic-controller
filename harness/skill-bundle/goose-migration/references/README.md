# Migration References — Writing Guide

This directory contains **migration knowledge** files that teach the planner how to migrate between technology stacks. Each reference is a **pattern catalog** that the LLM reads during the planning step to generate specific migration plans.

---

## What is a Reference File?

A reference file is **NOT executable code** — it's a knowledge document that describes:
- What needs to change (imports, annotations, patterns)
- How to change it (before/after examples)
- What order to follow (layer dependencies)
- What to verify (build commands, grep checks)

Think of it as a **migration cookbook** for a specific source → target pair.

---

## When to Create a Reference

Create a reference when you want to migrate between:
- **Frameworks**: Java EE → Quarkus, Spring Boot 2 → 3, Express 4 → 5
- **Languages**: Python 2 → 3, JavaScript → TypeScript
- **Platforms**: .NET Framework → .NET 8, Node 16 → 20
- **Architectures**: Monolith → Microservices, REST → GraphQL

---

## Reference File Structure

Use this template for ALL references (language-agnostic):

```markdown
---
name: <source>-to-<target>
description: Migration patterns for <Source X.Y> to <Target Z.W>
applies_to:
  manifests:
    <manifest_file_key>: true    # e.g., pom_xml, package_json, go_mod, cargo_toml
  graph_patterns:
    - "<pattern description>"     # e.g., "imports contains javax.ejb"
    - "<pattern description>"     # e.g., "annotations contains @MessageDriven"
---

# <Source> → <Target>: Migration Reference

## Migration Order (Layer Dependency)

Always migrate in this order — dependencies flow upward:

1. **Build config** - <build file names>
2. **App config** - <config file names>
3. **Utils/Common** - <description>
4. **Data layer** - <description>
5. **Business logic** - <description>
6. **API layer** - <description>
7. **Cleanup** - delete legacy files

**Why this order**: <Explain dependency flow>

---

## Import/Package Transformations

Simple find-and-replace (mechanical changes):

| Old | New | Notes |
|-----|-----|-------|
| `old.package.*` | `new.package.*` | Optional explanation |

---

## Annotation/Decorator Transformations

| Old | New | Notes |
|-----|-----|-------|
| `@OldAnnotation` | `@NewAnnotation` | Why this changed |

---

## Pattern Catalog

For each complex structural change, provide a before/after example.

### Pattern: <Descriptive Name>

**BEFORE:**
```<language>
<source code showing old pattern>
```

**AFTER:**
```<language>
<target code showing new pattern>
```

**Specific changes:**
1. Remove: <what to remove>
2. Add: <what to add>
3. Replace: <what to replace>

**Why**: <Explain what changed at the architectural level>

---

## Files to DELETE

| Delete this | Replaced by |
|-------------|-------------|
| `path/to/old/file` | `new/path/or/config` or "Not needed" |

**Why delete**: <Explain why these files are obsolete>

---

## Files to CREATE

| File | Purpose | Key contents |
|------|---------|--------------|
| `path/to/new/file` | <What it replaces> | <Template or key properties> |

---

## Build File Changes

### <build-file-name> (e.g., pom.xml, package.json, Cargo.toml)

```
REMOVE: <what to remove>
ADD: <what to add>
CHANGE: <what to change>
```

List specific dependencies, plugins, or settings.

---

## Verification Commands

```bash
# 1. Build/compile check
<build command> 2>&1 | tail -30

# 2. Check for old imports/patterns
grep -rn "<old pattern>" src/ | wc -l  # Should be 0

# 3. Check for old annotations
grep -rn "@OldAnnotation" src/ | wc -l  # Should be 0

# 4. Run in dev mode
<start dev server command>
```

---

## Notes / Gotchas

- **Edge case 1**: <Describe special handling needed>
- **Edge case 2**: <Describe common pitfall>
- **Performance**: <Any performance implications>
- **Breaking changes**: <List breaking changes users should know about>
```

---

## Frontmatter Fields Explained

### `name`
Kebab-case identifier matching the filename (without `.md`).
Example: `javaee-quarkus`, `python2-to-python3`, `springboot-2-to-3`

### `description`
One-line summary of what this migration does. Used in logs and to help the LLM understand if this reference applies.

### `applies_to`
Rules for auto-selecting this reference based on `detect.json` output.

#### `manifests`
Maps to `detect.json.manifests` object. Keys:
- `pom_xml` - Java/Maven (pom.xml)
- `package_json` - Node.js/npm (package.json)
- `pyproject_toml` - Python (pyproject.toml)
- `requirements_txt` - Python (requirements.txt)
- `setup_py` - Python (setup.py)
- `go_mod` - Go (go.mod)
- `cargo_toml` - Rust (Cargo.toml)
- `gemfile` - Ruby (Gemfile)
- `csproj` - .NET (.csproj)

Example:
```yaml
applies_to:
  manifests:
    pom_xml: true      # Only applies if pom.xml exists
```

#### `graph_patterns`
Describes patterns the LLM should look for in the code graph. These are **human-readable hints**, not exact jq queries.

The planner will check `graph.json` for matching patterns by:
- Reading node attributes (`attrs.imports`, `attrs.annotations`)
- Checking edge relationships (`relation: "imports"`)
- Counting occurrences

Examples:
```yaml
graph_patterns:
  - "imports contains javax.ejb"
  - "annotations contains @MessageDriven"
  - "annotations contains @Stateless"
  - "imports contains System.Web"
  - "imports contains __future__"      # Python 2 indicator
```

The LLM uses these hints to confirm applicability, not as strict filters.

---

## How the Planner Uses References

### Step 1: Auto-Selection (in `step-plan.sh`)

```bash
# Planner checks detect.json against each reference's applies_to:
if detect.json.manifests.pom_xml == true 
   AND graph contains "imports contains javax.ejb"
   THEN select references/javaee-quarkus.md
```

### Step 2: Read During Planning

The selected reference is passed to goose during the planning step:
```
YOUR JOB:
1. Read detect.json and graph.json
2. Read the build manifest (pom.xml, package.json, etc.)
3. Read ONE matching reference file (javaee-quarkus.md)
4. Write PLAN.md
```

### Step 3: Generate PLAN.md

The planner uses the reference to:
- Determine migration order (layer dependencies)
- Identify which files need changes
- Write specific "What to do" instructions for each file
- Mark complex patterns with ⚠️ flags
- Generate verification commands

---

## Example References

See existing references in this directory:
- `javaee-quarkus.md` - Java EE 7/8 → Quarkus 3 (comprehensive)
- `springboot-2-to-3.md` - Spring Boot 2 → 3 (Jakarta namespace + Spring changes)
- `dotnet-framework-to-core.md` - .NET Framework 4.x → .NET 8
- `python2-to-python3.md` - Python 2.7 → Python 3.x
- `migration-phases.md` - Generic migration concepts (no specific tech stack)

---

## Testing Your Reference

1. **Place the reference** in `skill-bundle/goose-migration/references/`
2. **Reinstall migration-harness**: `cd ~/migration-harness && ./install.sh`
3. **Run detect step** on a sample project:
   ```bash
   migration-harness step detect /path/to/project
   ```
4. **Check if reference is auto-selected**:
   - Look for "reference used: <your-reference-name>" in planning output
5. **Review generated PLAN.md**:
   - Are the steps specific?
   - Are complex patterns marked with ⚠️?
   - Is the layer order correct?

---

## Contributing

When adding a new reference:
1. Copy this template
2. Fill in patterns from real-world migrations
3. Test on a sample project
4. Submit a PR with:
   - The reference file
   - A sample `PLAN.md` it generated
   - Any updates to `applies_to` logic

---

## Questions?

- **How detailed should patterns be?** Include enough detail that the planner doesn't need to read every source file. For mechanical changes (imports), a table is enough. For architectural changes (MDB → @Incoming), provide full before/after examples.

- **Should I include tool-specific commands?** Yes! Verification commands should be real commands users can copy-paste.

- **What if my migration doesn't fit the template?** The template is a guide, not a strict schema. Adapt sections as needed, but keep the core structure (order, patterns, verification).

- **Can I reference other files?** Yes! You can reference other references or skills using relative paths: `See ../skills/javaee-quarkus/SKILL.md for execution details`.
