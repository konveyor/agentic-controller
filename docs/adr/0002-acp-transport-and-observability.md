# ADR 0002: ACP Transport and Agent Observability

**Status:** Accepted
**Date:** 2026-06-30
**Authors:** David Zager

## Context

The agentic platform controller needs two capabilities:

1. **Observability** — the UI needs real-time visibility into what a
   running agent is doing (tool calls, text output, progress).
2. **Human-in-the-loop** — a user in the web UI needs to interact
   with a running agent (approve permissions, redirect the agent,
   cancel a run).

The Agent Client Protocol (ACP) provides standardized agent-client
communication with streaming, tool calls, permission requests, and
session management. ACP defines two transports:

- **stdio** — stable, supported by all ACP agents (Goose, OpenCode,
  Claude, Cursor, Gemini CLI, and 30+ others)
- **Streamable HTTP & WebSocket** — draft RFD (merged April 2026),
  reference implementation in Goose (`goose serve`), not yet
  supported by other agents

The question is how the platform should expose agent interaction to
the UI and other consumers.

## Decision

### Controller stays simple — no ACP connection

The controller is a standard stateless reconciler. It does not
connect to agent pods via ACP. It watches:

- **AgentRun CRs** — for creation, to trigger Sandbox creation
- **Sandbox CRs** — for lifecycle transitions (Running, Finished,
  Failed)
- **AgentRun status** — updated by the harness at completion with
  push results (branch name, commit SHA, success/failure)

The controller creates Sandboxes, validates params, tracks lifecycle
via standard CR reconciliation, and sets `completionTime` / duration
on the AgentRun when the Sandbox reports Finished. No persistent
connections, no in-memory state, no streaming data.

### UI connects to agent pods directly for streaming

When a user wants to see what an agent is doing, the UI connects
to the agent pod's ACP HTTP endpoint. For the POC, Goose exposes
this via `goose serve --port 4000`.

```text
UI (browser)
  ↕ WebSocket (via Hub proxy or ingress)
Agent pod :4000/acp
  ↕ ACP over HTTP/WebSocket
Goose (goose serve)
```

The UI calls ACP `session/load` on connect, which causes Goose to
replay the full conversation history as notifications (Goose
persists sessions in SQLite). The UI then receives live events
going forward. This means:

- The UI can connect at any time and see the full history
- Multiple UI clients can connect to the same agent
- Disconnecting and reconnecting recovers the full state
- No intermediary service needs to buffer messages

### Human-in-the-loop via ACP

The ACP protocol supports bidirectional communication. The agent
(Goose) sends `request_permission` requests, and the UI client
responds with allow/deny:

1. Agent needs permission (file write, shell command)
2. Goose sends `request_permission` via WebSocket to connected UI
3. UI shows the request with a diff preview
4. User approves or rejects
5. UI sends response back via WebSocket
6. Agent proceeds or skips

The user can cancel an agent mid-run via `session/cancel`.

For unattended runs (no UI connected), Goose is configured with
`GOOSE_MODE=auto` so it does not send permission requests.

### Users without the UI

Users who do not have the web UI can:

- `kubectl logs <pod>` — tail the container log for agent output
- `kubectl port-forward <pod> 4000:4000` — connect any ACP client
  to the agent's local endpoint
- Watch AgentRun CR status for lifecycle transitions

### Pod architecture

```text
Pod:
  Harness (Go binary, entrypoint)
    1. Read git credentials from mounted Secret
    2. Clone repo, configure workspace remote without credentials
    3. Launch: goose serve --port 4000
    4. On agent exit: commit .konveyor/ files, push using credentials
    5. Update AgentRun CR status with push result (single k8s API write)

  Goose (goose serve on :4000)
    ACP over HTTP (SSE) and WebSocket
    Runs agent, streams events natively
```

The Sandbox CR is created with `service: true`, giving the pod a
stable DNS name (`<sandbox-name>.<namespace>.svc`) for network
access.

### MCP server integration

MCP servers (including Konveyor analysis tools) are passed to Goose
at session creation time via the `session/new` request's
`mcp_servers` array:

```json
{
  "method": "session/new",
  "params": {
    "cwd": "/workspace/repo",
    "mcp_servers": [
      {
        "type": "stdio",
        "name": "konveyor",
        "command": "/usr/local/bin/konveyor-mcp",
        "args": ["--config", "/workspace/mcp-config.json"]
      }
    ]
  }
}
```

