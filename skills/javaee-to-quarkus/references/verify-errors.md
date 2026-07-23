# Common Compilation Errors and Fixes

Use this table during the build gate checks after each phase.

## Build Config Phase

| Error | Cause | Fix |
|---|---|---|
| `package javax.ejb does not exist` | Java EE umbrella dependency removed but EJB code not yet migrated | Expected — will fix in EJB-to-CDI phase. If blocking, add `quarkus-arc` temporarily |
| `package javax.jms does not exist` | JMS dependency removed but MDB code not yet migrated | Expected — will fix in Messaging phase |
| `Duplicate dependency: hibernate-core` | Both explicit Hibernate and `quarkus-hibernate-orm` | Remove explicit `hibernate-core` — Quarkus manages it |
| `Plugin 'maven-war-plugin' not found` | Plugin removed but still referenced in profile | Remove all `maven-war-plugin` references from profiles |
| `Cannot resolve io.quarkus.platform:quarkus-bom` | Missing Quarkus repository or BOM not in `<dependencyManagement>` | Add BOM to `<dependencyManagement>` |

## EJB-to-CDI Phase

| Error | Cause | Fix |
|---|---|---|
| `cannot find symbol: class Stateless` | `@Stateless` import removed but annotation still present | Replace `@Stateless` with `@ApplicationScoped` |
| `cannot find symbol: class EJB` | `@EJB` import removed but annotation still present | Replace `@EJB` with `@Inject` |
| `package javax.ejb does not exist` | Leftover `import javax.ejb.*` | Replace with `import jakarta.enterprise.context.*` / `import jakarta.inject.*` |
| `incompatible types: no instance of ... FooRemote` | Class still implements deleted `@Remote` interface | Remove `implements FooRemote` clause |
| `cannot find symbol: class InitialContext` | JNDI lookup code not fully removed | Replace with `@Inject` |
| `Unsatisfied dependency for type X` | Missing `@ApplicationScoped` on the injected bean | Add scope annotation to the bean class |

## App Config Phase

| Error | Cause | Fix |
|---|---|---|
| `Unable to find a JDBC driver` | Missing JDBC extension in pom.xml | Add `quarkus-jdbc-postgresql` (or appropriate driver) |
| `Model classes are defined for the default persistence unit but no datasource` | No datasource config in application.properties | Add `quarkus.datasource.*` properties |
| `Could not find a suitable driver` | Wrong `db-kind` value | Check `quarkus.datasource.db-kind` matches the JDBC URL |

## Messaging Phase

| Error | Cause | Fix |
|---|---|---|
| `cannot find symbol: class Incoming` | Missing reactive messaging extension | Add `quarkus-smallrye-reactive-messaging-amqp` to pom.xml |
| `cannot find symbol: class Emitter` | Wrong import | Use `org.eclipse.microprofile.reactive.messaging.Emitter` |
| `SRMSG00018: No channel found for name` | Channel name mismatch between `@Incoming("x")` and `mp.messaging.incoming.y` | Make channel names match |
| `SRMSG00015: Unable to connect` | AMQP broker not running in dev mode | Add `%dev.mp.messaging.incoming.*.connector=smallrye-in-memory` |

## Lifecycle Phase

| Error | Cause | Fix |
|---|---|---|
| `package weblogic does not exist` | WebLogic imports still present | Delete weblogic import lines and stub directories |
| `cannot find symbol: class ApplicationLifecycleListener` | WebLogic listener base class | Replace with Quarkus `@Observes StartupEvent` / `ShutdownEvent` |
| `package org.jboss.ejb does not exist` | JBoss-specific imports | Remove — use standard CDI instead |

## General

| Error | Cause | Fix |
|---|---|---|
| `package javax.inject does not exist` | javax→jakarta rename not applied | Replace `javax.inject` → `jakarta.inject` |
| `package javax.persistence does not exist` | javax→jakarta rename not applied | Replace `javax.persistence` → `jakarta.persistence` |
| `package javax.ws.rs does not exist` | javax→jakarta rename not applied | Replace `javax.ws.rs` → `jakarta.ws.rs` |
| `package javax.annotation does not exist` | javax→jakarta rename not applied | Replace `javax.annotation` → `jakarta.annotation` |
| `beans.xml: Invalid content` | Leftover beans.xml with old schema | Delete beans.xml — Quarkus doesn't need it |
