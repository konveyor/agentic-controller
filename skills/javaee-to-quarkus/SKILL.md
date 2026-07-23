---
name: javaee-to-quarkus
description: Migrates Java EE 7/8 applications (WebLogic, JBoss, WildFly) to Quarkus 3.
  Use when moving off a traditional Java EE application server to the Quarkus runtime,
  not just renaming javax to jakarta.
license: Apache-2.0
metadata:
  source: java-ee-7
  target: quarkus-3
  language: java
  build_tool: "maven: mvn compile"
  guide_url: https://quarkus.io/guides/migration-guide
  generated_by: migration-skills-generator
  generated_at: 2026-07-20
---

# Java EE 7/8 to Quarkus 3 Migration

**Prerequisite:** Ensure your application compiles on Java EE before starting. Java 17 is the minimum JDK for Quarkus 3.

This migration goes beyond a javax-to-jakarta namespace rename. It replaces the Java EE programming model with Quarkus: EJB → CDI managed beans, JMS/MDB → SmallRye Reactive Messaging, WAR → JAR packaging, persistence.xml → application.properties, JNDI lookups → direct injection, and app server lifecycle hooks → Quarkus lifecycle events. The result is a standalone Quarkus application with no application server dependency.

## Phases

Execute in order. After each phase, run the project build and stop if it fails.

1. **Build Config** — Restructure pom.xml: WAR→JAR, add Quarkus BOM and plugin, replace Java EE dependencies with Quarkus extensions. See `modules/build-config.md`.
2. **App Config** — Replace persistence.xml with application.properties, remove web.xml and beans.xml. See `modules/app-config.md`.
3. **EJB to CDI** — Replace EJB annotations with CDI, remove Remote/Local interfaces, replace JNDI lookups with injection. See `modules/ejb-to-cdi.md`.
4. **Messaging** — Convert MDB classes to SmallRye Reactive Messaging, replace JMS producers with Emitter. See `modules/messaging.md`.
5. **Lifecycle** — Replace server-specific lifecycle listeners with Quarkus startup/shutdown events. See `modules/lifecycle.md`.
6. **Cleanup** — Delete legacy files, remove weblogic/jboss stubs, verify no javax.* EE imports remain. See `modules/cleanup.md`.

## How to use

Load each phase's module when starting that phase. Each module contains before/after code examples and references mapping tables in `references/`. Apply every applicable transformation to the codebase.

## Build gate

After completing each phase:
1. Detect the project's build tool (check metadata `build_tool` field above, or detect from project files: `pom.xml` → `mvn compile`)
2. Run the build
3. If it fails, fix the issue before proceeding
4. If you cannot fix it, stop and report to the user
