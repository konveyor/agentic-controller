# Annotation Map: Java EE → Quarkus/CDI

Use this table when converting EJB classes in the EJB-to-CDI phase.

## EJB → CDI Annotations

| Java EE Annotation | Quarkus Replacement | Import |
|---|---|---|
| `@Stateless` | `@ApplicationScoped` | `jakarta.enterprise.context.ApplicationScoped` |
| `@Stateful` | `@ApplicationScoped` | `jakarta.enterprise.context.ApplicationScoped` |
| `@Singleton` (javax.ejb) | `@ApplicationScoped` | `jakarta.enterprise.context.ApplicationScoped` |
| `@EJB` | `@Inject` | `jakarta.inject.Inject` |
| `@Local` | REMOVE | — |
| `@Remote` | REMOVE | — |
| `@LocalBean` | REMOVE | — |
| `@TransactionAttribute(REQUIRED)` | `@Transactional` | `jakarta.transaction.Transactional` |
| `@TransactionAttribute(REQUIRES_NEW)` | `@Transactional(REQUIRES_NEW)` | `jakarta.transaction.Transactional` |
| `@TransactionAttribute(NOT_SUPPORTED)` | `@Transactional(NOT_SUPPORTED)` | `jakarta.transaction.Transactional` |
| `@Schedule` (EJB Timer) | `@Scheduled` | `io.quarkus.scheduler.Scheduled` |
| `@Timeout` (EJB Timer) | REMOVE — use `@Scheduled` | — |
| `@MessageDriven` | `@ApplicationScoped` + `@Incoming` | See messaging phase |
| `@ActivationConfigProperty` | REMOVE — config in application.properties | — |
| `@Resource` (DataSource) | `@Inject` + `@ConfigProperty` | `jakarta.inject.Inject` |
| `@Resource` (JMS) | `@Inject @Channel` | See messaging phase |
| `@PersistenceContext` | `@Inject` | `jakarta.inject.Inject` |
| `@PersistenceUnit` | `@Inject` | `jakarta.inject.Inject` |

## JAX-RS Annotations (unchanged, just repackaged)

These annotations keep the same name but move from `javax.ws.rs` to `jakarta.ws.rs`:

| Annotation | New Import |
|---|---|
| `@Path` | `jakarta.ws.rs.Path` |
| `@GET`, `@POST`, `@PUT`, `@DELETE` | `jakarta.ws.rs.*` |
| `@Produces`, `@Consumes` | `jakarta.ws.rs.*` |
| `@PathParam`, `@QueryParam` | `jakarta.ws.rs.*` |

## JPA Annotations (unchanged, just repackaged)

| Annotation | New Import |
|---|---|
| `@Entity`, `@Table` | `jakarta.persistence.*` |
| `@Id`, `@GeneratedValue` | `jakarta.persistence.*` |
| `@Column`, `@JoinColumn` | `jakarta.persistence.*` |
| `@OneToMany`, `@ManyToOne` | `jakarta.persistence.*` |
| `@NamedQuery` | `jakarta.persistence.*` |

## Lifecycle Annotations

| Java EE | Quarkus | Import |
|---|---|---|
| `@PostConstruct` | `@PostConstruct` (keep) | `jakarta.annotation.PostConstruct` |
| `@PreDestroy` | `@PreDestroy` (keep) | `jakarta.annotation.PreDestroy` |
| Server startup listener | `@Observes StartupEvent` | `io.quarkus.runtime.StartupEvent` |
| Server shutdown listener | `@Observes ShutdownEvent` | `io.quarkus.runtime.ShutdownEvent` |
