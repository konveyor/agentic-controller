# Konveyor and Lightspeed discussion outline

November 11, 2026. Draft slide content for review before creating the deck.

The [discussion brief](../briefs/lightspeed-2026-11-11.md) provides the fuller
argument. The [demo plan](lightspeed-2026-11-11-demo.md) owns setup, rehearsal,
recording and evidence. This outline proposes 13 slides in one deck. It is plain
Markdown, not a rendered presentation.

Slides 1–9 target **9 minutes 30 seconds**, including a 2 minute 30 second demo
video and the invitation to give feedback. Discussion time is additional.
Slide 9 is a deliberate stopping point. Slides 10–13 are an optional operator
section to discuss immediately or read afterward, with about four minutes of
presenter material. These are pacing guides, not fixed slide-count constraints.

**Demo status:** live Coolstore planning is planned. Cluster access and provider
configuration are pending. Before rendering the deck, replace planned-demo
placeholders with the actual recording, source/image revisions, selected model,
run outcome and Git result. A failed or incomplete rehearsal must remain visible.
The earlier scripted integration recording is supporting evidence, not the
primary demonstration proposed here.

## 1. Konveyor and Lightspeed

**Time:** 20 seconds. Runtime section.

**On-slide copy**

Reusable runtime sessions for Konveyor workloads

Prototype demonstration and discussion

November 11, 2026

**Speaker intent**

Introduce the application-migration use case and the reason for meeting: show
working integration, learn whether the reusable changes are useful to the
Lightspeed community, and hear where they would prefer collaboration. This is
an invitation to review a prototype. ADR 0021 records Konveyor's direction and
does not establish Lightspeed acceptance.

**Source:** [Accepted ADR 0021](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md).

## 2. Runtime demonstration, then feedback

**Time:** 20 seconds. Runtime section.

**On-slide copy**

- What Konveyor reuses and what its harness owns
- Reusable sessions and an optional ACP adapter
- Coolstore planning demonstration and runtime feedback
- Optional discussion: operator adoption

**Speaker intent**

Explain the stopping point before the operator section. Runtime reuse already
has a path through Konveyor's controller and Agent Sandbox. Operator adoption
is a separate discussion. The group can stay with runtime feedback and read
the operator material later.

