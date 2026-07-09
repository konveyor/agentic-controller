# Migration References — Summary

We've created a **language-agnostic, framework-agnostic** reference system for migration planning.

## What We Built

### 1. README.md (Writing Guide)
**Location**: `skill-bundle/goose-migration/references/README.md`

Complete guide for end users explaining:
- ✅ What a reference file is
- ✅ When to create one
- ✅ Template structure (frontmatter, patterns, verification)
- ✅ How to test references
- ✅ Auto-selection logic (`applies_to` rules)

**Key sections**:
- Reference file structure template
- Frontmatter fields (`name`, `description`, `applies_to`)
- Pattern catalog format (before/after examples)
- How the planner uses references

---

### 2. Language/Framework References (4 Complete Examples)

| Reference | Source → Target | Size | Patterns |
|-----------|----------------|------|----------|
| `javaee-quarkus.md` | Java EE 7/8 → Quarkus 3 | 9.7KB | EJB→CDI, MDB→@Incoming, JNDI removal |
| `springboot-2-to-3.md` | Spring Boot 2.x → 3.x | 16KB | Jakarta namespace, Security config, Hibernate 6 |
| `dotnet-framework-to-core.md` | .NET Framework 4.x → .NET 8 | 25KB | System.Web→AspNetCore, Web.config→appsettings, EF6→EF Core |
| `python2-to-python3.md` | Python 2.7 → Python 3.x | 15KB | print(), xrange→range, unicode→str, urllib2 |

---

### 3. Frontmatter Auto-Selection

Each reference has frontmatter for auto-detection:

**Example (Java EE → Quarkus)**:
```yaml
---
name: javaee-quarkus
description: Migration patterns for Java EE 7/8 to Quarkus 3
applies_to:
  manifests:
    pom_xml: true
  graph_patterns:
    - "imports contains javax.ejb"
    - "annotations contains @MessageDriven"
---
```

**How it works**:
1. Detect step produces `detect.json` with manifests and graph stats
2. Planning step checks each reference's `applies_to` rules
3. Auto-selects matching reference (e.g., pom.xml + javax.ejb → javaee-quarkus.md)
4. Planner reads the reference and uses patterns to generate PLAN.md

---

## What Each Reference Provides

Every reference follows the same structure:

### 1. Migration Order (Layer Dependencies)
```
1. Build config (pom.xml, package.json, .csproj)
2. App config (application.properties, appsettings.json)
3. Utilities
4. Data layer
5. Business logic
6. API layer
7. Cleanup
```

**Why**: Ensures correct dependency order. Models don't depend on services, so migrate models first.

---

### 2. Import/Package Transformations
Simple find-and-replace mappings:

**Spring Boot 2→3**:
```
javax.persistence.* → jakarta.persistence.*
javax.validation.*  → jakarta.validation.*
```

**Python 2→3**:
```
urllib2 → urllib.request
Queue   → queue
```

**`.NET Framework→.NET 8`**:
```
System.Web.Mvc → Microsoft.AspNetCore.Mvc
```

---

### 3. Pattern Catalog (Before/After Examples)

For complex structural changes, show full examples:

**Example: Java EE MDB → Quarkus @Incoming**
```java
// BEFORE
@MessageDriven(...)
public class OrderMDB implements MessageListener {
    public void onMessage(Message msg) { ... }
}

// AFTER
@ApplicationScoped
public class OrderMDB {
    @Incoming("orders")
    public void onMessage(String body) { ... }
}
```

---

### 4. Files to DELETE/CREATE
**Java EE → Quarkus**:
- DELETE: `persistence.xml`, `web.xml`, `beans.xml`
- CREATE: `application.properties`

**`.NET Framework → .NET 8`**:
- DELETE: `Web.config`, `Global.asax`, `packages.config`
- CREATE: `appsettings.json`, `Program.cs`

---

### 5. Build File Changes
Specific instructions for `pom.xml`, `package.json`, `.csproj`, etc.

**Example (Spring Boot)**:
```xml
<!-- CHANGE parent version -->
<parent>
  <artifactId>spring-boot-starter-parent</artifactId>
  <version>3.2.0</version>  <!-- Was 2.7.x -->
</parent>

<!-- CHANGE Java version -->
<java.version>17</java.version>  <!-- Was 8 or 11 -->
```

---

### 6. Verification Commands
Copy-paste commands to verify migration:

**Python 2→3**:
```bash
# Check for print statements
grep -rn "print " . --include="*.py" | grep -v "print(" | wc -l  # Should be 0

# Check for xrange
grep -rn "xrange" . --include="*.py" | wc -l  # Should be 0
```

---

### 7. Notes / Gotchas
Common pitfalls and edge cases:

- **Spring Boot 3**: Java 17 is REQUIRED (not optional)
- **.NET 8**: Web Forms have no direct equivalent (use Blazor/Razor Pages)
- **Python 3**: Integer division changed (`5/2 = 2.5`, not `2`)

