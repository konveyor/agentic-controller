---
adr: "0021"
title: "Shared Runtime and Conditional Lightspeed Execution"
description: "Reuse Lightspeed's DeepAgents runtime within the Konveyor harness, independently of conditional operator adoption."
status: accepted
status_note: "Approves Konveyor direction only; implementation, backend qualification, and Lightspeed agreement are separate."
date: "2026-10-20"
last_updated: null
authors:
  - "David Zager"
last_reviewed: "2026-10-20"
implementation_status: deferred
review_note: "Runtime reuse and custom-image execution were demonstrated separately. Production runtime integration, a selectable execution backend, and OpenShell compatibility remain unimplemented."
relates_to:
  - "0001"
  - "0004"
  - "0007"
  - "0016"
  - "0020"
---

# ADR 0021: Shared Runtime and Conditional Lightspeed Execution

## Context

Konveyor needs reusable agent capabilities, independently composed workflows,
and reliable Hub/Git preparation and finalization. Sharing implementation with
Lightspeed can reduce duplicated runtime work without requiring us to surrender
those capabilities or wait for agreement on the whole product architecture.

The investigation established two separate feasibility results: Lightspeed's
operator ran a Konveyor-owned harness image, and an experimental Konveyor harness
reused Lightspeed's DeepAgents runtime under direct Agent Sandbox execution.
Neither demonstrates a production integration or the combined path. Adding
Lightspeed's operator also retains Agent Sandbox underneath; it does not itself
demonstrate lower maintenance cost, stronger isolation, or better performance.

## Decision

**Target Lightspeed's DeepAgents runtime within the Konveyor harness.** Keep
required Hub application/repository resolution, workspace and instruction
preparation, harness-owned Git push, token cleanup, and final outcome reporting.
The shared runtime supplies model, tool, skill, and MCP execution. Develop a
supported reusable interface with Lightspeed rather than productizing the
prototype's extraction of internal graph-construction code.

**Treat runtime reuse and operator adoption as independent choices.** The MTA
8.4 target can use the shared runtime under our existing controller and direct
Agent Sandbox. Adopting the Lightspeed operator additionally requires upstream
support for invocation-selected workload images with image-owned startup, plus
an agreed custom-workload input, completion, and lifecycle contract. The exact
selector field is joint design work. We will not ship a patched Lightspeed
operator. If that upstream capability is unavailable for 8.4, defer operator
adoption and continue the runtime path. Broader roadmap commitment is not a
prerequisite for that release path.

**Preserve capabilities while allowing their implementation to converge.**
Konveyor still needs reusable image/skill/tool capability definitions, concrete
invocations, and independently composed workflows. Lightspeed already provides
reusable Agent model/prompt configuration; that is useful but does not replace
all of these capabilities. The exact Agent, AgentRun, AgentWorkflow, and
AgentWorkflowRun names and schemas are negotiable. Retire our implementation
only when a shared implementation actually supplies the required behavior.

**Use one core workload and outcome contract across execution paths.** Direct
Agent Sandbox, Lightspeed, and OpenShell are portability targets. Backend
facilities are additive: a Lightspeed execution can publish its required
Kubernetes Result, while core work and outcome reporting remain usable without
that resource elsewhere. If work and push succeed but required backend
publication fails, preserve the completed work and its core outcome and report
the integration failure. The invocation must not silently become fully
successful. This does not choose a new outcome enum; that belongs to ADR 0020.

**Keep the controller domain-agnostic and stateless.** It reconciles Kubernetes
resources, resolves generic selections, and reflects lifecycle and outcomes.
It does not interpret Hub application data, connect to ACP, or retain streaming
state. The harness owns workload preparation/finalization; clients connect to
the workload for ACP. Where backend integration code lives remains an
implementation choice, subject to these behavioral boundaries.

**Pursue retained conversation sessions as an independent runtime improvement.**
A prompt finishing must be distinguishable from workload finalization so the
same live session can accept a follow-up. A server, local stdio, or embedded
session interface could provide this; no transport or native Lightspeed ACP
implementation is selected here. Same-session continuation is distinct from a
later workflow stage consuming committed handoff files. Reconnect to a live
session is also distinct from recovery after process loss.

