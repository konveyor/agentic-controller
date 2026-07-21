# Config Map: Java EE XML → Quarkus application.properties

Use this table when converting configuration in the App Config phase.

## persistence.xml → application.properties

| persistence.xml | application.properties |
|---|---|
| `<jta-data-source>java:jboss/datasources/PostgresDS</jta-data-source>` | `quarkus.datasource.db-kind=postgresql` |
| JDBC URL in datasource config | `quarkus.datasource.jdbc.url=jdbc:postgresql://host:5432/db` |
| Username in datasource config | `quarkus.datasource.username=user` |
| Password in datasource config | `quarkus.datasource.password=pass` |
| `<property name="hibernate.dialect" value="..."/>` | Auto-detected from `db-kind` |
| `<property name="hibernate.hbm2ddl.auto" value="update"/>` | `quarkus.hibernate-orm.database.generation=update` |
| `<property name="hibernate.show_sql" value="true"/>` | `quarkus.hibernate-orm.log.sql=true` |
| `<property name="hibernate.format_sql" value="true"/>` | `quarkus.hibernate-orm.log.format-sql=true` |
| `<property name="hibernate.default_schema" value="myschema"/>` | `quarkus.hibernate-orm.database.default-schema=myschema` |

## web.xml → application.properties

| web.xml | application.properties |
|---|---|
| `<context-param><param-name>...</param-name>` | `app.custom-param=value` (use `@ConfigProperty`) |
| `<session-config><session-timeout>30</session-timeout>` | Not applicable — Quarkus is stateless by default |
| `<welcome-file-list>` | Not applicable — use `@Path("/")` |
| `<error-page>` | Use JAX-RS `ExceptionMapper` |
| `<servlet-mapping>` | Use JAX-RS `@Path` |
| `<filter-mapping>` | Use JAX-RS `@Provider` + `ContainerRequestFilter` |
| `<security-constraint>` | `quarkus.http.auth.policy.*` or `@RolesAllowed` |

## Server-specific datasource XML → application.properties

### WebLogic (weblogic-application.xml / -ds.xml)

| WebLogic Config | application.properties |
|---|---|
| `<jdbc-data-source><name>MyDS</name>` | `quarkus.datasource.db-kind=postgresql` |
| `<url>jdbc:oracle:thin:@host:1521:orcl</url>` | `quarkus.datasource.jdbc.url=jdbc:oracle:...` |
| `<driver-name>oracle.jdbc.OracleDriver</driver-name>` | Auto-detected from URL |
| `<initial-capacity>5</initial-capacity>` | `quarkus.datasource.jdbc.min-size=5` |
| `<max-capacity>20</max-capacity>` | `quarkus.datasource.jdbc.max-size=20` |

### JBoss / WildFly (standalone.xml / *-ds.xml)

| JBoss Config | application.properties |
|---|---|
| `<datasource jndi-name="java:jboss/datasources/MyDS">` | `quarkus.datasource.db-kind=...` |
| `<connection-url>jdbc:postgresql://...</connection-url>` | `quarkus.datasource.jdbc.url=...` |
| `<min-pool-size>5</min-pool-size>` | `quarkus.datasource.jdbc.min-size=5` |
| `<max-pool-size>20</max-pool-size>` | `quarkus.datasource.jdbc.max-size=20` |

## Messaging config

| Java EE | application.properties |
|---|---|
| `@ActivationConfigProperty(destination="java:/queues/X")` | `mp.messaging.incoming.x.address=X` |
| JMS ConnectionFactory JNDI | `mp.messaging.incoming.x.connector=smallrye-amqp` |
| Queue vs Topic | `mp.messaging.incoming.x.durable=true` (for topics) |

## Quarkus dev profiles

Use `%dev.` prefix for local development, `%test.` for test:

```properties
# Production
quarkus.datasource.db-kind=postgresql
quarkus.datasource.jdbc.url=jdbc:postgresql://prod-host:5432/mydb

# Dev — H2 in-memory
%dev.quarkus.datasource.db-kind=h2
%dev.quarkus.datasource.jdbc.url=jdbc:h2:mem:devdb

# Test
%test.quarkus.datasource.db-kind=h2
%test.quarkus.datasource.jdbc.url=jdbc:h2:mem:testdb
```