This allows per-session MCP configuration without baking it into
the container image or Goose's config file.

### Authentication

Goose's HTTP transport supports `X-Secret-Key` header
authentication. The harness generates a random secret key per run
and sets it via `GOOSE_SERVER__SECRET_KEY`. The key is stored on
the AgentRun status (or a referenced Secret) so authorized
consumers (the UI via Hub proxy) can connect.

### Future: Multi-runtime support via harness bridge

When adding support for agent runtimes that do not implement
ACP-over-HTTP (OpenCode, Claude, Cursor, etc.), the harness adds
a stdio-to-HTTP bridge:

```text
Pod (multi-runtime):
  Harness (Go binary)
    1. Git lifecycle (same as above)
    2. Launch: <agent> acp (over stdio)
    3. Bridge: stdio ACP ↔ HTTP ACP on :4000
```

The harness speaks ACP-over-stdio to the agent subprocess and
exposes the same HTTP endpoint on the same port. The external
interface is identical — the UI connects to `pod:4000/acp`
regardless of which runtime is inside.

The bridge is a thin Go HTTP server (~200-300 lines) that relays
JSON-RPC bidirectionally between stdio and HTTP/SSE. The
`coder/acp-go-sdk` (v0.13.5) provides all ACP types and protocol
logic for the Go implementation.

### Future: Controller-side ACP for richer status

If richer AgentRun status is needed (tool call counts, token usage,
current activity), the controller could connect to agent pods via
ACP SSE and project events onto the CR. Go handles 1000+
concurrent goroutines trivially, so scalability is not a concern.
This is deferred because the simple model (controller watches CRs,
UI connects to pods) is sufficient for the POC and avoids making
the controller stateful.

## Alternatives Considered

### Controller as single ACP client, UI connects to controller

The controller maintains SSE connections to all running agents,
buffers conversation history in memory, and serves a WebSocket
endpoint for UI clients. The UI connects to the controller, not
to agent pods.

Rejected because: adds significant complexity to the controller
(in-memory state, WebSocket server, connection management,
restart recovery). The simpler model — controller watches CRs,
UI connects to pods directly — achieves the same outcome with
less moving parts. The controller stays a standard stateless
reconciler.

### Harness projects status to AgentRun CR via SA token

The harness speaks ACP-over-stdio to the agent, reads events, and
writes status updates to the AgentRun CR using a ServiceAccount
token and the Kubernetes API.

Rejected because: this requires building ACP event processing in
the harness, does not enable human-in-the-loop, and the controller
gets status updates but no external consumer can interact with the
agent in real time. The harness does write a single final status
update (push result) via SA token, but streaming observability
comes from the UI connecting directly to the ACP endpoint.

### ACP multiplexer service

A central service that all agent pods connect to, multiplexing ACP
streams.

Rejected because: adds a new stateful service to build, deploy,
and operate. Single point of failure. Unnecessary complexity when
the UI can connect to pods directly.

## Consequences

- **Goose dependency for POC**: The POC depends on `goose serve`
  for the HTTP transport. Other runtimes require the harness bridge.
  This is acceptable because Goose is the primary runtime and the
  bridge is additive (not a rewrite).

- **ACP HTTP transport is draft**: The Streamable HTTP & WebSocket
  Transport RFD is merged but not yet ratified as part of the core
  ACP spec. The reference implementation in Goose is Phase 2 of 4.
  Pin the Goose version to mitigate. We commit to contributing to
  the ACP spec where gaps affect our use case.

- **UI-to-pod networking**: The UI needs to reach agent pods. This
  requires Hub to proxy WebSocket connections to the pod's Service
  DNS, or an ingress route. Hub proxying to a pod Service is
  similar to the existing `ServiceHandler.Forward` pattern but for
  WebSocket traffic.

- **Human-in-the-loop enabled from day one**: The architecture
  supports interactive agent sessions from the POC. The UI can
  connect to the ACP endpoint and interact with agents without
  any controller changes. This is a significant differentiator —
  ACP-native interactive agents in Kubernetes.

- **Controller stays simple**: Standard stateless reconciler.
  No persistent connections, no in-memory buffering, no WebSocket
  server. Lifecycle tracking via CR watches only.

- **Known Goose issues**: O(n^2) SSE rendering performance on long
  outputs (goose#10075). Acceptable for POC; needs fixing for
  production workloads.
