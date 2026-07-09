# Spring Boot 2 → Spring Boot 3: Migration Reference

---
name: springboot-2-to-3
description: Migration patterns for Spring Boot 2.x to Spring Boot 3.x (Jakarta EE, Java 17+)
applies_to:
  manifests:
    pom_xml: true
  graph_patterns:
    - "imports contains org.springframework.boot"
    - "imports contains javax.persistence"
    - "imports contains javax.validation"
---

## Migration Order (Layer Dependency)

Always migrate in this order — dependencies flow upward:

1. **Build config** - pom.xml or build.gradle (Java 17+, Spring Boot 3.x BOM)
2. **App config** - application.properties / application.yml (deprecated properties)
3. **Security config** - SecurityConfig, WebSecurityConfigurerAdapter replacements
4. **Persistence layer** - Entities, Repositories (JPA → Jakarta Persistence)
5. **Service layer** - Business logic, validators
6. **API layer** - Controllers, REST endpoints
7. **Tests** - Update test dependencies and annotations
8. **Cleanup** - Remove deprecated code

**Why this order**: Build must support Java 17 first. Security config changes are foundational. Entities have no business logic deps. Services depend on entities. Controllers depend on services.

---

## Import/Package Transformations

All `javax.*` imports must change to `jakarta.*`:

| Old (javax) | New (jakarta) | Affected Components |
|-------------|---------------|---------------------|
| `javax.persistence.*` | `jakarta.persistence.*` | JPA entities, repositories |
| `javax.validation.*` | `jakarta.validation.*` | Bean validation (@NotNull, @Valid) |
| `javax.servlet.*` | `jakarta.servlet.*` | Filters, servlets, HTTP utilities |
| `javax.annotation.*` | `jakarta.annotation.*` | @PostConstruct, @PreDestroy, @Resource |
| `javax.transaction.*` | `jakarta.transaction.*` | @Transactional (if using JTA) |
| `javax.xml.bind.*` | `jakarta.xml.bind.*` | JAXB (if used) |

---

## Annotation Transformations

No annotation renames — just namespace changes. But note these Spring-specific changes:

| Old Pattern | New Pattern | Notes |
|-------------|-------------|-------|
| `@WebMvcTest` without imports | `@WebMvcTest` + explicit `@Import(SecurityConfig.class)` | Security auto-config changed |
| `WebSecurityConfigurerAdapter` | `SecurityFilterChain` bean | Adapter pattern deprecated |
| `@SpringBootTest(webEnvironment = RANDOM_PORT)` | Same, but check MockMvc setup | Some test utilities changed |

---

## Pattern Catalog

### Pattern 1: WebSecurityConfigurerAdapter → SecurityFilterChain

**BEFORE (Spring Boot 2):**
```java
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configuration.EnableWebSecurity;
import org.springframework.security.config.annotation.web.configuration.WebSecurityConfigurerAdapter;

@EnableWebSecurity
public class SecurityConfig extends WebSecurityConfigurerAdapter {
    
    @Override
    protected void configure(HttpSecurity http) throws Exception {
        http
            .authorizeRequests()
                .antMatchers("/public/**").permitAll()
                .anyRequest().authenticated()
            .and()
            .formLogin();
    }
}
```

**AFTER (Spring Boot 3):**
```java
import org.springframework.context.annotation.Bean;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configuration.EnableWebSecurity;
import org.springframework.security.web.SecurityFilterChain;

@EnableWebSecurity
public class SecurityConfig {
    
    @Bean
    public SecurityFilterChain filterChain(HttpSecurity http) throws Exception {
        http
            .authorizeHttpRequests(authorize -> authorize
                .requestMatchers("/public/**").permitAll()
                .anyRequest().authenticated()
            )
            .formLogin(form -> form.defaultSuccessUrl("/home"));
        return http.build();
    }
}
```

**Specific changes:**
1. Remove: `extends WebSecurityConfigurerAdapter`
2. Remove: `@Override protected void configure(HttpSecurity http)`
3. Add: `@Bean public SecurityFilterChain filterChain(HttpSecurity http)`
4. Replace: `.authorizeRequests()` → `.authorizeHttpRequests(authorize -> ...)`
5. Replace: `.antMatchers(...)` → `.requestMatchers(...)`
6. Replace: Method chaining `.and()` → Lambda DSL
7. Add: `return http.build();` at end of method

