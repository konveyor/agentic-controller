---
adr: "0021"
title: "Shared Runtime and Conditional Lightspeed Execution"
description: "Reuse Lightspeed's DeepAgents runtime and its inspection behavior within the Konveyor harness; evaluate operator adoption separately."
status: accepted
status_note: "Approves Konveyor direction only; implementation, backend qualification, and Lightspeed agreement are separate."
date: "2026-10-20"
last_updated: "2026-10-28"
authors:
  - "David Zager"
last_reviewed: "2026-10-28"
implementation_status: deferred
review_note: "Runtime reuse with model-boundary inspection and custom-image execution were demonstrated separately. Full runtime and ACP inspection parity, operator placement and credential delivery, and lifecycle qualification remain unresolved. Production integration is unimplemented."
relates_to:
  - "0001"
  - "0004"
  - "0007"
  - "0016"
  - "0020"
---

# ADR 0021: Shared Runtime and Conditional Lightspeed Execution

**Update (2026-10-28): scope clarification following review.** Runtime reuse
includes Lightspeed's applicable inspection and tool-admission behavior inside
Konveyor's harness. Image and entrypoint control remain firm requirements for
operator adoption. Execution placement and credential delivery return to open
design questions, superseding the namespace mandate in the earlier same-day
amendment below. The current Decision and Consequences sections reflect this
clarification; no provider mapping or Gateway API migration is selected here.

**Update (2026-10-28):** Made execution in Konveyor's Hub namespace an explicit
operator-adoption requirement alongside a workload image per invocation. The
earlier Decision named image selection but left namespace and credential
ownership to follow-up design. Both capabilities, including their combined
lifecycle and Hub connectivity, must be supported and validated before adoption.

**Update (2026-10-26):** Clarified the adoption sequence and ownership in the
Decision and follow-up sections. Existing use-case coverage gates runtime
adoption; retained-session contributions are separate. Lightspeed operator
execution is an optional backend alongside direct Agent Sandbox. Equivalent
Lightspeed resources should replace our APIs through deprecation and removal
over subsequent releases.

## Context

Konveyor needs reusable agent capabilities, independently composed workflows,
and reliable Hub/Git preparation and finalization. Lightspeed's DeepAgents
implementation supplies runtime work we can reuse: model and tool execution,
skills, MCP integration, and tool-output inspection for indirect prompt
injection. We want to share that implementation while preserving Konveyor's
capabilities and the harness's Hub/Git lifecycle.

Runtime reuse and execution-platform adoption are separate changes. Our
experiments reused Lightspeed's runtime with an experimental Konveyor harness
under direct Agent Sandbox and, separately, ran a Konveyor-owned harness image
under Lightspeed's operator. These establish bounded feasibility. They do not
establish complete runtime parity or a supported operator integration with our
deployment, credentials, lifecycle, and UI.

## Decision

**Adopt Lightspeed's DeepAgents runtime.**
Adapt our harness's invocation and configuration to reuse their existing runtime
implementation for model, tool, skill, and MCP execution, including Lightspeed's
applicable inspection and tool-admission behavior. Konveyor's harness continues
to own Hub application/repository resolution, checkout, workspace and instruction
preparation, Git push, token cleanup, and final outcome reporting.
Initial runtime reuse does not require Lightspeed to implement those Konveyor
behaviors or the retained-session improvements below.

**Preserve the runtime's inspection path when integrating it with our harness.**
Reuse Lightspeed's DeepAgents integration, including inspection before untrusted
tool results enter model context and its inspection-approved tool-result release
to clients. Inspection rejection and technical inspection failure must retain
the upstream failure behavior.

The prototype's ACP bridge preserved model-boundary inspection but bypassed
delayed tool-result release to viewers; production integration must address
that gap. This is tool-output prompt-injection inspection, not a guarantee that
every harmful command or request is prevented.

**Add the Lightspeed operator as an alternative execution backend when ready.**
Runtime reuse proceeds independently under our existing controller and direct
Agent Sandbox. **Control over the workload image and entrypoint per AgenticRun
is a firm adoption requirement.** The operator must let our selected image run
our harness and supply the runtime and toolchains. Honoring the selected image's
declared entrypoint may be sufficient to meet the startup requirement. The
integration design will determine whether an explicit command override is needed.

Before adopting the operator, resolve and qualify the custom-workload contract:

