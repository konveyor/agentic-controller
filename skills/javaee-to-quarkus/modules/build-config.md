# Phase: Build Config

Restructure pom.xml for Quarkus: change packaging, add Quarkus BOM and plugin, replace Java EE dependencies with Quarkus extensions.

## Steps

1. Read `references/dependency-map.md`
2. Open `pom.xml`

### Packaging

```xml
<!-- Before -->
<packaging>war</packaging>

<!-- After -->
<packaging>jar</packaging>
```

### Remove Java EE umbrella dependency

```xml
<!-- Remove -->
<dependency>
    <groupId>javax</groupId>
    <artifactId>javaee-api</artifactId>
</dependency>
```

Also remove `maven-war-plugin` from `<build><plugins>`.

### Add Quarkus BOM

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

### Add Quarkus Maven plugin

```xml
<build>
    <plugins>
        <plugin>
            <groupId>io.quarkus.platform</groupId>
            <artifactId>quarkus-maven-plugin</artifactId>
            <version>3.8.4</version>
            <extensions>true</extensions>
            <executions>
                <execution>
                    <goals>
                        <goal>build</goal>
                        <goal>generate-code</goal>
                        <goal>generate-code-tests</goal>
                    </goals>
                </execution>
            </executions>
        </plugin>
    </plugins>
</build>
```

### Add Quarkus extensions

Pick extensions based on what the app actually uses. See `references/dependency-map.md` for the full mapping.

Key examples:

| App uses | Quarkus extension |
|---|---|
| CDI / EJB | `quarkus-arc` (always include) |
| JAX-RS + JSON | `quarkus-rest-jackson` |
| JPA / Hibernate | `quarkus-hibernate-orm` |
| PostgreSQL | `quarkus-jdbc-postgresql` |
| H2 (dev/test) | `quarkus-jdbc-h2` |
| DB migrations | `quarkus-flyway` |
| JMS / MDB | `quarkus-smallrye-reactive-messaging-amqp` |
| Health checks | `quarkus-smallrye-health` |
| OAuth2 / Keycloak | `quarkus-oidc` |
| Swagger UI | `quarkus-smallrye-openapi` |

Do NOT add extensions speculatively — only add what the source code needs.

3. Run the build gate

## Build gate

Run `mvn compile`. Common issues:
- Missing Quarkus extensions for dependencies the app uses
- Version conflicts between old Java EE JARs and Quarkus-managed dependencies
- `maven-war-plugin` left in pom.xml after packaging change
