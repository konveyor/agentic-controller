---
name: maven-migration
description: Migrates Maven POM files from Java EE to Jakarta EE.
---

# Maven Migration

When migrating a Java EE application to Jakarta EE, update all Maven
POM files to use Jakarta EE dependencies.

## Steps

1. Replace `javax.*` group IDs with `jakarta.*` equivalents.
2. Update the `javax.servlet` API to `jakarta.servlet`.
3. Replace `javax.persistence` with `jakarta.persistence`.
4. Update plugin versions for Jakarta EE compatibility.
5. Run `mvn compile` to verify the migration.

## Examples

Before:
```xml
<dependency>
    <groupId>javax.servlet</groupId>
    <artifactId>javax.servlet-api</artifactId>
    <version>4.0.1</version>
</dependency>
```

After:
```xml
<dependency>
    <groupId>jakarta.servlet</groupId>
    <artifactId>jakarta.servlet-api</artifactId>
    <version>6.0.0</version>
</dependency>
```
