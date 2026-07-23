# Phase: Lifecycle

Replace application server lifecycle listeners with Quarkus lifecycle events.

## Steps

1. Identify classes extending server-specific lifecycle listeners
2. Replace with Quarkus `@Observes` events

### Server lifecycle → Quarkus events

```java
// Before (WebLogic example — same pattern applies to JBoss, WAS, etc.)
import weblogic.application.ApplicationLifecycleListener;
import weblogic.application.ApplicationLifecycleEvent;

public class AppStartup extends ApplicationLifecycleListener {
    public void postStart(ApplicationLifecycleEvent evt) { ... }
    public void preStop(ApplicationLifecycleEvent evt) { ... }
}

// After
import io.quarkus.runtime.StartupEvent;
import io.quarkus.runtime.ShutdownEvent;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;

@ApplicationScoped
public class AppStartup {
    void onStart(@Observes StartupEvent ev) { ... }
    void onStop(@Observes ShutdownEvent ev) { ... }
}
```

### @PostConstruct / @PreDestroy

`@PostConstruct` still works in Quarkus CDI — keep it if the logic is simple. For `ApplicationScoped` beans with shutdown logic, prefer lifecycle events:

```java
// Option A: keep as-is (works fine)
@PostConstruct public void init() { ... }

// Option B: use events (cleaner for app-scoped beans)
void onStart(@Observes StartupEvent ev) { ... }
void onStop(@Observes ShutdownEvent ev) { ... }
```

### Flyway / DB init on startup

If the app has a class that manually runs Flyway or Liquibase on startup, delete it. Quarkus handles this automatically:

```properties
# application.properties
quarkus.flyway.migrate-at-start=true
```

### Import removals

Remove all server-specific imports:
```
weblogic.*               → REMOVE
org.jboss.ejb.*          → REMOVE
org.wildfly.*            → REMOVE
```

3. Run the build gate

## Build gate

Run `mvn compile`. Common issues:
- `ClassNotFoundException weblogic.*` — delete the weblogic stub directory
- Missing Quarkus lifecycle event imports