---

## How It Works in Practice

### Step 1: User runs migration-harness
```bash
migration-harness ~/coolstore "Migrate this Java EE app to Quarkus 3"
```

### Step 2: Detect step builds graph
```
── Step 1/5 — Detect ──
✓ 1a. manifests: pom=true pkg=false ...
✓ 1b. graph: 4275 nodes, 8386 edges, 613 communities
✓ Step 1/5 complete → detect.json + graph.json
```

**Output**: `detect.json`
```json
{
  "manifests": {"pom_xml": true},
  "graph": {"nodes": 4275, "edges": 8386}
}
```

### Step 3: Planning step auto-selects reference

```bash
# Step-plan.sh checks each reference's applies_to:
if detect.json.manifests.pom_xml == true
   AND graph contains "imports contains javax.ejb"
   THEN select references/javaee-quarkus.md
```

### Step 4: Planner reads reference and generates PLAN.md

**Inputs**:
- `detect.json` (manifests, file counts)
- `graph.json` (code structure, communities, god nodes)
- `references/javaee-quarkus.md` (patterns, order, transformations)

**Output**: `PLAN.md` (like the coolstore example we saw)
- 34 steps with specific file paths
- Complex patterns marked with ⚠️
- Layer-ordered (build → config → models → services → API → cleanup)

---

## Language/Framework Coverage

| Language/Framework | Reference | Status |
|--------------------|-----------|--------|
| Java EE → Quarkus | `javaee-quarkus.md` | ✅ Complete |
| Spring Boot 2 → 3 | `springboot-2-to-3.md` | ✅ Complete |
| .NET Framework → .NET 8 | `dotnet-framework-to-core.md` | ✅ Complete |
| Python 2 → 3 | `python2-to-python3.md` | ✅ Complete |
| React Class → Hooks | `react-class-to-hooks.md` | ⏳ TODO |
| Node.js 16 → 20 | `nodejs-16-to-20.md` | ⏳ TODO |
| Go 1.16 → 1.22 | `go-116-to-122.md` | ⏳ TODO |
| Rails 6 → 7 | `rails-6-to-7.md` | ⏳ TODO |

---

## Adding New References

### 1. Copy the template from README.md
```bash
cd skill-bundle/goose-migration/references
cp README.md my-new-reference.md
```

### 2. Fill in the sections
- Frontmatter (`name`, `description`, `applies_to`)
- Migration order
- Import transformations
- Pattern catalog (before/after examples)
- Verification commands

### 3. Test it
```bash
cd ~/migration-harness
./install.sh  # Installs the new reference
migration-harness step detect /path/to/test-project
migration-harness step plan /path/to/test-project "Migrate to X"
```

### 4. Check if auto-selected
Look for:
```
✓ 2e2. reference used: my-new-reference
```

---

## Key Benefits

### 1. Language/Framework Agnostic
The template works for ANY migration:
- JVM (Java, Kotlin, Scala)
- .NET (C#, F#, VB.NET)
- Dynamic (Python, Ruby, JavaScript, TypeScript)
- Systems (Go, Rust, C++)

### 2. LLM Makes the Judgment
The planner (goose + LLM) decides:
- Which reference to use (based on detect.json + graph patterns)
- Which files need migration (based on graph communities)
- Which patterns apply to which files (based on imports/annotations)
- What order to follow (based on layer dependencies)

You just provide the knowledge (patterns, transformations). The LLM does the reasoning.

### 3. Reusable Across Projects
Write the reference once, use it for ALL projects of that type:
- One `javaee-quarkus.md` works for WebLogic, JBoss, WildFly, GlassFish
- One `springboot-2-to-3.md` works for all Spring Boot 2.x apps
- One `dotnet-framework-to-core.md` works for MVC, Web API, Web Forms (with notes)

### 4. Easy to Extend
Community can contribute references:
- Django 3 → 4
- React 17 → 18
- Angular 15 → 16
- Vue 2 → 3

Just follow the template in README.md.

---

## Next Steps

1. **Test the existing references** on real projects
2. **Add more references** (React, Node.js, Go, Rails)
3. **Improve auto-selection logic** (better pattern matching)
4. **Create reference validator** (script to check reference format)

---

## Files Created

```
skill-bundle/goose-migration/references/
├── README.md                        ← 8.1KB - Writing guide
├── javaee-quarkus.md                ← 9.7KB - Java EE → Quarkus
├── springboot-2-to-3.md             ← 16KB  - Spring Boot 2 → 3
├── dotnet-framework-to-core.md      ← 25KB  - .NET Framework → .NET 8
├── python2-to-python3.md            ← 15KB  - Python 2 → 3
└── migration-phases.md              ← 4.3KB - Generic concepts (existing)
```

Total: **~78KB of migration knowledge** covering 4 major technology stacks.
