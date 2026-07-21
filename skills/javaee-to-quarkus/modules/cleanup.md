# Phase: Cleanup and Verification

Delete legacy files, remove remaining server stubs, verify no javax.* EE imports remain.

## Steps

### 1. Delete legacy directories

Remove any server-specific stub directories:
```bash
find . -type d -name "weblogic" -exec rm -rf {} +
```

### 2. Delete legacy config files

If not already deleted in the app-config phase:
- `src/main/resources/META-INF/persistence.xml`
- `src/main/webapp/WEB-INF/beans.xml`
- `src/main/webapp/WEB-INF/web.xml`
- Any `src/main/java/**/weblogic/` directories

### 3. Verify no javax.* EE imports remain

```bash
grep -rn "import javax\." --include="*.java" src/
```

All `javax.ejb`, `javax.jms`, `javax.inject`, `javax.enterprise`, `javax.persistence`, `javax.ws.rs`, `javax.transaction`, `javax.annotation` imports should be gone or replaced with `jakarta.*`.

**Leave unchanged** (Java SE packages): `javax.sql`, `javax.crypto`, `javax.net`, `javax.naming`, `javax.xml.parsers`, `javax.xml.transform`.

### 4. Verify no JNDI lookups remain

```bash
grep -rn "InitialContext\|Context.lookup\|INITIAL_CONTEXT_FACTORY" --include="*.java" src/
```

Should find zero results.

### 5. Verify no EJB annotations remain

```bash
grep -rn "@Stateless\|@Stateful\|@MessageDriven\|@EJB\|@Local\b\|@Remote\b" --include="*.java" src/
```

Should find zero results.

### 6. Final build and test

```bash
mvn clean compile
mvn test
mvn quarkus:dev
```

### 7. Report to user

- List all changes made across all phases
- Flag any remaining javax.* imports that could not be migrated
- Note any MDB classes that need manual channel configuration
- Report test results (failures are expected and documented, not fixed)
