# Decisions: Java EE → Quarkus

The choices this migration forces that the code often cannot settle. Read it
before planning, or when asking a human about the application.

Each entry gives:

- **Applies when** — the evidence that makes this a decision for the application.
- **Settled without asking when** — evidence that leaves one reasonable answer.
  Record that answer and its source; do not ask.
- **Options** — the concrete choices, with what each costs.
- **Default** — the recommendation when nothing else is known.
- **Changes** — what the answer changes in the plan.

Defaults follow one rule: the migration should not change how the application
behaves or is operated unless someone decides it should.

---

## Java release

- **Applies when**: the build compiles for a Java release below 17
  (`maven.compiler.source`, `target` or `release`).
- **Settled without asking when**: the build already targets 17 or later —
  keep it.
- **Options**:
  - A) Java 17 — the minimum Quarkus 3 supports.
  - B) Java 21 — a later long-term-support release, and a larger jump for the
    code and for the images that run it.
- **Default**: A.
- **Changes**: compiler settings in the build, language-level fixes in scope,
  and the base image the deployment needs.

## Production database

- **Applies when**: the application has a JPA persistence unit or a JDBC data
  source.
- **Settled without asking when**: a JDBC driver, a Hibernate dialect or a JDBC
  URL names a production database — in `persistence.xml`, in server data source
  files such as `*-ds.xml`, `*-jdbc.xml` or `standalone*.xml`, in the driver
  dependencies, or in the repository's own deployment instructions. Target
  that database.
- **Open when**: only an embedded database (H2, HSQLDB, Derby) is configured,
  or the data source is a JNDI name the repository never defines.
- **Options**:
  - A) PostgreSQL.
  - B) Keep the embedded database in production.
  - C) Another database (say which).
- **Default**: A — what `modules/app-config.md` configures, with H2 for the dev
  and test profiles.
- **Changes**: the JDBC extension in the build, the data source properties,
  and the dev and test profile settings.

## Schema management

- **Applies when**: the application has JPA entities.
- **Settled without asking when**: Flyway or Liquibase is already in use —
  keep it. Or schema generation is off (`none`, `validate`) and the repository
  holds no schema scripts — the schema is managed outside the application, and
  stays that way.
- **Open when**: Hibernate creates or updates the schema at startup
  (`hibernate.hbm2ddl.auto` or `javax.persistence.schema-generation.*` set to
  `create`, `create-drop` or `update`), with or without an `import.sql`.
- **Options**:
  - A) Keep Hibernate schema generation as it is today.
  - B) Introduce Flyway, with a baseline migration written from the entities.
  - C) Turn generation off — the schema is managed outside the application.
- **Default**: A.
- **Changes**: the schema generation setting (`references/config-map.md`),
  whether `quarkus-flyway` and a migration directory are added, and whether
  `import.sql` is kept.

## Messaging broker

- **Applies when**: the application has `@MessageDriven` beans or uses
  `javax.jms`.
- **Settled without asking when**: a parameter or the grounding names the
  broker the organisation runs.
- **Options**:
  - A) An AMQP 1.0 broker, for example ActiveMQ Artemis — keeps queue and topic
    semantics.
  - B) Apache Kafka — only where the organisation is moving its messaging to
    Kafka; delivery and ordering semantics change.
  - C) In-process CDI events, no broker — only when every producer and consumer
    of a destination is inside this application and messages need not survive a
    restart.
- **Default**: A — what `modules/messaging.md` implements.
- **Changes**: the messaging extension and connector configuration; for C, no
  broker dependency, and events in place of destinations.

## View layer: JSP pages

- **Applies when**: the application has `.jsp` pages. Quarkus has no JSP
  support, so keeping them as they are is not one of the options. Static files
  beside the pages (HTML, JavaScript, CSS under `src/main/webapp`) are not part
  of this decision: they move to `src/main/resources/META-INF/resources`.
- **Options**:
  - A) Turn the pages into static HTML — for pages that only load a
    client-side application and render nothing on the server; anything a page
    computes moves to a REST endpoint.
  - B) Rewrite the pages as Qute templates — keeps them server-rendered.
  - C) Go API-only — REST endpoints for what the pages do, with the user
    interface as separate work.
  - D) Leave the pages out of this migration and record them as follow-up.
- **Default**: A when every page only loads a client-side application;
  otherwise B.
- **Changes**: A replaces each page with an HTML file, and adds an endpoint
  where a page computed something; B adds one step per page; C removes the
  pages and adds endpoints; D leaves the pages untouched, and the plan's Notes
  say they will not work until the follow-up is done.

