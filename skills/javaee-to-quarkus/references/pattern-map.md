# Pattern Map: Java EE → Quarkus

Use this table for structural transformations that go beyond simple annotation swaps.

## MDB Patterns

### Queue consumer

```java
// Before
@MessageDriven(activationConfig = {
    @ActivationConfigProperty(propertyName = "destinationType",
                              propertyValue = "javax.jms.Queue"),
    @ActivationConfigProperty(propertyName = "destination",
                              propertyValue = "java:/queues/OrderQueue")
})
public class OrderMDB implements MessageListener {
    public void onMessage(Message msg) {
        TextMessage txt = (TextMessage) msg;
        processOrder(txt.getText());
    }
}

// After
@ApplicationScoped
public class OrderMDB {
    @Incoming("order-queue")
    public void onMessage(String body) {
        processOrder(body);
    }
}
```

### Topic subscriber

```java
// Before
@MessageDriven(activationConfig = {
    @ActivationConfigProperty(propertyName = "destinationType",
                              propertyValue = "javax.jms.Topic"),
    @ActivationConfigProperty(propertyName = "destination",
                              propertyValue = "java:/topics/Events")
})
public class EventMDB implements MessageListener { ... }

// After
@ApplicationScoped
public class EventMDB {
    @Incoming("events")
    public CompletionStage<Void> onEvent(String body) { ... }
}
```

### JMS producer

```java
// Before
@Resource(mappedName = "java:/topic/notifications")
private Topic topic;
@Inject JMSContext jms;

void notify(String msg) {
    jms.createProducer().send(topic, msg);
}

// After
@Inject @Channel("notifications-out")
Emitter<String> emitter;

void notify(String msg) {
    emitter.send(msg);
}
```

## JNDI Patterns

### EJB lookup

```java
// Before
InitialContext ctx = new InitialContext();
OrderService svc = (OrderService) ctx.lookup(
    "java:global/myapp/OrderService");

// After
@Inject OrderService svc;
```

### DataSource lookup

```java
// Before
Context ctx = new InitialContext();
DataSource ds = (DataSource) ctx.lookup("java:/jdbc/myDS");

// After — configured in application.properties, injected via:
@Inject AgroalDataSource dataSource;
// or use Hibernate ORM with @Inject EntityManager
```

### Environment entry

```java
// Before
Context ctx = new InitialContext();
String value = (String) ctx.lookup("java:comp/env/myConfig");

// After
@ConfigProperty(name = "my.config") String value;
```

## EJB Timer → Quarkus Scheduler

```java
// Before
@Singleton
public class BatchJob {
    @Schedule(hour = "*/1", persistent = false)
    public void hourlyJob(Timer timer) { ... }
}

// After
@ApplicationScoped
public class BatchJob {
    @Scheduled(every = "1h")
    public void hourlyJob() { ... }
}
```

## Interceptor Pattern

```java
// Before
@InterceptorBinding
@Target({TYPE, METHOD})
@Retention(RUNTIME)
public @interface Logged {}

@Interceptor @Logged
public class LogInterceptor {
    @AroundInvoke
    public Object log(InvocationContext ctx) throws Exception {
        // logging...
        return ctx.proceed();
    }
}

// After — same pattern works in Quarkus CDI, just update imports:
// javax.interceptor.* → jakarta.interceptor.*
// javax.enterprise.* → jakarta.enterprise.*
```

## Servlet Filter → JAX-RS Filter

```java
// Before
@WebFilter("/*")
public class AuthFilter implements Filter {
    public void doFilter(ServletRequest req, ...) { ... }
}

// After
@Provider
public class AuthFilter implements ContainerRequestFilter {
    @Override
    public void filter(ContainerRequestContext ctx) { ... }
}
```
