---
adr: "0016"
title: "Gateway CRD as Interim Execution Interface"
description: "Defines the Gateway CRD and direct Agent Sandbox path as the execution interface. Originally framed as interim pending OpenShell; now permanent."
status: accepted
status_note: "Supersedes two clauses of ADR 0004. The OpenShell end-state ADR 0004 deferred to was deleted upstream, so 'interim' in the title is historical — the Gateway CRD is permanent."
date: "2026-08-17"
last_updated: "2026-10-13"
authors:
  - "David Zager"
last_reviewed: "2026-10-13"
implementation_status: amended
review_note: "Accepted current state: Gateway CRD, direct Agent Sandbox creation, verification Jobs, and provider-specific credential injection. OpenShell adoption remains deferred, but the privacy router it deferred to was deleted upstream — the LLM credential exposure in Consequences is now unmitigated and unscheduled."
---

# ADR 0016: Gateway CRD as Interim Execution Interface

**Update (2026-09-01):** `Agent.spec.gateways` becomes optional
(`MinItems=1` → `MinItems=0`). The gateway list is reframed as a
**presence-gated curation constraint**: when an Agent lists gateways the
controller still enforces membership (a run's gateway must be one of them,
so an architect can lock an Agent to a single gateway — this refines
"An AgentRun selects exactly one Gateway" below); when the list is empty
the controller constrains nothing — the run must name a gateway itself,
which must exist and be `Ready`, and what an empty list means to a user
(not-runnable vs. free-choice) is a Hub/UI concern. To surface that state
the Kubernetes-native way, the controller sets a `GatewayConfigured`
condition on the Agent (`False`/`NoGatewaysDeclared` when the list is empty,
`False`/`GatewaysNotReady` when a declared gateway is not `Ready`, else
`True`/`GatewaysReady`) — distinct from `Ready`, so an Agent with no gateways
is still a valid, `Ready` template. This keeps the controller
domain-agnostic and lets curated default Agents ship without knowing a
customer's Gateway name at build time. `AgentRun.spec.gateway` and the
`Agent.spec.gateways` / `AgentRun.spec.gateway` stable seam are otherwise
unchanged; AgentWorkflow needs no change. Implementation tracked in #209.

**Update (2026-10-13): "interim" is no longer accurate, and the credential
exposure below has lost its planned remediation.**

