# Dependency Map: Java EE → Quarkus Extensions

Use this table when updating `pom.xml` in the Build Config phase.

| Java EE Dependency | Quarkus Extension | Notes |
|---|---|---|
| `javax:javaee-api` | REMOVE | Replaced by individual extensions below |
| `javax:javaee-web-api` | REMOVE | Replaced by individual extensions below |
| CDI (built into javaee-api) | `io.quarkus:quarkus-arc` | Always include — Quarkus CDI engine |
| JAX-RS (built into javaee-api) | `io.quarkus:quarkus-rest` | RESTEasy Reactive |
| JAX-RS + JSON | `io.quarkus:quarkus-rest-jackson` | Adds JSON serialization |
| JPA / Hibernate | `io.quarkus:quarkus-hibernate-orm` | |
| JPA + Panache | `io.quarkus:quarkus-hibernate-orm-panache` | Optional: active record pattern |
| Bean Validation | `io.quarkus:quarkus-hibernate-validator` | |
| JTA / Transactions | `io.quarkus:quarkus-narayana-jta` | `@Transactional` support |
| JMS / MDB | `io.quarkus:quarkus-smallrye-reactive-messaging-amqp` | For AMQP brokers |
| JMS (Kafka) | `io.quarkus:quarkus-smallrye-reactive-messaging-kafka` | For Kafka |
| JSON-P | `io.quarkus:quarkus-jsonp` | |
| JSON-B | `io.quarkus:quarkus-jsonb` | Or use Jackson instead |
| JAXB | `io.quarkus:quarkus-jaxb` | XML binding |
| JAX-WS (SOAP) | `io.quarkiverse.cxf:quarkus-cxf` | Quarkiverse extension |
| `org.hibernate:hibernate-core` | `io.quarkus:quarkus-hibernate-orm` | Quarkus manages version |
| `org.postgresql:postgresql` | `io.quarkus:quarkus-jdbc-postgresql` | |
| `com.h2database:h2` | `io.quarkus:quarkus-jdbc-h2` | Dev/test only |
| `mysql:mysql-connector-java` | `io.quarkus:quarkus-jdbc-mysql` | |
| `org.flywaydb:flyway-core` | `io.quarkus:quarkus-flyway` | |
| `org.liquibase:liquibase-core` | `io.quarkus:quarkus-liquibase` | |
| `org.eclipse.microprofile.*` | `io.quarkus:quarkus-smallrye-*` | MicroProfile impls |
| MicroProfile Config | `io.quarkus:quarkus-config-yaml` | Optional: YAML config support |
| MicroProfile Health | `io.quarkus:quarkus-smallrye-health` | |
| MicroProfile Metrics | `io.quarkus:quarkus-micrometer` | Micrometer preferred over MP Metrics |
| MicroProfile OpenAPI | `io.quarkus:quarkus-smallrye-openapi` | Swagger UI at `/q/swagger-ui` |
| MicroProfile Fault Tolerance | `io.quarkus:quarkus-smallrye-fault-tolerance` | |
| MicroProfile JWT | `io.quarkus:quarkus-smallrye-jwt` | |
| JAAS / Security | `io.quarkus:quarkus-oidc` | For OAuth2 / Keycloak |
| Servlet Filter | `io.quarkus:quarkus-undertow` | Only if truly needed |
| WebSocket | `io.quarkus:quarkus-websockets` | |
| Mail (JavaMail) | `io.quarkus:quarkus-mailer` | |
| Scheduler (EJB Timer) | `io.quarkus:quarkus-scheduler` | Replaces `@Schedule` |
| `maven-war-plugin` | REMOVE | No longer producing WAR |
| `maven-ejb-plugin` | REMOVE | No EJBs |

## Quarkus BOM

All Quarkus extensions are version-managed by the BOM. Do NOT specify versions on individual extensions:

```xml
<dependencyManagement>
    <dependencies>
        <dependency>
            <groupId>io.quarkus.platform</groupId>
            <artifactId>quarkus-bom</artifactId>
            <version>3.8.4</version>
            <type>pom</type>
            <scope>import</scope>
        </dependency>
    </dependencies>
</dependencyManagement>
```
