# ADR 0003: Hub Curated API for Agent Resources

**Status:** Accepted
**Date:** 2026-06-30
**Authors:** David Zager

## Context

The agentic platform controller introduces seven CRDs under the
`konveyor.io` API group (SkillCard, SkillCollection, LLMProvider,
Agent, AgentRun, AgentPlaybook, AgentPlaybookRun). The UI needs to
create, read, update, and delete these resources. The question is
how the UI accesses them.

Hub is the existing API gateway for the Konveyor UI. All UI traffic
flows through Hub today — application inventory, analysis results,
identities, business services. Hub provides authentication, RBAC,
and a consistent REST API surface.

The agent CRDs live in the Kubernetes API (etcd), not in Hub's
database. Hub needs a way to serve these resources to the UI.

## Decision

### Curated Hub REST endpoints for all agent resources

Hub exposes purpose-built REST endpoints for agent resources. Hub
reads and writes CRDs using its controller-runtime client
(`h.Client(ctx)`, available on `BaseHandler`) and converts between
k8s CRD format and Hub's REST format.

This follows the existing pattern established by `AddonHandler`
(reads Addon CRDs via controller-runtime) and `ConfigMapHandler`
(reads ConfigMaps via controller-runtime), extended with
Create/Update/Delete operations.

**POC resources (five handlers):**

| Hub Endpoint | CRD | Operations |
|---|---|---|
| `/hub/agents` | Agent | List, Get, Create, Update, Delete |
| `/hub/skillcards` | SkillCard | List, Get, Create, Update, Delete |
| `/hub/skillcollections` | SkillCollection | List, Get, Create, Update, Delete |
| `/hub/llmproviders` | LLMProvider | List, Get, Create, Update, Delete |
| `/hub/agentruns` | AgentRun | List, Get, Create, Delete |

Each handler is ~150 lines following Hub's standard pattern:
struct embedding `BaseHandler`, `AddRoutes()` with `Required(scope)`
middleware, and CRUD methods that use `h.Client(ctx)` for k8s API
operations and resource converters for format translation.

### AgentRun creation: Hub resolves application metadata

The AgentRun create endpoint is a smart endpoint, not a thin proxy.
The UI sends a minimal request:

```json
POST /hub/agentruns
{
  "agent": "java-migration-agent",
  "application": 123,
  "models": [
    {
      "role": "primary",
      "provider": "anthropic-provider",
      "model": "claude-sonnet-4-20250514"
    }
  ],
  "instructions": "Migrate from Java EE 7 to Quarkus 3.x."
}
```

Hub receives this and:

1. Looks up application 123 in its database — gets git URL, source
   branch
2. Looks up the application's Identity with `role: "source"` — gets
   the credential Secret name for git read
3. Looks up the application's Identity with `role: "asset"` — gets
   the credential Secret name for git write (falls back to source
   identity if no asset identity exists)
4. Generates a target branch name
   (`konveyor/migrate-app-123-<timestamp>`)
5. Builds the full AgentRun CR with all params and envFrom filled in
6. Creates the CR via `client.Create()`
7. Returns the created AgentRun in Hub's REST format

The same endpoint also supports direct param specification (no
application ID) for CLI and GitOps use cases where the caller
supplies raw params instead of an application reference.

| Field | Required | Notes |
|---|---|---|
| `agent` | Yes | Agent CR name |
| `application` | No | If provided, Hub resolves git URLs and credentials |
| `models` | Yes | Provider/model selections |
| `instructions` | No | Task-specific instructions |
| `params` | No | Override or supplement Hub-resolved params |
| `env` / `envFrom` | No | Additional env vars beyond what Hub resolves |

### Request-driven reads, no informer cache

All reads are request-driven. When the UI calls `GET /hub/agents`,
Hub calls `client.List()` against the k8s API in real-time, converts
the response, and returns it. No watching, no caching.

This matches the existing `AddonHandler` and `ConfigMapHandler`
pattern. The number of agent resources in a cluster is small (tens,
not thousands). Watch-based caching with informers is an
optimization deferred until read performance becomes a concern.

### Hub proxies ACP WebSocket to agent pods

When the UI needs real-time streaming from a running agent, Hub
proxies a WebSocket connection to the agent pod's ACP endpoint.

**Flow:**

1. UI creates an AgentRun via `POST /hub/agentruns`
2. Hub returns the AgentRun in Hub's REST format
3. UI polls `GET /hub/agentruns/:name` until `phase: Running`
4. UI requests WebSocket upgrade via
   `GET /hub/agentruns/:name/stream`