**Source:** [ADR 0021, Decision](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md#decision).

## 3. The runtime reuse boundary

**Time:** 60 seconds. Runtime section.

**On-slide copy**

- Lightspeed runtime: models, tools, skills, MCP and inspection
- Konveyor Go harness: Hub, workspace, Git push and finalization
- Konveyor controller and Agent Sandbox: workload lifecycle

**Speaker intent**

Define “harness” as Konveyor's Go entrypoint so it is distinct from the
DeepAgents implementation. Walk through an application being resolved from
Hub, prepared for the runtime, and finalized by the harness. The agent commits
locally and the harness pushes. The controller reconciles resources and stays
outside ACP. The live-demo image needs the harness, modified runtime and any
build tools required by the selected task. Runtime reuse, operator adoption and future resource migration are
independent steps. The demonstration and cluster tour use Konveyor's controller
with direct Agent Sandbox.

**Sources:** [Domain glossary](../../CONTEXT.md#infrastructure),
[ADR 0021](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md#decision),
[prototype implementation and scope][prototype].

## 4. Reusable sessions

**Time:** 75 seconds. Runtime section.

**On-slide copy**

- A session owns conversation state and model clients
- Further prompts continue the same live conversation
- Prompt completion and workload finalization are separate
- Limit-triggered handoff uses that same session

**Speaker intent**

Use the concrete wind-down example: work reaches its turn limit, then the
harness asks the same session to write a handoff before finalizing. Describe
the proposed reusable session interface, which keeps graph/checkpoint state
and model-client ownership inside the runtime. Approval and question callbacks
do not require ACP. Reconnecting a viewer, another prompt in one live session,
committed handoff to a fresh stage, and recovery after process loss have
different requirements. This prototype retains state for the runtime process's
lifetime. Extended conversations after completion and durable recovery remain
separate work.

**Sources:** [Runtime session implementation][session],
[ADR 0021, session continuation](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md#decision).

## 5. Optional ACP integration and inspection

**Time:** 75 seconds. Runtime section.

**On-slide copy**

- Optional ACP adapter connects sessions to the existing UI
- Approval, questions, steering and cancellation use session controls
- Inspection precedes tool-result release to the model and viewers

**Speaker intent**

Separate the reusable session contribution from its removable ACP adapter.
The existing harness tee supplies the workload endpoint. Tool approval asks
permission to run a tool. Ask User returns a human decision to the agent.
Steering is consumed at a safe model boundary. Cancellation must stop active
work before finalization. The modified integration preserves Lightspeed's
inspection path before untrusted tool results reach model context or viewers.
This is tool-output inspection, with its existing rejection/failure behavior.
It is not evidence that every harmful command or request is prevented. Describe
these as prototype changes available for review, not stock upstream features.

**Sources:** [Session implementation][session],
[optional ACP package][acp], [prototype inspection and interaction scope][prototype],
[ADR 0021, inspection requirement](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md#decision).

## 6. Coolstore planning demonstration

**Time:** 2 minutes 30 seconds, including video. Runtime section.

**On-slide copy**

Coolstore Java EE application: prepare a Quarkus migration plan

- Read application analysis and source
- Make a human decision and steer the work
- Inspect the resulting plan and Git changes

Live model recording planned

**Speaker intent**

Before playback, identify Coolstore and the planning task, then show the selected
Agent, actual model and Lightspeed runtime image. The source baseline is
Coolstore `mta-test` at `32c1722`; outputs belong on a separate demo branch.
Give enough time to read each interaction and its consequence. Aim to show a
question, tool approval, steering, observed usage and a committed plan, using
only interactions that actually occur in rehearsal. Explain the human decision
and locate it in the plan rather than merely showing a button click. Inspect
the plan/diff and runtime provenance at the end. Use the demo plan's final
evidence and actual outcome when narrating.

If the live recording is unavailable, state that limitation and label any
supporting scripted footage explicitly. Do not describe scripted responses as
live model reasoning or use the old 36-second clip without application/task
context and readable pacing. A cluster tour, if requested after the main
presentation, shows this runtime integration on Konveyor's controller and
Agent Sandbox. It does not show a deployed Lightspeed operator.

**Sources:** [Demo plan](lightspeed-2026-11-11-demo.md),
[Coolstore source baseline][coolstore],
[earlier planning demonstration](demo-2026-09-30-script.md#part-2-a-human-in-the-loop).

## 7. Demo evidence and limits

**Time:** 60 seconds. Runtime section.

**On-slide copy**

- Recorded result: [actual run outcome and plan commit]
- Trace each human decision to the plan
- Confirm runtime and image provenance
- Planning is one part of the required behavior

**Speaker intent**

Replace the placeholder with the observed outcome and inspect the resulting
branch and plan. Report the harness's actual verification verdict separately
from execution success. A planning result alone does not verify a migrated
application. Use provenance evidence to establish which runtime executed.
Questions, approvals and steering are demonstrated only if the recording shows
them. Cancellation and limit-triggered same-session handoff have separate
prototype evidence. Workflow execution, restart recovery and operator adoption
are not established by a planning run. Keep any failed interaction or missing
evidence explicit. No model-quality claim follows from the earlier scripted
integration tests.

**Sources:** [Demo evidence checklist](lightspeed-2026-11-11-demo.md),
[prototype verification record and limits][prototype],
[recorded source and test revisions][reference].

## 8. Follow-on runtime work: usage and cost

**Time:** 60 seconds. Runtime section.

**On-slide copy**

- Cumulative token observations are implemented
- Inspection classifier calls remain outside that coverage
- Monetary reporting and budget enforcement need follow-on work
- Konveyor consumes accounting supplied by the runtime

**Speaker intent**

Distinguish partial token reporting from complete accounting. The prototype
reports agent, subagent, summarization and shaping observations, excludes
isolated inspection classifier calls, and supplies no monetary cost. Unknown
cost remains absent. A configured `maxCost` is rejected before model execution.
Propose collaborating on complete runtime-provided usage/cost reporting and
timely updates plus cancellation for monetary budget enforcement. Konveyor
should consume those observations rather than maintain its own billing
estimator. This is follow-on work for the prototype. Preserving monetary budget
behavior remains a requirement for full runtime adoption under ADR 0021.

**Sources:** [Prototype usage and limits][prototype],
[ADR 0021, interaction and accounting requirements](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md#decision).

## 9. Runtime feedback

**Time:** 50 seconds to invite discussion. Runtime section stops here.

**On-slide copy**

- Would reusable sessions be useful to Lightspeed?
- How should we shape the session and optional ACP contributions?
- Which example would help your team evaluate them?

Code review first, then agree useful follow-up

**Speaker intent**

Stop advancing and invite feedback. Ask about community interest before
assuming contribution acceptance or a delivery schedule. Point to the review
branches and the accepted Konveyor ADR. The author will review generated code
before submitting implementation PRs. If there is interest, discuss a smaller
technical review or a possible November 18 follow-up, subject to availability.
The group can end here or continue to the operator questions.

**Sources:** [Accepted ADR 0021](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md),
[controller integration review branch][controller-review],
[runtime review branch][runtime-review], [companion UI review branch][ui-review].

## 10. Optional: workload images and startup

**Time:** 50 seconds. Optional operator section.

**On-slide copy**

Operator adoption is a separate step

- Select the workload image for each invocation
- Start its harness with the required runtime and toolchain
- Agree how Lightspeed can support that contract

**Speaker intent**

Re-establish that the runtime demonstration did not deploy the Lightspeed
operator. Konveyor needs different workload images and language toolchains
without changing a platform-wide image default. The historical custom-image
experiment used a global override, which establishes bounded feasibility.
Per-invocation image selection remains a requirement. Field placement is joint
design work. Honoring the selected image's entrypoint may be sufficient, so do
not prescribe a command override before discussing the supported startup
contract. Qualify the Agent Sandbox path for Konveyor's accepted direction.

**Sources:** [ADR 0021, conditional operator adoption](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md#decision),
[discussion brief and pinned operator assessment](../briefs/lightspeed-2026-11-11.md),
[historical operator contract probe][operator-probe].

## 11. Optional: placement and credential access

**Time:** 65 seconds. Optional operator section.

**On-slide copy**

- Where does the workload run?
- How does the workload reach Hub, and how do viewers reach ACP?
- How are provider credentials and run inputs delivered?
- Who owns access, isolation and cleanup?

**Speaker intent**

Explain the full access path rather than proposing a namespace change in
isolation. Konveyor Hub currently supplies application inputs and credentials
in its installation namespace. Lightspeed's Agent/LLMProvider resources are
cluster-scoped, while their credential Secrets are namespaced. The reviewed
operator uses its configured namespace for workload/input placement and provider
Secret lookup. Existing RequiredSecrets supports environment/file bindings.
The question is how these facilities form a supported integration with Hub,
ACP routing, network policy and cleanup. The accepted ADR chooses neither
centralized execution nor execution beside Hub. Leave placement and credential
delivery solutions open for collaboration.

**Sources:** [ADR 0021, placement and consequences](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md#decision),
[pinned operator access-path assessment](../briefs/lightspeed-2026-11-11.md).

## 12. Optional: completion, cancellation and Results

**Time:** 65 seconds. Optional operator section.

**On-slide copy**

- Coordinate runtime stop, harness finalization and workload cleanup
- Preserve push, verification and normal token revocation
- Publish required backend Results and truthful outcomes
- Keep completed work visible if publication fails

**Speaker intent**

Discuss who owns cancellation and deadlines, how active work stops, and when
the harness has time to finalize before workload cleanup. Separate prompt
completion from workload completion. Under Lightspeed, its required Kubernetes
Result is additional to the portable core workload outcome. A successful push
followed by mandatory publication failure must preserve the work and expose
the integration failure. Agree how the integration reflects artifact identity,
outcomes, ownership and cleanup in Konveyor status. Include cluster-wide
suspension, approval policy and workload permissions in the lifecycle
discussion. Propose qualification cases rather than a predetermined adapter
placement or new outcome enum.

**Sources:** [ADR 0021, completion and common outcome contract](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md#decision),
[operator lifecycle assessment](../briefs/lightspeed-2026-11-11.md),
[historical input/Result probe][operator-probe].

## 13. Optional: joint follow-up

**Time:** 60 seconds. Optional operator section.

**On-slide copy**

- Review a custom-workload contract together
- Choose a representative integration test
- Identify interested contributors and a suitable follow-up

Runtime contributions can proceed independently

**Speaker intent**

Ask what they consider the useful next experiment and who would like to join.
A candidate operator check is two simultaneous invocations with different
images, complete input/credential access, and observable completion/cleanup.
Exercise cancellation and Result-publication failure as distinct cases. Use
their feedback to refine the design and scope. Upstream support is required
before operator adoption, and Konveyor will retain its direct Agent Sandbox
path while qualifying the alternative. Future migration to equivalent reusable
Agent/workflow resources is another step as those capabilities cover the use
cases. Avoid implying a maintenance transfer, accepted contribution or fixed
implementation schedule. Offer November 18 or a smaller discussion only if
useful to the participants.

**Sources:** [ADR 0021, evidence and follow-up](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md#evidence-and-follow-up),
[discussion brief](../briefs/lightspeed-2026-11-11.md).

[prototype]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/docs/lightspeed-runtime-prototype.md
[reference]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/hack/lightspeed-runtime/reference.json
[session]: https://github.com/djzager/lightspeed-agentic-sandbox/blob/ecbd76a613ed43e580c2481fac4cb1f2781edb67/src/lightspeed_agentic/session.py
[acp]: https://github.com/djzager/lightspeed-agentic-sandbox/tree/ecbd76a613ed43e580c2481fac4cb1f2781edb67/src/lightspeed_agentic/acp
[coolstore]: https://github.com/konveyor-ecosystem/coolstore/tree/32c1722efdf883f5f400170adb770a46a9b58116
[controller-review]: https://github.com/djzager/agentic-controller/tree/feat/lightspeed-runtime-integration
[runtime-review]: https://github.com/djzager/lightspeed-agentic-sandbox/tree/feat/reusable-sessions-acp
[ui-review]: https://github.com/djzager/tackle2-ui/tree/fix/lightspeed-acp-ui
[operator-probe]: https://github.com/djzager/lightspeed-agentic-operator/tree/792f28cb2a36055c7d6e88a8b7ea2c07ae7c402e/research/konveyor-contract
