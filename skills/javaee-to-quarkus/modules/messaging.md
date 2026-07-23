# Phase: Messaging

Convert MDB (Message-Driven Bean) classes to SmallRye Reactive Messaging. Replace JMS producers with Emitter.

## Steps

1. Read `references/pattern-map.md`
2. Identify all MDB classes (`@MessageDriven` / `implements MessageListener`)
3. Identify all JMS producer classes (`JMSContext`, `MessageProducer`)

### MDB → @Incoming

```java
// Before
import javax.ejb.MessageDriven;
import javax.ejb.ActivationConfigProperty;
import javax.jms.Message;
import javax.jms.MessageListener;
import javax.jms.TextMessage;

@MessageDriven(activationConfig = {
    @ActivationConfigProperty(propertyName = "destinationType",
                              propertyValue = "javax.jms.Queue"),
    @ActivationConfigProperty(propertyName = "destination",
                              propertyValue = "java:/queues/orders")
})
public class OrderServiceMDB implements MessageListener {
    public void onMessage(Message msg) {
        String body = ((TextMessage) msg).getText();
        // process body...
    }
}

// After
import jakarta.enterprise.context.ApplicationScoped;
import org.eclipse.microprofile.reactive.messaging.Incoming;

@ApplicationScoped
public class OrderServiceMDB {
    @Incoming("orders")
    public void onMessage(String body) {
        // same processing logic — body arrives as String directly
    }
}
```

Add to `application.properties`:
```properties
mp.messaging.incoming.orders.connector=smallrye-amqp
mp.messaging.incoming.orders.address=orders
%dev.mp.messaging.incoming.orders.connector=smallrye-in-memory
```

Derive the channel name from the queue/topic name in the original `@ActivationConfigProperty`.

### JMS Producer → @Outgoing / Emitter

```java
// Before
@Resource(mappedName = "java:/topic/orders")
private Topic ordersTopic;
@Inject JMSContext context;

public void send(String payload) {
    context.createProducer().send(ordersTopic, payload);
}

// After
import org.eclipse.microprofile.reactive.messaging.Channel;
import org.eclipse.microprofile.reactive.messaging.Emitter;

@Inject @Channel("orders-out") Emitter<String> emitter;

public void send(String payload) {
    emitter.send(payload);
}
```

Add to `application.properties`:
```properties
mp.messaging.outgoing.orders-out.connector=smallrye-amqp
mp.messaging.outgoing.orders-out.address=orders
%dev.mp.messaging.outgoing.orders-out.connector=smallrye-in-memory
```

### Import removals

Remove all JMS imports — they have no direct replacement:
```
javax.jms.*              → REMOVE
```

4. Run the build gate

## Build gate

Run `mvn compile`. Common issues:
- Missing `quarkus-smallrye-reactive-messaging-amqp` extension in pom.xml
- `cannot find symbol @Incoming` — add the extension
- `cannot find symbol Emitter` — add `@Channel` import from `org.eclipse.microprofile.reactive.messaging`
