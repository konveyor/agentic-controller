# Migration Phases Reference

Standard file ordering and transformation rules by migration type.

---

## Java EE 7 → Quarkus 3

### Phase Order
1. **Project config** — `pom.xml`, then create `application.properties`
2. **Delete legacy config** — `persistence.xml`, `beans.xml`, `web.xml`
3. **Utils** — `Producers.java`, `Transformers.java`, `StartupListener.java`, `DataBaseMigrationStartup.java`
4. **Persistence** — `Resources.java`
5. **Models** — all `model/*.java` (JPA entities — usually just import changes)
6. **Services** — `ShippingService`, `ProductService`, `CatalogService`, `InventoryNotificationMDB`, `OrderServiceMDB` (hardest — JMS→reactive)
7. **REST endpoints** — `RestApplication.java`, then each `*Endpoint.java`
8. **Cleanup** — delete WebLogic stubs, remove unused imports

### Transformation Rules
```
# Imports
javax.ejb.*           → jakarta.ejb.* (then remove @Stateless/@Stateful)
javax.inject.*        → jakarta.inject.*
javax.enterprise.*    → jakarta.enterprise.*
javax.persistence.*   → jakarta.persistence.*
javax.ws.rs.*         → jakarta.ws.rs.*
javax.transaction.*   → jakarta.transaction.*
javax.jms.*           → REMOVE (replace with SmallRye Reactive Messaging)
weblogic.*            → REMOVE entirely

# Annotations
@Stateless            → @ApplicationScoped
@Stateful             → @ApplicationScoped
@EJB                  → @Inject
@MessageDriven        → @Incoming("channel-name") on method (SmallRye)

# Config
persistence.xml       → quarkus.datasource.* in application.properties
jndi datasource       → quarkus.datasource.jdbc.url
WAR packaging         → JAR (quarkus.package.type=uber-jar)
```

### MDB → Reactive Messaging Pattern
```java
// BEFORE (Java EE)
@MessageDriven(activationConfig = {
    @ActivationConfigProperty(propertyName = "destinationType", propertyValue = "javax.jms.Queue"),
    @ActivationConfigProperty(propertyName = "destination", propertyValue = "java:/orders/queue")
})
public class OrderServiceMDB implements MessageListener {
    public void onMessage(Message rcvMessage) { ... }
}

// AFTER (Quarkus)
@ApplicationScoped
public class OrderServiceMDB {
    @Incoming("orders")
    public void onMessage(String orderJson) { ... }
}
// + in application.properties:
// mp.messaging.incoming.orders.connector=smallrye-amqp
// mp.messaging.incoming.orders.address=orders
```

---

## Python 2 → Python 3

### Phase Order
1. `requirements.txt` / `setup.py` — update dependency versions
2. `__init__.py` files
3. Utility/helper modules first
4. Core business logic modules
5. Entry points / main scripts last

### Transformation Rules
```
print "x"            → print("x")
print >> sys.stderr  → print(..., file=sys.stderr)
xrange(...)          → range(...)
unicode(x)           → str(x)
basestring           → str
raw_input(...)       → input(...)
except E, e:         → except E as e:
raise E, msg         → raise E(msg)
dict.iteritems()     → dict.items()
dict.itervalues()    → dict.values()
dict.iterkeys()      → dict.keys()
/  (int division)    → // where integer result needed
```

---

## React Class Components → Functional + Hooks

### Phase Order
1. Leaf components first (no children that are also class components)
2. Shared/common components
3. Page-level components
4. Root/App component last

### Transformation Rules
```
class Foo extends React.Component  → const Foo = () =>
this.state = { x }                 → const [x, setX] = useState(...)
this.setState({ x: val })          → setX(val)
componentDidMount                  → useEffect(() => {...}, [])
componentDidUpdate(prevProps)      → useEffect(() => {...}, [dep])
componentWillUnmount               → useEffect(() => { return () => cleanup }, [])
this.props.x                       → props.x (or destructure)
render() { return (...) }          → return (...)
```

---

## Node.js 14 → Node.js 20

### Phase Order
1. `package.json` — update engines field + dependency versions
2. Any custom HTTP server code
3. Crypto usage (subtle API changes)
4. Stream usage (updated in Node 16+)
5. Test files last

### Key Changes
```
require('crypto').createCipher     → createCipheriv (deprecated removal)
Buffer(size)                       → Buffer.alloc(size)
url.parse()                        → new URL()
fs.promises everywhere             → await fs.promises.* or fs/promises import
```
