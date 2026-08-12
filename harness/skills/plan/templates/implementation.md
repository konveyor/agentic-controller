# Implementation Plan Output Format

The plan stage writes `.konveyor/implementation.md` — step-by-step migration
instructions the execute stage follows.

## Schema

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

## Step Fields

| Field | Description |
|---|---|
| `Phase` | Must match a domain skill phase name |
| `File` | Exact path from repo root (from graph.json) |
| `Action` | One of CREATE, MODIFY, or DELETE |
| `What to do` | Specific transformation instructions |
| `Why` | What pattern is being changed and why |
| `Depends on` | Step numbers this step depends on, or "none" |
| `Verify` | How to confirm this step is complete |

## Step Detail Levels

### Mechanical (simple find-replace)

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

### Complex (structural/architectural changes — use domain skill patterns)

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

### CREATE (new file)

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

### DELETE (remove file)

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

## Rules for Writing Steps

1. **Phase on every step** — every step must have a `Phase:` matching a domain skill phase
2. **One file per step** — never combine two files in one step
3. **Exact paths** — use real paths from graph.json, not placeholders
4. **Dependency order** — steps that others depend on come first
5. **Phase order** — follow the domain skill's phase ordering
6. **Hard steps flagged** — add `COMPLEX:` prefix for structural changes
7. **DELETE steps last** — after all modifications are done
