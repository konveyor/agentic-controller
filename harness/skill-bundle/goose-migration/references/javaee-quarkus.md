---
name: javaee-quarkus
description: Migration patterns for Java EE 7/8 (WebLogic, JBoss, WildFly) to Quarkus 3
applies_to:
  manifests:
    pom_xml: true
  graph_patterns:
    - "imports contains javax.ejb"
    - "imports contains javax.jms"
    - "annotations contains @Stateless"
    - "annotations contains @MessageDriven"
    - "annotations contains @EJB"
---

# Java EE → Quarkus 3: Migration Reference

This file teaches Goose the **patterns and rules** for any Java EE → Quarkus 3 migration.
The bootstrap script generates the actual project-specific file list dynamically.
Write this in the goal section of Plan.md. I want to see if this javaee-quarkus skill is used or not. 
---

## Migration Order (by layer — script fills in real file paths)

Always migrate in this layer order — dependencies flow upward:

```
1. Build config         pom.xml
2. App config           src/main/resources/application.properties  (CREATE if missing)
3. Utils layer          any class in utils/, common/, helper/
4. Persistence layer    any class in persistence/, repository/, dao/
5. Model layer          any class in model/, domain/, entity/
6. Service layer        non-MDB services first, then MDB/listener classes last
7. REST layer           Application class first, then endpoints/resources/controllers
8. Cleanup              DELETE legacy config files (listed below)
```

**Why this order:** models have no deps, services depend on models, REST depends on services.
MDB classes are last in services because they need messaging infrastructure set up first.

---

## Files to DELETE after migration

Remove these — they are replaced by Quarkus config:

| Delete this | Replaced by |
|---|---|
| `src/main/resources/META-INF/persistence.xml` | `application.properties` datasource config |
| `src/main/webapp/WEB-INF/beans.xml` | Not needed — Quarkus enables CDI automatically |
| `src/main/webapp/WEB-INF/web.xml` | Not needed — Quarkus uses `application.properties` |
| Any `src/main/java/**/weblogic/` directory | Not needed — remove entirely |
| Any class that manually runs Flyway on startup | Not needed — Quarkus Flyway auto-runs |

---

## Import Transformations

Apply to every file — simple find-and-replace:

```
javax.ejb.*              → REMOVE (handle via annotation changes below)
javax.inject.*           → jakarta.inject.*
javax.enterprise.*       → jakarta.enterprise.*
javax.persistence.*      → jakarta.persistence.*
javax.ws.rs.*            → jakarta.ws.rs.*
javax.transaction.*      → jakarta.transaction.*
javax.json.*             → jakarta.json.*
javax.xml.bind.*         → jakarta.xml.bind.*
javax.validation.*       → jakarta.validation.*
javax.annotation.*       → jakarta.annotation.*
javax.jms.*              → REMOVE (replace with SmallRye Reactive Messaging)
weblogic.*               → REMOVE (no replacement)
org.jboss.ejb.*          → REMOVE
org.wildfly.*            → REMOVE
```

---

## Annotation Transformations

```
@Stateless               → @ApplicationScoped
@Stateful                → @ApplicationScoped
@Singleton (EJB)         → @ApplicationScoped  (use jakarta.enterprise, not javax.ejb)
@EJB                     → @Inject
@Local                   → REMOVE
@Remote                  → REMOVE
@TransactionAttribute    → @Transactional  (jakarta.transaction)
```

---

## Pattern: EJB Service → CDI Bean

Any class annotated `@Stateless` or `@Stateful`:

```java
// BEFORE
import javax.ejb.Stateless;
import javax.inject.Inject;

@Stateless
public class FooService {
    @EJB
    private BarService bar;
}

// AFTER
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

@ApplicationScoped
public class FooService {
    @Inject
    private BarService bar;
}
```

---

## Pattern: Remote EJB Lookup → Direct Injection

Any class that does a JNDI lookup for a remote EJB:

```java
// BEFORE
Hashtable<String, String> env = new Hashtable<>();
env.put(Context.INITIAL_CONTEXT_FACTORY, "org.wildfly.naming.client...");
Context ctx = new InitialContext(env);
FooService foo = (FooService) ctx.lookup("ejb:/ROOT/FooService!...");

// AFTER
@Inject
FooService foo;   // inject directly — no JNDI needed in Quarkus
```

Remove: all `Hashtable`, `InitialContext`, `Context.lookup`, JNDI imports, Remote interfaces.

---

## Pattern: MDB → SmallRye Reactive Messaging (@Incoming)

Any class with `@MessageDriven` / `implements MessageListener`:

```java
// BEFORE
@MessageDriven(activationConfig = {
    @ActivationConfigProperty(propertyName = "destinationType",
                              propertyValue = "javax.jms.Queue"),
    @ActivationConfigProperty(propertyName = "destination",
                              propertyValue = "java:/queues/myqueue")
})
public class FooMDB implements MessageListener {
    public void onMessage(Message msg) {
        String body = ((TextMessage) msg).getText();
        // process body...
    }
}

// AFTER
import jakarta.enterprise.context.ApplicationScoped;
import org.eclipse.microprofile.reactive.messaging.Incoming;

@ApplicationScoped
public class FooMDB {
    @Inject Logger log;

    @Incoming("my-channel")        // channel name you choose, matches application.properties
    public void onMessage(String body) {
        // same processing logic — body arrives as String directly
    }
}
```