## View layer: JSF pages and servlets

- **Applies when**: the application renders pages with JSF (`faces-config.xml`,
  `.xhtml`, `javax.faces`) or with servlets that write HTML.
- **Options**:
  - A) Keep the pages server-rendered on Quarkus — JSF through a JSF
    implementation's Quarkus extension, servlets through `quarkus-undertow`.
  - B) Rewrite the pages as Qute templates.
  - C) Go API-only — REST endpoints for what the pages do, with the user
    interface as separate work.
  - D) Leave the view layer out of this migration and record it as follow-up.
- **Default**: A.
- **Changes**: A adds the view extensions and moves the web resources; B adds
  one step per page; C removes the pages and adds endpoints; D leaves the pages
  untouched, and the plan's Notes say the user interface will not work until
  the follow-up is done.

## Authentication and authorisation

- **Applies when**: `web.xml` has `<security-constraint>` or `<login-config>`,
  a server descriptor names a security domain (`jboss-web.xml`,
  `weblogic.xml`), or the code uses `@RolesAllowed`, `HttpServletRequest.login`
  or JAAS.
- **Settled without asking when**: the application already integrates an OpenID
  Connect or OAuth2 provider (an adapter dependency or its configuration is in
  the repository) — keep that provider, through `quarkus-oidc`.
- **Open when**: authentication is container-managed — the user store is in the
  application server's configuration, which is not in the repository.
- **Options**:
  - A) OpenID Connect against an identity provider, for example Keycloak —
    needs a provider the organisation runs.
  - B) Keep the current user store (database, LDAP or a properties file) behind
    Quarkus Security, with the same form or basic login.
  - C) Carry over the role checks and path rules only, and leave the
    authentication mechanism to be wired after the migration.
- **Default**: A when the grounding or the repository shows an identity
  provider is available to the application; otherwise B.
- **Changes**: the security extension, the `quarkus.http.auth.*` policies that
  replace `<security-constraint>`, and for A the provider settings the
  deployment has to supply.

## Callers of remote EJB interfaces

- **Applies when**: session beans expose `@Remote` interfaces.
  `modules/ejb-to-cdi.md` removes them, and a caller in another application
  cannot be seen from this repository.
- **Settled without asking when**: the grounding names the applications that
  depend on this one, or says there are none.
- **Options**:
  - A) No other application calls them — remove the remote interfaces.
  - B) Other applications call them — expose the same operations as REST
    endpoints; the callers must change.
  - C) Not known — remove them, and list the operations in the plan's Notes as
    a risk to confirm before release.
- **Default**: A when the build publishes no client artifact; otherwise B.
- **Changes**: for B, a REST resource for each remote operation, with the
  callers' change named as out of scope.

## Application-server APIs and vendored libraries

- **Applies when**: the code imports an application server's own packages
  (`weblogic.*`, `org.jboss.*`, `com.ibm.websphere.*`), or the build depends on
  vendored JARs, `system`-scoped artifacts, or an organisation-specific group.
- **Settled without asking when**: the use is one the modules already replace —
  server lifecycle listeners (`modules/lifecycle.md`), JNDI lookups
  (`modules/ejb-to-cdi.md`), server stubs (`modules/cleanup.md`).
- **Options**. Each one that remains is a decision of its own, with its own
  block and its own question:
  - A) Replace it with the Quarkus or standard equivalent (name it).
  - B) Keep it as an ordinary dependency — it must resolve from a repository the
    build can reach and must not need the application server at run time.
  - C) Remove the feature that depends on it.
  - D) Leave it out of scope and record it as a blocker.
- **Default**: A where an equivalent is known. Otherwise B, and say in the
  question that you do not know what the library does.
- **Changes**: the dependencies in the build, one step per call site for A, and
  a Notes entry for D.

## Modules and packaging

- **Applies when**: the build has more than one module — typically an EAR
  assembling EJB, WAR and library modules — or the repository holds more than
  one deployable.
- **Settled without asking when**: the build is a single WAR or JAR module — it
  becomes one Quarkus application.
- **Options**:
  - A) One Quarkus application module, with the other modules kept as libraries
    it depends on.
  - B) Merge every module into a single Quarkus module.
  - C) Migrate one named module now and leave the rest.
- **Default**: A — the smallest change to the build's structure.
- **Changes**: which `pom.xml` gets the Quarkus plugin, the removal of the EAR
  module, a `META-INF/beans.xml` or Jandex index in each library module that
  holds beans, and for C the plan's scope.
