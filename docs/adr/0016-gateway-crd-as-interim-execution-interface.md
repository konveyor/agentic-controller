---
adr: "0016"
title: "Gateway CRD as Interim Execution Interface"
description: "Defines the current Gateway CRD and direct Agent Sandbox path while OpenShell remains the deferred execution target."
status: accepted
status_note: "Supersedes two clauses of ADR 0004; ADR 0004's OpenShell end-state remains deferred."
date: "2026-08-17"
last_updated: null
authors:
  - "David Zager"
last_reviewed: "2026-08-31"
implementation_status: in-sync
review_note: "Accepted current state: Gateway CRD, direct Agent Sandbox creation, verification Jobs, and provider-specific credential injection. OpenShell remains deferred."
---

# ADR 0016: Gateway CRD as Interim Execution Interface

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
  LLM credentials in its environment. This is the exposure OpenShell's
  privacy router is meant to close, accepted as a dev-preview limitation.
- **The Gateway controller verifies connectivity** by running a Job that
  probes `<endpoint>/v1/models` with the resolved credential, surfacing the
  result as `status.connectionVerified`.
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
  exists in the interim design. This is a known dev-preview limitation that
  OpenShell adoption is expected to close; hardening it further before then
  is out of scope for this ADR.
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