**Why**: Spring Security 5.7+ deprecated `WebSecurityConfigurerAdapter`. The new approach uses component-based configuration with `@Bean` methods returning `SecurityFilterChain`.

---

### Pattern 2: JPA Entity (javax → jakarta)

**BEFORE (Spring Boot 2):**
```java
import javax.persistence.*;
import javax.validation.constraints.NotNull;
import javax.validation.constraints.Size;

@Entity
@Table(name = "users")
public class User {
    
    @Id
    @GeneratedValue(strategy = GenerationType.IDENTITY)
    private Long id;
    
    @NotNull
    @Size(min = 3, max = 50)
    private String username;
    
    @OneToMany(mappedBy = "user", cascade = CascadeType.ALL)
    private List<Order> orders;
}
```

**AFTER (Spring Boot 3):**
```java
import jakarta.persistence.*;
import jakarta.validation.constraints.NotNull;
import jakarta.validation.constraints.Size;
import java.util.List;

@Entity
@Table(name = "users")
public class User {
    
    @Id
    @GeneratedValue(strategy = GenerationType.IDENTITY)
    private Long id;
    
    @NotNull
    @Size(min = 3, max = 50)
    private String username;
    
    @OneToMany(mappedBy = "user", cascade = CascadeType.ALL)
    private List<Order> orders;
}
```

**Specific changes:**
1. Replace: `import javax.persistence.*` → `import jakarta.persistence.*`
2. Replace: `import javax.validation.*` → `import jakarta.validation.*`

**Why**: Jakarta EE (formerly Java EE) namespace migration. All Jakarta EE 9+ specs use `jakarta.*` instead of `javax.*`.

---

### Pattern 3: REST Controller with Validation

**BEFORE (Spring Boot 2):**
```java
import org.springframework.web.bind.annotation.*;
import javax.validation.Valid;

@RestController
@RequestMapping("/api/users")
public class UserController {
    
    @PostMapping
    public User createUser(@Valid @RequestBody User user) {
        return userService.save(user);
    }
}
```

**AFTER (Spring Boot 3):**
```java
import org.springframework.web.bind.annotation.*;
import jakarta.validation.Valid;

@RestController
@RequestMapping("/api/users")
public class UserController {
    
    @PostMapping
    public User createUser(@Valid @RequestBody User user) {
        return userService.save(user);
    }
}
```

**Specific changes:**
1. Replace: `import javax.validation.Valid` → `import jakarta.validation.Valid`

**Why**: Jakarta namespace migration.

---

### Pattern 4: Servlet Filter (javax → jakarta)

**BEFORE (Spring Boot 2):**
```java
import javax.servlet.*;
import javax.servlet.http.HttpServletRequest;
import org.springframework.stereotype.Component;

@Component
public class LoggingFilter implements Filter {
    
    @Override
    public void doFilter(ServletRequest request, ServletResponse response, FilterChain chain)
            throws IOException, ServletException {
        HttpServletRequest req = (HttpServletRequest) request;
        logger.info("Request URI: {}", req.getRequestURI());
        chain.doFilter(request, response);
    }
}
```

**AFTER (Spring Boot 3):**
```java
import jakarta.servlet.*;
import jakarta.servlet.http.HttpServletRequest;
import org.springframework.stereotype.Component;
import java.io.IOException;

@Component
public class LoggingFilter implements Filter {
    
    @Override
    public void doFilter(ServletRequest request, ServletResponse response, FilterChain chain)
            throws IOException, ServletException {
        HttpServletRequest req = (HttpServletRequest) request;
        logger.info("Request URI: {}", req.getRequestURI());
        chain.doFilter(request, response);
    }
}
```

**Specific changes:**
1. Replace: `import javax.servlet.*` → `import jakarta.servlet.*`

**Why**: Servlet API moved to Jakarta namespace.

---

### Pattern 5: @PostConstruct / @PreDestroy Lifecycle

**BEFORE (Spring Boot 2):**
```java
import javax.annotation.PostConstruct;
import javax.annotation.PreDestroy;
import org.springframework.stereotype.Service;

@Service
public class CacheService {
    
    @PostConstruct
    public void init() {
        // Initialize cache
    }
    
    @PreDestroy
    public void cleanup() {
        // Clear cache
    }
}
```

