---
adr: "0004"
title: "OpenShell as Execution Interface"
description: "Records the deferred OpenShell execution interface, with corrections to its upstream inference-routing assumptions."
status: accepted
status_note: "OpenShell adoption remains deferred. The managed inference route was removed, but the gateway and credential isolation remain. ADR 0016 supersedes the no-Gateway-CRD and immediate OpenShell-client clauses."
date: "2026-07-20"
last_updated: "2026-10-20"
authors:
  - "David Zager"
last_reviewed: "2026-10-20"
implementation_status: amended
review_note: "Rechecked against OpenShell v0.1.3. Managed inference routing was removed; the gateway, sandbox lifecycle API, and provider credential isolation remain. Implications for Konveyor's adoption design remain open for discussion."
---

# ADR 0004: OpenShell as Execution Interface

**Update (2026-10-20): correction of an earlier proposed amendment — managed
inference routing was removed; the gateway and credential isolation remain.**

An earlier proposed amendment ([closed PR #271](https://github.com/konveyor/agentic-controller/pull/271)) incorrectly concluded that deleting the privacy-router
crate deleted credential isolation, and that the OpenShell gateway no longer
existed. This correction replaces those conclusions, preserving the original
design below as historical context. Evidence is pinned to OpenShell v0.1.3
(`e1f3c82caa3ed3b65de22889ae7ef32a774878ef`).

[PR #3195][os-3195] merged on 2026-09-09; [v0.1.0][os-release] shipped on
2026-09-25. It removed `inference.local`, workspace-global managed inference
routes, and `openshell-router`. The [replacement access path][os-inference]
uses attached providers: workloads call native APIs with placeholder
credentials, and the sandbox proxy substitutes real values after policy and
credential endpoint checks. It no longer selects or rewrites the model.

| Original assumption | Verified upstream behavior |
| --- | --- |
| An OpenShell gateway serves one provider/model | The gateway manages workspaces, sandboxes, providers, and policy. A provider attachment grants access; the workload selects the model. |
| A neutral inference route removes runtime-specific configuration | The harness/runtime still needs native endpoint, provider, model, and request configuration. OpenShell can keep the real credential out of that workload. |
| OpenShell adoption removes Konveyor's Gateway CRD | Upstream behavior does not determine this. The future of Konveyor's provider/model selection resource remains a design question. |
| OpenShell manages sandbox lifecycle through its API | Still supported. Its Kubernetes compute driver creates Agent Sandbox resources. Konveyor currently creates them directly and does not receive OpenShell enforcement merely by sharing that dependency. |

A [provider profile][os-profiles] is a reusable service-access definition,
not a per-sandbox instance. A provider supplies concrete credentials/config;
attachment grants a sandbox access to it. Profiles have no first-class model
selection, but that does not prove Gateway is the only possible home for
model configuration or must be permanent. One provider/model per Konveyor
Gateway remains our current selection rule, not an upstream requirement.

The [gateway/supervisor architecture][os-architecture] survives. The original
single-player and fixed-namespace limitations below need reassessment against
current workspace support. Closed issue status alone does not prove delivery:
[#1795][os-1795], for example, was closed as `not_planned`. The [Go SDK][os-sdk]
exists, while the [Kubernetes Helm deployment][os-helm] is still described as
experimental. These are distinct from our SDK-GA and team-buy-in requirements.

ADR 0016's default remains Gateway CRD plus direct Agent Sandbox. Its
permission to evaluate OpenShell behind a default-off feature flag remains
valid. These are existing decisions. This amendment corrects upstream facts;
any resulting change to our adoption plans remains for discussion.

[os-3195]: https://github.com/NVIDIA/OpenShell/pull/3195
[os-3172]: https://github.com/NVIDIA/OpenShell/issues/3172
[os-1722]: https://github.com/NVIDIA/OpenShell/issues/1722
[os-1795]: https://github.com/NVIDIA/OpenShell/issues/1795
[os-1977]: https://github.com/NVIDIA/OpenShell/issues/1977
[os-2270]: https://github.com/NVIDIA/OpenShell/issues/2270
[os-release]: https://github.com/NVIDIA/OpenShell/releases/tag/v0.1.0
[os-inference]: https://github.com/NVIDIA/OpenShell/blob/e1f3c82caa3ed3b65de22889ae7ef32a774878ef/docs/how-it-works/inference.mdx
[os-profiles]: https://github.com/NVIDIA/OpenShell/blob/e1f3c82caa3ed3b65de22889ae7ef32a774878ef/docs/how-it-works/providers/profiles.mdx
[os-architecture]: https://github.com/NVIDIA/OpenShell/blob/e1f3c82caa3ed3b65de22889ae7ef32a774878ef/docs/about/architecture.mdx
[os-sdk]: https://github.com/NVIDIA/OpenShell/blob/e1f3c82caa3ed3b65de22889ae7ef32a774878ef/sdk/go/README.md
[os-helm]: https://github.com/NVIDIA/OpenShell/blob/e1f3c82caa3ed3b65de22889ae7ef32a774878ef/deploy/helm/openshell/README.md

---

The controller replaces its direct Agent Sandbox dependency with
OpenShell as the execution interface. Instead of creating
`agents.x-k8s.io` Sandbox CRs directly, the controller creates
sandboxes through the OpenShell gateway API using the OpenShell Go SDK.
This eliminates the LLMProvider CRD entirely — inference routing,
credential management, and security policy enforcement move to
OpenShell, which already solves these problems.

## Context

OpenShell (NVIDIA) is a secure runtime that layers on top of Agent
Sandbox. It provides inference routing through a sandbox-local
`inference.local` HTTPS endpoint, a privacy router that strips
sandbox-supplied credentials and injects real backend credentials, and
declarative security policies. OpenShell's Go SDK
(github.com/NVIDIA/OpenShell, in-progress) provides typed clients for
sandbox lifecycle management following client-go conventions, including
watch primitives suitable for controller use.

### Current state

The controller creates `agents.x-k8s.io` Sandbox CRs directly and
has RBAC permissions for that API group. The AgentRun controller
manages LLM credentials through the LLMProvider CRD: validates
Secrets, runs connectivity verification Jobs, and builds
`KONVEYOR_MODEL_*` environment variables to inject provider endpoints,
model names, and API keys into sandbox containers. The agent inside
the sandbox receives real LLM credentials in its environment.

### What changes

The controller stops creating Sandbox CRs directly and instead
creates sandboxes through the OpenShell gateway API using the Go SDK.
The LLMProvider CRD, its controller, and all credential plumbing are
removed. Agent references OpenShell Gateways instead of LLMProviders.
AgentRun selects a single gateway instead of provider/model/role
combinations. The controller's `agents.x-k8s.io` RBAC is no longer
needed — the OpenShell gateway owns Sandbox CR lifecycle.

### Prerequisites

- **OpenShell Go SDK** (NVIDIA/OpenShell#2270): PR A is in review,
  5 more PRs follow. Must land before implementation can begin.
- **OpenShell gateways deployed in the namespace**: Platform Admin
  installs and configures gateways via Helm + OpenShell CLI.

## Decision

**The controller becomes an OpenShell gateway API client.** The AgentRun
controller creates sandboxes through the OpenShell Go SDK instead of
creating Sandbox CRs directly. OpenShell creates the Sandbox CRs on
our behalf and injects its supervisor, which handles credential
isolation, inference routing, and policy enforcement.

**LLMProvider is removed.** The CRD, the controller, and all credential
plumbing (`buildEnvVars`, `validateModels`, verification Jobs) are
eliminated. Inference routing is OpenShell's problem — each gateway is
configured with one provider and one model by the Platform Admin.

**Agent references OpenShell Gateways, not LLMProviders.** Each
OpenShell Gateway is a Kubernetes Service deployed via Helm, configured
with exactly one provider/model combination. An Agent declares one or
more gateways (the set available for runs). An AgentRun selects one
gateway from the Agent's list. The controller validates the gateway
Service exists and creates the sandbox through it.

**No Gateway CRD.** OpenShell Gateways are admin-managed infrastructure
deployed independently. The controller discovers them as Services in
the shared namespace — it does not create, manage, or mirror them as
custom resources.

**Multi-model per run is dropped.** ADR-0001 introduced per-run model
selection with roles (e.g. primary, efficient) via
`KONVEYOR_MODEL_<ROLE>_*` env vars. OpenShell's `inference.local`
serves exactly one model per gateway, and each sandbox lives on one
gateway. Multi-role model selection within a single run is not
possible through OpenShell's routing. An AgentRun selects one
gateway = one model. The multi-role design was speculative and not
exercised in practice.

**Agent Sandbox remains a transitive dependency.** OpenShell uses Agent
Sandbox under the hood. The controller no longer imports the Agent
Sandbox Go module or creates Sandbox CRs directly. Agent Sandbox must
still be installed in the cluster as an OpenShell prerequisite.

## Deployment Model

OpenShell deploys one gateway per Helm release. Each gateway is a
StatefulSet (or Deployment with external Postgres) with its own
Service, database, TLS Secrets, and RBAC — all namespace-scoped.
Sandboxes are created in the same namespace as the gateway
(`server.sandboxNamespace` defaults to the release namespace).

**This means all OpenShell gateways must be installed in the same
namespace as our controller and Hub** (e.g. the konveyor namespace).
Multiple gateways coexist by using different Helm release names:

```sh
helm install anthropic-gw oci://ghcr.io/.../helm-chart -n konveyor
helm install openai-gw    oci://ghcr.io/.../helm-chart -n konveyor
```

Each produces a separate Service (`anthropic-gw`, `openai-gw`) that
the controller can reach without cross-namespace access. Each gateway
is fully independent — its own database, TLS material, provider
config, and inference route. They do not share state.

**This is not multi-tenancy.** OpenShell has open upstream work on
multi-tenant deployments (NVIDIA/OpenShell#1722), a Kubernetes
operator (NVIDIA/OpenShell#1719), and cross-namespace sandbox
creation (NVIDIA/OpenShell#1795). Our design does not depend on any
of these. We are running multiple independent single-user gateway
instances in one namespace — this is supported today via Helm and
does not require upstream multi-tenancy features.

OpenShell is explicit about its current scope: "Alpha software —
single-player mode. One developer, one environment, one gateway. We
are building toward multi-tenant enterprise deployments." There are
no Kubernetes CRDs for providers, inference routes, or policies —
configuration is imperative (CLI/gRPC against the gateway).

### UX impact

LLM provider configuration moves from "create an LLMProvider CR" to
"Platform Admin pre-provisions OpenShell gateways, users select from
them." The per-persona experience:

- **Platform Admin**: deploys gateways via Helm, configures providers
  and inference routing via the OpenShell CLI. This is infrastructure
  setup comparable to deploying the Agent Sandbox controller today.
- **Architect**: references available gateways by Service name in Agent
  CRDs. Does not interact with OpenShell directly.
- **Developer**: picks an Agent and a gateway, runs it. Does not
  configure providers, credentials, or OpenShell. Hub presents
  available gateways through its REST API / UI.

Hub could eventually provide a UI for managing OpenShell gateways
(creating providers, setting inference routes) so the admin does not
need the CLI. That is a Hub feature, not a controller concern, and
not in scope for this decision.

### Multi-tenancy limitations

OpenShell's namespace-scoped gateway model means all gateways, all
sandbox pods, and all our CRs live in one namespace today. This works
for our current single-namespace deployment but does not scale to
multi-tenant scenarios where teams need namespace-level isolation
between gateway configurations. Relevant upstream work:

- NVIDIA/OpenShell#1719 — Kubernetes operator (status: Idea)
- NVIDIA/OpenShell#1722 — Multi-tenant deployments (milestone:
  OpenShell Beta, status: Idea)
- NVIDIA/OpenShell#1795 — Cross-namespace sandbox CRs (open)

Multi-namespace support would require either:

- OpenShell adding cross-namespace or cluster-scoped gateway support
- Us contributing upstream to OpenShell to expand their deployment model
- Running separate controller + OpenShell stacks per namespace

This is a known constraint, not a blocker for the current phase.
Our single-namespace, multi-gateway design works today without any
upstream changes.

## Alternatives Considered

### Controller creates Sandbox CRs directly, OpenShell deployed alongside

The controller continues creating Sandbox CRs and OpenShell is an
optional operational layer. Rejected because OpenShell's supervisor is
only injected into sandboxes created through its gateway API — Sandbox
CRs created by third parties bypass OpenShell entirely. There is no
webhook or sidecar injection model, and OpenShell's architecture does
not point toward one.

### Hub as OpenShell gateway client instead of the controller

Hub already mediates between the UI and Kubernetes. It could create
sandboxes through the OpenShell gateway. Rejected because this would
hollow out the controller — its core job is reconciling AgentRun CRs
into running workloads. Moving that to Hub eliminates the controller's
reason to exist.

### New Gateway CRD to represent OpenShell Gateways

A lightweight CRD mapping gateway names to endpoints and credentials,
with a controller that validates connectivity. Rejected because it
mirrors an external concept for no reason. Gateways are admin-managed
infrastructure — the controller just needs to verify the Service
exists, not own its lifecycle.

### Keep LLMProvider alongside OpenShell

Maintain LLMProvider for credential validation while using OpenShell
for execution. Rejected because OpenShell handles credential injection
through its privacy router — there is nothing left for LLMProvider to
do. Keeping it would mean maintaining dead validation logic.

## Consequences

- The controller gains a runtime dependency on OpenShell gateways
  being deployed and reachable. This is the same shape of dependency as
  the existing Agent Sandbox requirement.
- The `sigs.k8s.io/agent-sandbox` Go module dependency is replaced by
  the OpenShell Go SDK. The controller's RBAC for `agents.x-k8s.io`
  Sandboxes is removed — the gateway owns that lifecycle.
- Hub discovers gateways by listing annotated Services and exposes
  them through its curated REST API. The UI shows available gateways
  to users.
- Multi-model profiling (running the same agent against different
  models to compare results) is supported by listing multiple gateways
  on an Agent.
- RBAC for gateway access (restricting which users can use which
  gateways) is a Hub concern, not a controller concern. The controller
  validates structural correctness only.
