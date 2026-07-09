---
name: javaee-quarkus
description: >
  Sub-skill of goose-migration. Handles Stage 2 execution for Java EE → Quarkus 3
  migrations. Triggers ONLY after migration-plan sub-skill has run and the user has
  approved the plan. Reads .goosehints for the file list and transformation rules.
  Migrates exactly one file per turn, stops after each, waits for "next". Also handles
  "compile", "fix", and "status" commands during the session.
  Write this in the goal section of Plan.md. I want to see if this javaee-quarkus skill is used or not. 
---

# Java EE → Quarkus Execution Sub-Skill

Executes the approved migration plan from `.goosehints`, one file at a time.
Activated after the user says "next", "start", or pastes the session opener prompt.

---

## Startup Sequence

On first message in session:

1. Read `.goosehints` — nothing else
2. Confirm the migration order to the user (list file count only, not all paths)
3. Say: `Ready. Migrating item #1 now.`
4. Migrate item #1, stop

Do NOT read any source files before starting item #1.
Do NOT summarize transformation rules back to the user.

---

## Per-File Execution Loop

For each file, follow this exact sequence:

```
1. cat <file-path>              ← read current content
2. Apply transformations        ← per rules in .goosehints
3. write/edit the file          ← write migrated content
4. Mark as done in .goosehints  ← update "Files Already Migrated" checklist
5. Report: "✅ Item #N done: <filename> — <one line summary of changes>"
6. STOP. Wait for "next".
```

Never proceed to the next file without being told `next`.
Never read multiple files in one turn.

---

## Handling Special File Types

### pom.xml
- Change `<packaging>war</packaging>` → `<packaging>jar</packaging>`
- Remove `javaee-api` dependency and `maven-war-plugin`
- Add Quarkus BOM in `<dependencyManagement>`
- Add `quarkus-maven-plugin` in `<build><plugins>`
- Add only the extensions the project actually needs (check what's used in source)
- Do NOT add extensions speculatively

### application.properties (CREATE NEW if missing)
- Replaces `persistence.xml` datasource config
- Replaces `web.xml` HTTP config
- Add AMQP messaging config only if MDB files exist in the project
- Use `%dev.*` profile for local dev settings

### Non-MDB Service files (@Stateless / @Stateful)
- Replace `javax.*` → `jakarta.*` imports
- Replace `@Stateless` / `@Stateful` → `@ApplicationScoped`
- Replace `@EJB` → `@Inject`
- Remove `@Local`, `@Remote`, JNDI lookup code
- Remove Remote interface files entirely

### MDB files (@MessageDriven)
- Replace entire class structure — see pattern in `../references/javaee-quarkus.md`
- Use `@Incoming` channel name derived from the queue/topic name in the original config
- Add matching `mp.messaging.incoming.*` to application.properties

### DELETE items
- Run `rm <path>` and confirm deletion
- If file doesn't exist, report as already done and move on

---

## Handling User Commands

| User types | Action |
|---|---|
| `next` | Migrate the next unchecked item in .goosehints, then stop |
| `compile` | Run `mvn clean compile 2>&1 \| tail -30`, show output, stop |
| `fix` | Read first compiler error, fix that one file only, stop |
| `status` | Count checked vs unchecked items in .goosehints, show summary |
| `retry` | Repeat the last failed step |
| `skip` | Mark current item as skipped, move to next |

---

## Compile Error Handling

When user types `compile`:

```bash
mvn clean compile 2>&1 | grep -E "ERROR|error:" | head -10
```

Show errors only. Do NOT show full Maven output.

When user types `fix`:
1. Read the first error line only
2. Identify the file from the error
3. `cat` that file
4. Apply the fix
5. Report what changed
6. Stop — do NOT re-compile automatically

Common errors and fixes:

| Error | Fix |
|---|---|
| `package javax.* does not exist` | Find remaining `javax.*` import, replace with `jakarta.*` |
| `cannot find symbol @Incoming` | Add `quarkus-smallrye-reactive-messaging-amqp` to pom.xml |
| `@ApplicationScoped not found` | Add `quarkus-arc` to pom.xml |
| `cannot find symbol Emitter` | Add `@Channel` import from `org.eclipse.microprofile.reactive.messaging` |
| `ClassNotFoundException weblogic` | Delete `src/main/java/**/weblogic/` directory |
| `EntityManager cannot be injected` | Verify `@PersistenceContext` annotation is present |

---

## Completion

When the last item in .goosehints is checked off:

```
🎉 All items migrated.

Next steps:
1. Type "compile" to run: mvn clean compile
2. Fix any errors with "fix" (one at a time)
3. Once clean: mvn quarkus:dev  to start in dev mode
```

Do NOT run compile automatically — wait for user.