**AFTER (Spring Boot 3):**
```java
import jakarta.annotation.PostConstruct;
import jakarta.annotation.PreDestroy;
import org.springframework.stereotype.Service;

@Service
public class CacheService {
    
    @PostConstruct
    public void init() {
        // Initialize cache
    }
    
    @PreDestroy
    public void cleanup() {
        // Clear cache
    }
}
```

**Specific changes:**
1. Replace: `import javax.annotation.*` → `import jakarta.annotation.*`

**Why**: Jakarta namespace migration.

---

## Files to DELETE

| Delete this | Replaced by | Reason |
|-------------|-------------|--------|
| None typically | N/A | Spring Boot 3 is largely backward-compatible at the file level |

**Note**: No files are typically deleted, but deprecated code should be updated (see Deprecated Properties below).

---

## Files to CREATE

No new files required — Spring Boot 3 uses the same structure as Spring Boot 2.

---

## Build File Changes

### pom.xml

```xml
<!-- CHANGE: Parent version -->
<parent>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-parent</artifactId>
    <version>3.2.0</version>  <!-- Was 2.7.x -->
</parent>

<!-- CHANGE: Java version -->
<properties>
    <java.version>17</java.version>  <!-- Was 8 or 11 -->
</properties>

<!-- REMOVE: Javax dependencies (if explicitly declared) -->
<!-- These are now provided by jakarta dependencies via Spring Boot BOM -->
<dependency>
    <groupId>javax.persistence</groupId>
    <artifactId>javax.persistence-api</artifactId>  <!-- REMOVE -->
</dependency>

<!-- ADD: If using Swagger/OpenAPI, update to springdoc-openapi v2 -->
<dependency>
    <groupId>org.springdoc</groupId>
    <artifactId>springdoc-openapi-starter-webmvc-ui</artifactId>
    <version>2.3.0</version>  <!-- Was springdoc-openapi-ui 1.x -->
</dependency>

<!-- ADD: If using Actuator with Micrometer -->
<!-- Spring Boot 3 uses Micrometer Observation API -->
<dependency>
    <groupId>io.micrometer</groupId>
    <artifactId>micrometer-tracing-bridge-brave</artifactId>  <!-- For distributed tracing -->
</dependency>
```

### build.gradle

```gradle
// CHANGE: Spring Boot plugin version
plugins {
    id 'org.springframework.boot' version '3.2.0'  // Was 2.7.x
    id 'io.spring.dependency-management' version '1.1.4'
    id 'java'
}

// CHANGE: Java version
java {
    sourceCompatibility = '17'  // Was '8' or '11'
}

// REMOVE: Javax dependencies (now jakarta via Spring Boot BOM)
// dependencies {
//     implementation 'javax.persistence:javax.persistence-api'  // REMOVE
// }
```

---

## application.properties / application.yml Changes

### Deprecated Properties

| Old Property (Spring Boot 2) | New Property (Spring Boot 3) | Notes |
|------------------------------|------------------------------|-------|
| `spring.jpa.hibernate.use-new-id-generator-mappings` | Removed | Always true in Hibernate 6 |
| `spring.mvc.throw-exception-if-no-handler-found` | `spring.mvc.problemdetails.enabled=true` | RFC 7807 Problem Details |
| `spring.security.oauth2.resourceserver.jwt.jwk-set-uri` | Same, but check OIDC config | OIDC config may need updates |
| `management.metrics.export.*` | `management.observations.export.*` | Micrometer Observation API |

### Example application.properties

```properties
# BEFORE (Spring Boot 2)
spring.jpa.hibernate.use-new-id-generator-mappings=true
spring.mvc.throw-exception-if-no-handler-found=true

# AFTER (Spring Boot 3)
# spring.jpa.hibernate.use-new-id-generator-mappings — REMOVED (always true)
spring.mvc.problemdetails.enabled=true
```

---

## Verification Commands

```bash
# 1. Update dependencies
mvn clean install
# OR
./gradlew clean build

# 2. Check for javax imports (should be 0, except javax.sql which is OK)
grep -rn "import javax\." src/main/java/ | grep -v "javax.sql" | wc -l
# Should be 0

# 3. Check Java version in compiled classes
javap -v target/classes/com/example/MyClass.class | grep "major version"
# Should show "61" (Java 17) or higher

# 4. Run tests
mvn test
# OR
./gradlew test

# 5. Start application
mvn spring-boot:run
# OR
./gradlew bootRun

# 6. Check actuator endpoints (if using Spring Boot Actuator)
curl http://localhost:8080/actuator/health
```