OpenShell v0.1.0 ([NVIDIA/OpenShell#3195][os-3195], closing
[#3172][os-3172]) removed `inference.local`, the workspace-global inference
route, and the `openshell-router` ("privacy router") crate. ADR 0004 carries
the full correction.

Three parts of this ADR change meaning as a result:

1. **The credential exposure is unmitigated and unscheduled.** Consequences
   says LLM credentials reaching the agent environment is "a known dev-preview
   limitation that OpenShell adoption is expected to close." That expectation
   is void — the privacy router was deleted, and OpenShell shipped nothing in
   its place. The exposure is real, present, and now has **no remediation on
   any roadmap**. It needs its own hardening decision rather than a pointer to
   someone else's. "Hardening it further before then is out of scope" was
   reasonable when "then" existed; it no longer does.
2. **The Gateway CRD is permanent, not interim.** The title's word "interim"
   and the framing throughout both assumed a successor. There is none:
   OpenShell provider profiles have no model field, so model selection has no
   home in the substrate. This strengthens the decision rather than weakening
   it — Gateway should be treated as stable API, and `GatewaySpec.Provider`'s
   documented removal trigger (corrected in the same change as this
   amendment) could never have fired.
3. **"One gateway = one model" has lost its stated rationale.** It was
   justified by OpenShell's routing model. It survives on its own merits: a
   Gateway that serves exactly one provider/model pair is what lets an Agent
   select a model by reference without the controller interpreting a model
   name, which ADR 0002 and ADR 0013 require.

Unaffected: the accepted implementation itself. Direct `agents.x-k8s.io`
Sandbox creation, verification Jobs, presence-gated gateway curation, and the
`Agent.spec.gateways` / `AgentRun.spec.gateway` seam all stand unchanged.

**Not reopened here.** All four deferral blockers cited below
([#1722][os-1722], [#1795][os-1795], [#1977][os-1977], [#2270][os-2270]) are
closed and OpenShell is a CNCF Sandbox project with three Red Hat maintainers.
Flipping the deferral still requires the SDK to be GA, team buy-in, and a
superseding ADR — a separate effort, deliberately not decided here.

[os-3195]: https://github.com/NVIDIA/OpenShell/pull/3195
[os-3172]: https://github.com/NVIDIA/OpenShell/issues/3172
[os-1722]: https://github.com/NVIDIA/OpenShell/issues/1722
[os-1795]: https://github.com/NVIDIA/OpenShell/issues/1795
[os-1977]: https://github.com/NVIDIA/OpenShell/issues/1977
[os-2270]: https://github.com/NVIDIA/OpenShell/issues/2270

> Numbering note: an earlier PR proposed 0016 (harness git-backend
> migration) and 0017 (goose credential isolation). Both were nacked and
> never merged, so 0016 is free and reused here.

## Context

ADR 0004 accepted OpenShell as the execution interface and, as part of that
decision, made three commitments that the shipping code does not honour:

- **No Gateway CRD** — gateways would be admin-managed OpenShell Gateway
  *Services*, discovered by the controller, not modelled as a custom
  resource.
- **LLMProvider is removed** and **the controller becomes an OpenShell
  gateway API client** — creating sandboxes through the OpenShell Go SDK
  rather than creating `agents.x-k8s.io` Sandbox CRs directly.
- Inference routing and credential isolation move to OpenShell's privacy
  router.

None of this landed, deliberately. OpenShell is alpha, "single-player
mode," and its Go SDK (NVIDIA/OpenShell#2270) is not yet GA. Adopting it now
would take a hard dependency on unreleased, pre-1.0 software for the core
execution path, and the team has not committed to that direction. We are
waiting on both the SDK reaching GA and explicit buy-in before moving.

In the meantime the platform still needs a way to declare which LLM a run
uses. Issue #79 filled that gap by renaming `LLMProvider` to `Gateway` and
reshaping it to one provider/model per CR, described in code and in
CONTEXT.md as a "pre-OpenShell shim." That decision was never recorded in an
ADR, and the one ADR that mentions a "Gateway" (0004) explicitly says not to
build a CRD for it. A future reader hits a direct contradiction between the
accepted ADR set and both the code and the glossary. This ADR records the
decision that was actually made.

## Decision

**The `Gateway` CRD is the execution/inference-config interface for now.**
It is a Konveyor custom resource, not an OpenShell Gateway Service.

- **Gateway is a namespaced CRD.** Each Gateway declares exactly one
  `provider`, one `endpoint`, a `credentialRef` (Secret name + optional
  key), and one `model` (`name`, `contextWindow`, optional `tier`). One
  provider/model per Gateway.
- **The controller creates `agents.x-k8s.io` Sandbox CRs directly** and
  retains its RBAC for that API group and its `sigs.k8s.io/agent-sandbox`
  Go module dependency. It does **not** use the OpenShell Go SDK.
- **The controller injects LLM credentials into the sandbox** via
  `KONVEYOR_LLM_*` env vars (and `envFrom` for keyless multi-var
  credentials). There is no privacy router; the agent process can read the
  LLM credentials in its environment. Accepted as a dev-preview limitation.
  (Amended 2026-10-13: the OpenShell privacy router that was to close this
  was deleted upstream. See the Update above — the exposure now has no
  planned remediation.)
- **The Gateway controller verifies connectivity** by running a Job that
  probes `<endpoint>/v1/models` with the resolved credential, surfacing the
  result as `status.connectionVerified`. Providers reached through a cloud
  SDK rather than an HTTP endpoint (`aws-bedrock`, `gcp-vertex-ai`) have no
  such surface to probe, so they are marked `Ready` without one, with
  `reason=VerificationSkipped` and `connectionVerified` left false.
- **An AgentRun selects exactly one Gateway.** Multi-model-per-run stays
  dropped — this half of ADR 0004 (and the retirement of
  `KONVEYOR_MODEL_<ROLE>_*`) is honoured, not superseded.
- **`Gateway.spec.provider` is an explicit shim.** When OpenShell's
  `inference.local` lands, provider-specific credential mapping goes away
  and the field is removed.

### What this supersedes in ADR 0004

Only these clauses:

- "**No Gateway CRD.**" — There is a Gateway CRD.
- "**LLMProvider is removed** … **the controller becomes an OpenShell
  gateway API client**" and "the controller stops creating Sandbox CRs
  directly." — The rename to Gateway replaces LLMProvider, and the
  controller still creates Sandbox CRs directly using the Agent Sandbox
  module.

### What ADR 0004 keeps

- OpenShell remains the intended execution interface. The end-state — the
  controller creating sandboxes through OpenShell, the supervisor owning
  credential isolation and inference routing, `inference.local` removing
  provider-specific credential mapping — stands as the deferred target.
- One gateway = one model = one run.

### Revisiting OpenShell — two separate bars

**Evaluating OpenShell behind a non-default feature flag is sanctioned now**
and does not require superseding this ADR. The OpenShell Go SDK is integrated
in-tree in NVIDIA/OpenShell (first beta ~mid-September 2026) and its
maintainers have invited integration feedback. A controller execution-backend
feature flag — **default off** — that creates sandboxes through the OpenShell
Go SDK is exploratory work this ADR permits. It does not change the accepted
default below. This is the path taken by the spike in #144.

**Flipping the default to OpenShell is a separate decision that still needs a
new ADR.** Making OpenShell the execution interface — and removing the Gateway
CRD and `agents.x-k8s.io` RBAC (the ADR 0004 end-state, tracked in #144) —
requires the SDK to be GA, team buy-in, and a superseding ADR. Until that
lands, the Gateway CRD is the accepted default and this ADR is the accepted
state.

### Naming

To keep the docs unambiguous: **"Gateway CRD"** (or just "Gateway") is this
Konveyor custom resource. OpenShell's is the **"OpenShell Gateway Service."**
They are different things that ADR 0004 unfortunately calls by the same word.

## Alternatives Considered

### Adopt OpenShell now (implement ADR 0004 as written)

Rejected for this phase: the Go SDK is not GA, OpenShell is alpha
single-player software, and there is no team commitment to the dependency.
Building the core execution path on unreleased software is the risk we are
choosing not to take yet.

### Leave ADR 0004 accepted and record nothing

Rejected. It leaves the accepted ADR set self-contradicting — 0004 says "no
Gateway CRD" while the code ships one — which is exactly the divergence that
forced this review. Recording the real decision is the point.

### No CRD; discover gateways as Services now (0004's model minus OpenShell)

Rejected. Without OpenShell nothing injects the supervisor or privacy
router, so we would take on Service discovery and lose every benefit that
justified it, and still need connectivity validation and credential
injection somewhere. The CRD is the simpler interim shape.

## Consequences

- **LLM credentials reach the agent environment today.** No privacy router
  exists in this design. Originally recorded as a dev-preview limitation that
  OpenShell adoption was expected to close, with further hardening out of
  scope. **Amended 2026-10-13: that remediation no longer exists** — the
  `openshell-router` crate was deleted in OpenShell v0.1.0 and nothing
  replaced it. The exposure is unmitigated and unscheduled, and needs a
  hardening decision of its own rather than a pointer to OpenShell.
- **Gateway hardening is live work, not throwaway.** Because the Gateway CRD
  is the accepted interim interface, issues against it (e.g. verification
  Job egress/SSRF #102, 401/403 handling #101, watching credential Secrets
  #103) are real and worth doing, not deferred pending OpenShell.
- **The controller keeps its `agents.x-k8s.io` RBAC and Agent Sandbox
  module dependency** — the removal ADR 0004 called for does not happen yet.
- **The Agent/AgentRun API is the stable seam.** `Agent.spec.gateways` and
  `AgentRun.spec.gateway` name a gateway; whether that resolves to a Gateway
  CRD (today) or an OpenShell Gateway Service (later) is a controller-
  internal change behind an unchanged API, so the eventual OpenShell move
  does not churn Agent or AgentRun authors.
- **CONTEXT.md already matches this ADR** — its Gateway entry describes the
  pre-OpenShell shim — so no glossary change is required, only this record.
