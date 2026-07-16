# OpenShell as Execution Interface

Status: accepted | Date: 2026-07-16

The controller replaces its direct Agent Sandbox dependency with
OpenShell as the execution interface. Instead of creating
`agents.x-k8s.io` Sandbox CRs directly, the controller creates
sandboxes through the OpenShell gateway API using the OpenShell Go SDK.
This eliminates the LLMProvider CRD entirely — inference routing,
credential management, and security policy enforcement move to
OpenShell, which already solves these problems.

## Context

The controller previously created Sandbox CRs directly via the Agent
Sandbox API and managed LLM credentials through its own LLMProvider
CRD. The LLMProvider controller validated Secrets, ran connectivity
verification Jobs, and the AgentRun controller built `KONVEYOR_MODEL_*`
environment variables to inject provider endpoints, model names, and
API keys into sandbox containers. The agent inside the sandbox received
real LLM credentials in its environment.

OpenShell (NVIDIA) is a secure runtime that layers on top of Agent
Sandbox. It provides inference routing through a sandbox-local
`inference.local` HTTPS endpoint, a privacy router that strips
sandbox-supplied credentials and injects real backend credentials, and
declarative security policies. OpenShell's Go SDK
(github.com/NVIDIA/OpenShell, in-progress) provides typed clients for
sandbox lifecycle management following client-go conventions, including
watch primitives suitable for controller use.

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

**Agent Sandbox remains a transitive dependency.** OpenShell uses Agent
Sandbox under the hood. The controller no longer imports the Agent
Sandbox Go module or creates Sandbox CRs directly. Agent Sandbox must
still be installed in the cluster as an OpenShell prerequisite.

## Considered Options

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
  the OpenShell Go SDK. The SDK is in-progress (NVIDIA/OpenShell#2270)
  and must land before this migration can be implemented.
- Hub discovers gateways by listing annotated Services and exposes
  them through its curated REST API. The UI shows available gateways
  to users.
- Multi-model profiling (running the same agent against different
  models to compare results) is supported by listing multiple gateways
  on an Agent.
- RBAC for gateway access (restricting which users can use which
  gateways) is a Hub concern, not a controller concern. The controller
  validates structural correctness only.
