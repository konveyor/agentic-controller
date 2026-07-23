# Phase: EJB to CDI

Replace EJB annotations with CDI managed beans. Remove Remote/Local interfaces and JNDI lookups.

## Steps

1. Read `references/annotation-map.md`
2. Process all Java source files

### Annotation replacements

Apply to every EJB class:

| Before | After |
|---|---|
| `@Stateless` | `@ApplicationScoped` |
| `@Stateful` | `@ApplicationScoped` |
| `@Singleton` (javax.ejb) | `@ApplicationScoped` (jakarta.enterprise) |
| `@EJB` | `@Inject` |
| `@Local` | REMOVE |
| `@Remote` | REMOVE |
| `@LocalBean` | REMOVE |
| `@TransactionAttribute` | `@Transactional` (jakarta.transaction) |

### Import replacements

```
javax.ejb.*              → REMOVE (handled via annotation changes above)
javax.inject.*           → jakarta.inject.*
javax.enterprise.*       → jakarta.enterprise.*
javax.persistence.*      → jakarta.persistence.*
javax.ws.rs.*            → jakarta.ws.rs.*
javax.transaction.*      → jakarta.transaction.*
javax.json.*             → jakarta.json.*
javax.xml.bind.*         → jakarta.xml.bind.*
javax.validation.*       → jakarta.validation.*
javax.annotation.*       → jakarta.annotation.*
```

### Example: EJB service → CDI bean

```java
// Before
import javax.ejb.Stateless;
import javax.ejb.EJB;

@Stateless
public class OrderService {
    @EJB
    private InventoryService inventory;
}

// After
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

@ApplicationScoped
public class OrderService {
    @Inject
    private InventoryService inventory;
}
```

### Remove Remote interfaces

Delete any `@Remote` interface files. Update classes that implemented them to remove the `implements` clause.

### Replace JNDI lookups with injection

```java
// Before
Hashtable<String, String> env = new Hashtable<>();
env.put(Context.INITIAL_CONTEXT_FACTORY, "org.wildfly.naming.client...");
Context ctx = new InitialContext(env);
FooService foo = (FooService) ctx.lookup("ejb:/ROOT/FooService!...");

// After
@Inject
FooService foo;
```

Remove all `Hashtable`, `InitialContext`, `Context.lookup`, JNDI imports.

3. Run the build gate

## Build gate

Run `mvn compile`. Common issues:
- Missing `quarkus-arc` extension in pom.xml
- Dangling references to deleted Remote interfaces
- JNDI lookup code still present