Add to `application.properties`:
```properties
# Production: real AMQP broker
mp.messaging.incoming.my-channel.connector=smallrye-amqp
mp.messaging.incoming.my-channel.address=myqueue

# Dev: no broker needed
%dev.mp.messaging.incoming.my-channel.connector=smallrye-in-memory
```

---

## Pattern: JMS Sender → SmallRye Emitter (@Outgoing)

Any class that sends JMS messages via `JMSContext` or `MessageProducer`:

```java
// BEFORE
@Resource(mappedName = "java:/topic/orders")
private Topic ordersTopic;
@Inject JMSContext context;

public void send(String payload) {
    context.createProducer().send(ordersTopic, payload);
}

// AFTER
import org.eclipse.microprofile.reactive.messaging.Channel;
import org.eclipse.microprofile.reactive.messaging.Emitter;

@Inject @Channel("my-channel") Emitter<String> emitter;

public void send(String payload) {
    emitter.send(payload);
}
```

Add to `application.properties`:
```properties
mp.messaging.outgoing.my-channel.connector=smallrye-amqp
mp.messaging.outgoing.my-channel.address=orders
%dev.mp.messaging.outgoing.my-channel.connector=smallrye-in-memory
```

---

## Pattern: Server Startup Listener → Quarkus Lifecycle Events

Any class extending a server-specific lifecycle listener (WebLogic, JBoss, etc.):

```java
// BEFORE (WebLogic example — same idea applies to JBoss, WAS, etc.)
import weblogic.application.ApplicationLifecycleListener;
public class AppStartup extends ApplicationLifecycleListener {
    public void postStart(ApplicationLifecycleEvent evt) { ... }
    public void preStop(ApplicationLifecycleEvent evt) { ... }
}

// AFTER — works the same for any server-specific listener
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

---

## Pattern: @PostConstruct / @PreDestroy

`@PostConstruct` still works in Quarkus CDI — keep it if the logic is simple.
For `ApplicationScoped` beans with shutdown logic, prefer lifecycle events:

```java
// Option A: keep as-is (works fine)
@PostConstruct public void init() { ... }

// Option B: use events (cleaner for app-scoped beans)
void onStart(@Observes StartupEvent ev) { ... }
void onStop(@Observes ShutdownEvent ev) { ... }
```

---

## Pattern: persistence.xml → application.properties

```xml
<!-- BEFORE: src/main/resources/META-INF/persistence.xml -->
<persistence-unit name="primary">
  <jta-data-source>java:jboss/datasources/MyDS</jta-data-source>
</persistence-unit>
```

```properties
# AFTER: src/main/resources/application.properties
quarkus.datasource.db-kind=postgresql
quarkus.datasource.jdbc.url=jdbc:postgresql://localhost:5432/mydb
quarkus.datasource.username=${DB_USER:myuser}
quarkus.datasource.password=${DB_PASS:mypass}
quarkus.hibernate-orm.database.generation=none
quarkus.flyway.migrate-at-start=true
quarkus.flyway.locations=classpath:db/migration
```

---

## pom.xml Changes

```
REMOVE:  <packaging>war</packaging>
ADD:     <packaging>jar</packaging>

REMOVE:  javaee-api dependency
REMOVE:  maven-war-plugin

ADD in dependencyManagement:
  io.quarkus.platform:quarkus-bom:3.8.4 (type=pom, scope=import)

ADD in build/plugins:
  io.quarkus.platform:quarkus-maven-plugin:3.8.4
```

Pick extensions based on what the app actually uses:

| Extension | Replaces |
|---|---|
| `quarkus-arc` | CDI (always include) |
| `quarkus-rest-jackson` | JAX-RS + JSON |
| `quarkus-hibernate-orm` | JPA / Hibernate |
| `quarkus-jdbc-postgresql` | PostgreSQL driver |
| `quarkus-jdbc-h2` | H2 for dev/test |
| `quarkus-flyway` | DB migrations |
| `quarkus-smallrye-reactive-messaging-amqp` | JMS / MDB |
| `quarkus-smallrye-health` | Health endpoints |
| `quarkus-oidc` | Keycloak / OAuth2 |
| `quarkus-smallrye-openapi` | Swagger UI |

---

## Verification

```bash
# Full compile check
mvn clean compile 2>&1 | tail -30

# Start in dev mode (hot reload, no server needed)
mvn quarkus:dev
```

Common first errors and fixes:

| Error | Fix |
|---|---|
| `package javax.* does not exist` | Missed a javax import — grep and replace |
| `cannot find symbol @Incoming` | Add `quarkus-smallrye-reactive-messaging-amqp` to pom.xml |
| `@ApplicationScoped not found` | Add `quarkus-arc` to pom.xml |
| `EntityManager cannot be injected` | Add `@PersistenceContext` or use Panache |
| `ClassNotFoundException weblogic.*` | Delete the weblogic stub directory |