Primary use cases are the preview priority. A one-hour budget per step and
restarting rare interrupted work from a fresh session are acceptable. Retained
multi-prompt sessions are an important improvement, not an additional gate for
the initial unattended-work/passive-observation path. ask_user, steering, safe
cancellation, post-completion sessions, and durable checkpoint recovery remain
separate work. Normal finalization revokes Hub tokens; existing short-lived
tokens bound rare abrupt loss.

## Consequences and boundaries

Runtime reuse can deliver a useful near-term convergence result even while
Konveyor retains its controller and capability/workflow resources. Longer-term
operator adoption should transfer responsibilities we do not want to maintain,
such as platform integration/support. That ownership transfer is a goal to
negotiate, not a demonstrated benefit or an upstream commitment. Organizational
alignment can justify near-term work without claiming immediate code removal.

The common workload contract must avoid a separate Lightspeed-only mechanism
for publishing core results. Each backend may require additional integration;
none of the three paths is qualified merely by naming it as a portability goal,
and qualifying all three is not a requirement for 8.4.

This ADR supersedes only ADRs 0004/0016's deferred assumption that OpenShell is
the predetermined execution destination and that adopting it automatically
removes Konveyor Gateway/provider configuration. OpenShell integration remains
a separate investigation. The current Gateway selection contract and direct
Agent Sandbox default in ADR 0016 remain in force. Agent Sandbox remains a
dependency, directly today and transitively in the measured Lightspeed path.
This ADR does not change an API, default runtime, or execution backend.

## Evidence and follow-up

The [preserved reference][research] contains prototype sources, a concise
findings summary, source pins, reproduction guidance, and links to the separate
operator probe. Full historical reports and operational records remain local. The measured boundaries are:

- Operator `e195a026588c6db674e573a6bd4cff2b47e0e55d` ran the real harness,
  Hub/Git, and Goose using a **global** image override. Per-invocation selection
  and heterogeneous-image concurrency were not demonstrated.
- Sandbox runtime `325121ba39db810b1e98f38e5a1ca8dc02fecdef` was reused under
  direct Agent Sandbox with skills/MCP and verified Git output. The October 19
  passive ACP demo retained three attempts: an observer-startup failure, an
  inspection-blocked workload, and one combined success. No combined
  shared-runtime/Lightspeed-operator product path was tested.
- Checkpoint-assisted continuation and fresh-process committed handoff each
  passed three bounded samples; first-party ACP multi-prompt behavior was
  measured separately. The live passive adapter accepts one owner prompt.
  These do not establish a production continuation interface or reliability.
- The ACP prototype kept model-boundary inspection but bypassed the provider's
  delayed approved-result display. Inspection/display behavior, typed failure
  propagation, accounting, and cancellation need further design and validation.
- OpenShell v0.1.3 `e1f3c82caa3ed3b65de22889ae7ef32a774878ef` retains its
  gateway and provider credential isolation after removing managed inference
  routing. No Konveyor/OpenShell deployment was tested. See the factual
  amendments in ADRs 0004 and 0016.

Review and merge this decision internally in `konveyor/agentic-controller`.
Then approach Lightspeed separately with concrete follow-up work: a supported
runtime boundary and retained-session contribution, and the upstream
per-invocation image capability for conditional operator adoption. Intended
MTA/OpenShift deployment, namespace/credential ownership, completion mapping,
and product ACP routing need delivery design. OpenShell integration follows its
own issue. An enhancements-repository proposal is not part of this step.

- [Convergence investigation](https://redhat.atlassian.net/browse/MTA-7977)
- [Sprint convergence proposal](https://redhat.atlassian.net/browse/MTA-8048)
- [Runtime/design investigation](https://redhat.atlassian.net/browse/MTA-7984)
- [Decision and brief review](https://redhat.atlassian.net/browse/MTA-7985)
- [OpenShell factual corrections](https://redhat.atlassian.net/browse/MTA-7980)

[research]: https://github.com/djzager/agentic-controller/blob/a2c2bfe99d8afb6a22076f35377d9aa1c5ea4995/docs/research/lightspeed-runtime-2026-10.md