5. Hub resolves the Sandbox Service DNS from the AgentRun status
   (`<sandbox-name>.<namespace>.svc:4000/acp`)
6. Hub proxies the WebSocket connection to the pod
7. UI receives ACP events (streaming text, tool calls, permission
   requests) and can send responses (permission approvals,
   cancellation)

The ACP `session/load` method replays full conversation history on
connect, so the UI can join a running session at any time without
missing messages.

Hub authenticates the WebSocket proxy request using the agent pod's
`X-Secret-Key` (stored on the AgentRun status or a referenced
Secret). The UI authenticates to Hub via its normal auth mechanism.

### Dependency chain

```text
konveyor/agentic-controller
  → api/v1alpha1/*_types.go (CRD Go types)
  → publish Go module

konveyor/tackle2-hub (after types are published)
  → import CRD types
  → build curated handlers
  → AgentRun create resolves app metadata

konveyor/tackle2-ui (can start when Hub API contract is agreed)
  → build against Hub REST endpoints
  → Hub-shaped responses, not k8s-shaped
```

Streams 2 and 3 can overlap since the Hub API contract (endpoint
paths, request/response shapes) can be agreed upfront from the CRD
type definitions.

## Alternatives Considered

### Direct k8s API via ingress proxy

The UI constructs k8s API URLs
(`/k8s/apis/konveyor.io/v1alpha1/agents`) and an ingress proxy
routes them to the k8s API server. This is how the POC prototype
branch (dymurray/tackle2-ui) currently works.

Rejected because: the UI receives raw k8s API responses (with
`metadata.managedFields`, `metadata.resourceVersion`, etc.), must
handle k8s error conventions (Status objects, 409 Conflict), and
must assemble the full AgentRun CR client-side including resolving
application metadata. Hub adds no value in this model. Two API
surfaces (Hub for apps, k8s for agents) with different conventions
is a poor UX for the UI team.

### Hub passthrough via ServiceHandler.Forward

Hub proxies raw k8s API requests to the k8s API server using
`httputil.ReverseProxy`. The UI sends k8s-shaped requests to Hub,
Hub forwards them unchanged.

Rejected because: Hub becomes a dumb proxy — adds latency without
adding value. The UI still receives k8s-shaped responses. Hub
cannot validate inputs with business logic, cannot resolve
application metadata, and cannot join agent data with application
data. Worse than both alternatives (direct k8s is simpler; curated
API is more useful).

### Hub database-backed agent resources

Agent resources are stored in Hub's database (GORM models) instead
of as CRDs. The UI talks to Hub's standard CRUD endpoints.

Rejected because: agent resources need to be Kubernetes-native for
GitOps, kubectl, RBAC, and controller-runtime watch semantics. The
controller reconciles CRDs, not database rows. Storing in Hub's
database would require syncing between Hub and the cluster, adding
complexity without benefit. The curated API gives the UI a
Hub-shaped experience while keeping the resources as CRDs.

### Curated API for AgentRun only, passthrough for others

Only the AgentRun endpoint gets the smart resolution logic. Other
resources (Agent, SkillCard, LLMProvider) are accessed via k8s API
passthrough since they don't benefit from Hub resolution.

Rejected because: consistency. The UI should talk to one API
surface with one response format. The marginal cost of a read-only
handler (~100 lines each, mechanical pattern) is small. Mixing two
API conventions in the UI creates unnecessary complexity.

## Consequences

- **Hub imports from agentic-controller**: Hub's Go module depends
  on `konveyor/agentic-controller/api/v1alpha1` for CRD types.
  The controller repo must publish its module before Hub handlers
  can be built.

- **Hub handler maintenance**: Every CRD field change requires
  updating the Hub resource converter. This is the cost of a
  curated API vs raw passthrough. Mitigated by the mechanical
  nature of the converters — they are straightforward field mapping.

- **WebSocket proxy in Hub**: Hub needs to proxy WebSocket
  connections to agent pods. Go's standard library and
  `httputil.ReverseProxy` support WebSocket upgrade natively.
  This is new capability for Hub but a well-understood Go pattern.

- **Application resolution coupling**: The AgentRun create endpoint
  couples Hub's application/identity model to the AgentRun param
  model. If the CRD params change, the resolution logic in Hub
  changes. This is acceptable because Hub is the system that knows
  about applications — the coupling is inherent to the value Hub
  provides.

- **UI simplification**: The UI sends minimal requests and receives
  clean responses. No k8s API conventions, no client-side metadata
  resolution, no dual-API-surface complexity. One REST API for
  everything.
