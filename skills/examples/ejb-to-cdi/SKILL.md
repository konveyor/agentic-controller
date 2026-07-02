---
name: ejb-to-cdi
description: Migrates EJB components to CDI managed beans.
---

# EJB to CDI Migration

When migrating from Java EE to Jakarta EE or Quarkus, replace
Enterprise JavaBeans (EJB) with CDI managed beans.

## Rules

1. Replace `@Stateless` with `@ApplicationScoped`.
2. Replace `@Stateful` with `@SessionScoped` or `@ApplicationScoped`
   depending on the use case.
3. Replace `@Singleton` (javax.ejb) with `@ApplicationScoped` +
   `@Startup` if initialization is needed.
4. Remove `@LocalBean` and `@Local` — CDI beans are local by default.
5. Replace `@EJB` injection with `@Inject`.
6. Replace JNDI lookups with CDI `@Inject` or programmatic lookup
   via `CDI.current().select(...)`.
7. Replace `@Schedule` (EJB Timer) with Quarkus `@Scheduled` or
   `jakarta.enterprise.concurrent` scheduled executors.
8. Replace `@MessageDriven` with framework-specific messaging
   (e.g., Quarkus SmallRye Reactive Messaging with `@Incoming`).

## Examples

Before:
```java
@Stateless
public class OrderService {
    @EJB
    private InventoryService inventory;

    public void processOrder(Order order) {
        inventory.reserve(order.getItems());
    }
}
```

After:
```java
@ApplicationScoped
public class OrderService {
    @Inject
    InventoryService inventory;

    public void processOrder(Order order) {
        inventory.reserve(order.getItems());
    }
}
```