---

## Notes / Gotchas

### 1. **Java 17 is REQUIRED**
Spring Boot 3 requires Java 17 as the minimum version. You cannot run it on Java 8 or 11.

**Action**: Update `JAVA_HOME` and build tool configuration.

```bash
# Verify Java version
java -version  # Should show 17 or higher
```

### 2. **Hibernate 6.x Breaking Changes**
Spring Boot 3 uses Hibernate 6.x, which has several changes:
- `use-new-id-generator-mappings` is always `true` (property removed)
- Some HQL queries may need updates
- `@Type` annotation deprecated (use `@JdbcType` or `@JavaType`)

**Action**: Test all JPA queries and native queries.

### 3. **Spring Security Configuration**
The `WebSecurityConfigurerAdapter` pattern is removed. All security configs must use `@Bean` methods returning `SecurityFilterChain`.

**Action**: Refactor all security configs (see Pattern 1).

### 4. **Spring Cloud Compatibility**
If using Spring Cloud, ensure you're on a compatible version:
- Spring Boot 3.2.x → Spring Cloud 2023.x (codename: Leyton)
- Check: https://spring.io/projects/spring-cloud#overview

**Action**: Update Spring Cloud BOM version in `pom.xml` / `build.gradle`.

### 5. **Observability Changes**
Spring Boot 3 uses Micrometer Observation API for tracing and metrics.

**Before (Spring Boot 2)**:
```java
@Timed("my.metric")
public void doSomething() { }
```

**After (Spring Boot 3)**:
```java
@Observed(name = "my.metric")
public void doSomething() { }
```

**Action**: Update metrics annotations and check Micrometer documentation.

### 6. **Swagger/OpenAPI**
If using Swagger UI, update to `springdoc-openapi v2`:
- Old: `springdoc-openapi-ui` 1.x
- New: `springdoc-openapi-starter-webmvc-ui` 2.x

**Action**: Update dependency and verify Swagger UI at `/swagger-ui.html`.

### 7. **GraalVM Native Image Support**
Spring Boot 3 has first-class support for GraalVM native images. If interested:
```bash
mvn -Pnative spring-boot:build-image
```

### 8. **Logging Pattern Changes**
Default logging pattern may have changed. If you rely on specific log formats, verify `logging.pattern.console` in `application.properties`.

### 9. **MockMvc Test Changes**
Some `@WebMvcTest` tests may need explicit security config imports:
```java
@WebMvcTest(controllers = UserController.class)
@Import(SecurityConfig.class)  // May be required now
public class UserControllerTest { }
```

### 10. **Deprecation Warnings**
After migration, run with deprecation warnings enabled to catch remaining issues:
```bash
mvn clean compile -Xlint:deprecation
```

---

## Migration Checklist

- [ ] Update Java to 17+ (`JAVA_HOME`, `pom.xml`, `build.gradle`)
- [ ] Update Spring Boot version to 3.2.x in build file
- [ ] Replace all `javax.*` → `jakarta.*` imports
- [ ] Refactor `WebSecurityConfigurerAdapter` → `SecurityFilterChain`
- [ ] Update deprecated `application.properties` entries
- [ ] Update Spring Cloud BOM (if used)
- [ ] Update Swagger/OpenAPI dependency (if used)
- [ ] Run full test suite
- [ ] Test Actuator endpoints
- [ ] Check HQL/native queries with Hibernate 6
- [ ] Verify application starts and basic flows work

---

## Resources

- [Spring Boot 3.0 Migration Guide](https://github.com/spring-projects/spring-boot/wiki/Spring-Boot-3.0-Migration-Guide)
- [Spring Security 6.0 Migration](https://docs.spring.io/spring-security/reference/6.0/migration/index.html)
- [Jakarta EE 9 Namespace](https://jakarta.ee/specifications/platform/9/)
- [Hibernate 6 Migration Guide](https://github.com/hibernate/hibernate-orm/blob/6.0/migration-guide.adoc)