- **Placement, configuration, and credential delivery:** where the workload
  executes, how it reaches Hub, and how it receives inputs, provider/model
  configuration, and Hub/model credentials. Lightspeed's Agent/LLMProvider
  resources are cluster-scoped, but their credential Secrets and workload inputs
  are namespaced. The integration design must cover this complete access path.
- **Completion and shutdown:** input delivery, Result publication and outcome
  translation, cancellation/deadlines, ownership, and cleanup must preserve our
  required behavior, including visible publication failures after work succeeds.
- **Shared platform controls and permissions:** establish how Lightspeed's
  cluster-wide suspension, approval policy, and workload RBAC affect Konveyor.

Execution in Konveyor's Hub namespace and execution in Lightspeed's namespace
have different access and credential consequences. This ADR chooses neither;
the supported design must provide the required Hub/ACP access, credential
handling, and isolation. Retain our existing execution path while qualifying
the alternative backend. Adopt operator execution for MTA 8.4 only if upstream
capabilities and our integration are ready without delaying the release;
otherwise continue the runtime path under our controller. We will not ship a
patched Lightspeed operator. Broader roadmap commitment is not a prerequisite
for runtime reuse.

**Migrate to Lightspeed resources as they cover our required capabilities.**
Konveyor needs reusable Agent definitions covering images, skills, tools, MCP
servers, prompts, and model configuration, concrete invocations, and independently
composed workflows. Lightspeed already provides reusable Agent model/prompt
configuration; that does not yet replace all of these capabilities. As its Agent
and workflow resources cover our use cases, adopt those resources, deprecate our
equivalent APIs, and remove them in subsequent releases. The exact Agent,
AgentRun, AgentWorkflow, and AgentWorkflowRun names and schemas can change through
that migration. Resource migration is independent of initial runtime reuse and
optional operator execution; retain our implementations until their replacements
supply the required behavior.

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
implementation is selected here. Contribute the needed session-lifecycle changes
to Lightspeed's DeepAgents implementation and adapt our integration. A supported
session interface should replace the prototype's extraction of internal
graph-construction code for this work. These contributions are follow-up work,
not prerequisites for the initial use cases. Same-session continuation is
distinct from a later workflow stage consuming committed handoff files.
Reconnect to a live session is also distinct from recovery after process loss.

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

Hub currently creates and reads agentic resources and their credential Secrets
in its own namespace. Lightspeed currently resolves provider credentials and
creates workloads in its configured operator namespace. Namespace-local
execution would preserve our current Hub-side placement but needs a solution
for provider credentials. Centralized execution needs a solution for Hub inputs,
tokens, and network access. Neither is established as the supported deployment.
Qualify Hub application/credential resolution, normal token revocation, ACP
connectivity, Result publication, and cleanup under the intended network and
access policies. Disabling namespace isolation in an experiment does not
qualify either production design.

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
- That real-harness experiment used a Hub fixture in a separate namespace with
  namespace isolation disabled. A separate namespace probe reproduced invalid
  approval ownership for a run outside the operator namespace at that revision;
  it did not attempt workload execution or completion. These experiments do
  not qualify namespace-local execution or normal Konveyor network policies.
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
Then validate existing use-case coverage while integrating the existing runtime
with our harness, including inspection at both model and client-display
boundaries. Collaborate with Lightspeed on retained-session support as separate
runtime work and on workload-image/entrypoint selection for optional operator
adoption. Resolve execution placement, provider configuration and credential
delivery, Hub/ACP access, completion/shutdown behavior, and shared platform
controls before qualifying that backend. Migrate and deprecate our APIs as
equivalent resources become available and cover our requirements. OpenShell
integration follows its own issue. An enhancements-repository proposal is not
part of this step.

- [Convergence investigation](https://redhat.atlassian.net/browse/MTA-7977)
- [Sprint convergence proposal](https://redhat.atlassian.net/browse/MTA-8048)
- [Runtime/design investigation](https://redhat.atlassian.net/browse/MTA-7984)
- [Decision and brief review](https://redhat.atlassian.net/browse/MTA-7985)
- [OpenShell factual corrections](https://redhat.atlassian.net/browse/MTA-7980)

[research]: https://github.com/djzager/agentic-controller/blob/a2c2bfe99d8afb6a22076f35377d9aa1c5ea4995/docs/research/lightspeed-runtime-2026-10.md
