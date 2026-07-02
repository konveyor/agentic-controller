# Agent Container Images

Layered image hierarchy for agent workloads. Each layer adds
capabilities on top of the previous one.

```
agent-base            UBI9 + harness + skillctl + system tools
  └─ agent-base-goose   + goose runtime
       └─ agent-java-goose   + JDK 21 + Maven
```

## Images

### agent-base

The base image for all agents. Contains:
- UBI 9 minimal
- `konveyor-harness` binary (Go entrypoint managing git lifecycle)
- `skillctl` for skill management
- System tools (git, etc.)
- `/opt/skills/` directory for skill mounting
- Non-root execution compatible with OpenShift restricted SCC

### agent-base-goose

Extends `agent-base` with the Goose agent runtime. Goose exposes
ACP over HTTP via `goose serve --port 4000`.

### agent-java-goose

Extends `agent-base-goose` with Java development toolchains:
- JDK 21
- Maven

## Building

```bash
# Build base image
podman build -t quay.io/konveyor/agent-base:latest images/agent-base/

# Build goose runtime image
podman build -t quay.io/konveyor/agent-base-goose:latest images/agent-base-goose/

# Build Java language image
podman build -t quay.io/konveyor/agent-java-goose:latest images/agent-java-goose/
```

See the [agent-base-image-composition enhancement](https://github.com/konveyor/enhancements/pull/296)
for the full design.
