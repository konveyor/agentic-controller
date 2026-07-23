# Phase: App Config

Replace Java EE XML configuration with Quarkus application.properties.

## Steps

1. Read `references/config-map.md`

### Replace persistence.xml with application.properties

```xml
<!-- Before: src/main/resources/META-INF/persistence.xml -->
<persistence-unit name="primary">
    <jta-data-source>java:jboss/datasources/MyDS</jta-data-source>
</persistence-unit>
```

```properties
# After: src/main/resources/application.properties
quarkus.datasource.db-kind=postgresql
quarkus.datasource.jdbc.url=jdbc:postgresql://localhost:5432/mydb
quarkus.datasource.username=${DB_USER:myuser}
quarkus.datasource.password=${DB_PASS:mypass}
quarkus.hibernate-orm.database.generation=none
quarkus.flyway.migrate-at-start=true
quarkus.flyway.locations=classpath:db/migration
```

Use `%dev.*` profile prefix for local dev settings:

```properties
%dev.quarkus.datasource.db-kind=h2
%dev.quarkus.datasource.jdbc.url=jdbc:h2:mem:test
```

### Remove legacy XML config files

Delete these — they are replaced by Quarkus configuration:

| Delete this | Replaced by |
|---|---|
| `src/main/resources/META-INF/persistence.xml` | `application.properties` datasource config |
| `src/main/webapp/WEB-INF/beans.xml` | Not needed — Quarkus enables CDI automatically |
| `src/main/webapp/WEB-INF/web.xml` | Not needed — Quarkus uses `application.properties` |

### Add messaging config (only if MDB files exist)

If the project has `@MessageDriven` classes, add AMQP messaging config:

```properties
# Production: real AMQP broker
mp.messaging.incoming.my-channel.connector=smallrye-amqp
mp.messaging.incoming.my-channel.address=myqueue

# Dev: no broker needed
%dev.mp.messaging.incoming.my-channel.connector=smallrye-in-memory
```

Channel names are defined in the messaging phase.

2. Run the build gate

## Build gate

Run `mvn compile`. Configuration errors surface as startup failures — also run `mvn quarkus:dev` briefly to verify:
- Datasource connects
- Hibernate/JPA initializes
- No missing config properties
