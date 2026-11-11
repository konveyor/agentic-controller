---
adr: "0024"
title: "AgentLearning: Immutable Shared Knowledge for Agent Invocations"
description: "Proposes reviewed, immutable learnings selected at invocation creation and delivered as organized memory files."
status: proposed
status_note: "Independent proposal for existing AgentRuns and AgentWorkflowRuns; AgentSession is an optional future consumer."
date: "2026-11-11"
last_updated: null
authors:
  - "David Zager"
last_reviewed: "2026-11-11"
implementation_status: deferred
review_note: "Learning acceptance, invocation snapshots, consumer accounting, and file delivery are unimplemented. The learning spike is independent of event routing and durable sessions."
relates_to:
  - "0001"
  - "0007"
  - "0014"
  - "0021"
---

# ADR 0024: AgentLearning — Immutable Shared Knowledge for Agent Invocations

This proposal covers shared knowledge for existing finite invocations.
It is proposed and unimplemented; continuing sessions are a separate design.

[Review slides](../slides/agent-learnings-2026-11-11.pdf) ·
[Editable source and speaker notes](../slides/agent-learnings-2026-11-11.md) ·
[Proposed spike](../spikes/agent-learning-delivery.md)

## Use cases and value

A migration run discovers that a repository's default test target omits a
required verification suite. After review, later runs should receive that
lesson without rediscovering the failure. A review run and an implementation
run can use the same accepted convention despite using different Agent
templates. A validated procedure can apply to several applications selected
by Hub-owned metadata.

These use cases already exist with finite AgentRuns and AgentWorkflowRuns.
They do not require a continuing session, event router, webhook receiver, or
new execution backend. The value is reusable reviewed knowledge, reproducible
selection, and evidence of which content reached an invocation.

An AgentLearning is distinct from a SkillCard: a SkillCard declares a capability
or behavioral rule; an AgentLearning records a reviewed lesson and its
applicability/provenance. Their delivery can reuse the same runtime mechanisms.

## Proposed decision

### Immutable content and applicability; explicit review

Introduce a namespaced AgentLearning resource with a concise title/summary,
relevance description, full lesson, evidence references, and generic
applicability selector. The exact schema is a spike output. Content and
applicability are immutable together. A change to either creates a new proposal;
acceptance, rejection, replacement, and revocation are lifecycle state.

When policy requires human approval, record the authenticated human's decision
on the exact content and scope, whether submitted through an API or ACP. An
agent can propose a lesson or replacement; it cannot claim human approval for
itself. Rejected proposals remain history and are excluded from active guidance.
Background reflection can generate candidates but cannot rewrite accepted
shared knowledge. Writable scratch notes remain separate.

Hub or another consumer supplies domain labels on workloads. Generic selector
matching can cover multiple applications, for example
`konveyor.io/application In ["42", "87"]`. The controller does not interpret
application IDs or call inventory systems. Acceptance, matching, and
authorization determine eligibility; labels do not grant access. Initial
resolution is namespace-scoped. Descriptive provenance belongs in annotations
or content, not in an implicit delivery/ownership rule.

### Select at invocation creation

A standalone AgentRun pins its selected learning revisions when created, before
any queueing delay. An AgentWorkflowRun pins one set at creation for every stage,
including stage AgentRuns created later. Workload-label changes and accepted
replacements do not alter an existing snapshot.

The snapshot retains immutable identities and content digests with retained
content or durable artifact references. A selector, mutable name, or pointer to
"latest" is insufficient. Binding the snapshot and registering the consumer
must be part of durable invocation admission; an eventual lookup at first pod
startup is too late. The transaction mechanism is an experiment to prove,
not a guarantee obtained from adding status fields.

If AgentSession is adopted independently, its worker selects before each new
AgentInput and restores the same set on retry. That integration is optional:
learning delivery must work for existing finite invocations first, and the
session proposal can be evaluated without AgentLearning.

### Replace for future work; refuse revocation during use

An accepted explicit replacement switches future eligible selections to the
new revision. Existing consumers retain the old revision. Changing scope also
requires approval; overlapping or conflicting lessons must be made visible
rather than resolved by undocumented ordering.

**Revocation is refused while any unfinished consumer references the learning.**
Queued runs and entire workflows between stages count, not just live pods.
Consumers must finish or actually stop/cancel and release their use first.
Rejected revocation must not silently disable future selections. This is not
a disable-now, drain-later policy.

Admission and revocation must coordinate at an authoritative boundary. A cached
list or asynchronous reference count alone does not prove that no active
consumer exists. Deletion and artifact cleanup must respect active references
and the historical retention contract. Retaining an artifact for history is
distinct from permitting future selection.

### Deliver organized read-only memory files

The harness materializes a consumer's snapshot; the runtime handles context and
selective reads. Illustrative paths are:

```text
memory/index.md                       approved summaries and paths
memory/learnings/<revision-id>.md      metadata, lesson, detail, evidence
memory/manifest.json                   identities, digests, file mapping
```

Configure the concise index explicitly as startup memory. Short required
statements can be included directly; longer procedures and supporting evidence
are read when applicable. The manifest is bookkeeping, not prompt text. MCP is
not required for this delivery path. Files are a reproducible projection of
durable records, not the authoritative mutable store.

Review summary, relevance description, and full content together. Generate the
index deterministically from approved text. Favor one reusable lesson per
record, show content size during review, and separate evidence from the concise
lesson. Add navigable subordinate indexes as collections grow. Do not silently
truncate required guidance or replace it with an unreviewed startup summary.
Context budgets, conflict handling, and required-content overflow behavior
remain explicit spike decisions.

This follows the always-loaded rule/on-demand skill pattern in
[ADR 0014](0014-skill-loading-and-prompt-assembly.md) and the intended
Lightspeed/DeepAgents integration in
[ADR 0021](0021-shared-runtime-and-conditional-lightspeed-execution.md).
Restore required guidance after compaction or restart, define propagation to
runtime subagents, and preserve inspection before model input and viewer output.
A new snapshot must explicitly supersede older guidance still present in
historical conversation text.

### Record what was supplied without claiming to observe reasoning

The harness persists attribution from runtime events:

- **Available:** the selected revision was materialized.
- **Advertised:** its summary entered the model's context.
- **Delivered:** its content or an identified excerpt entered a model call.
- **Agent-reported consideration:** the agent explicitly reported applicability
  or conflict; this is not proof of internal reasoning or compliance.

Correlate revision, consumer/attempt, delivery method, and tool/model call.
Observe the actual context boundary after filtering and truncation. A filesystem
read alone is insufficient, and shell/subagent coverage must be qualified.
Exact model-input retention and mandatory acknowledgment remain open. For
machine-checkable requirements, verification provides evidence that prompt
inclusion alone cannot.

## Boundaries and alternatives

This proposal can ship independently of AgentSession. It introduces no webhook
receiver, router, durable action journal, or continuing workspace. It does not
change the controller into an ACP intermediary. The harness prepares inputs;
the shared runtime owns context and tool execution.

The proposal would replace only ADR 0001's deferred per-Agent MCP memory-service
assumption for approved shared knowledge. It does not prohibit a separate
queryable memory service for other purposes or change existing finite workflows.

All-upfront content risks excessive context; tool-only retrieval can miss
required guidance. The proposed hybrid needs task-based evaluation rather than
a universal performance claim. Mutable shared memory with automatic promotion
cannot preserve the agreed approval scope and snapshot lifecycle. See the
[primary-source research](../research/2026-11-11-durable-agent-learning-delivery.md).

The cost is admission/consumer coordination, retained artifacts, review workflow,
and runtime instrumentation. The independent spike must establish these costs
and guarantees before public fields or a storage backend are selected.

## Proposed next step

Run the [learning-delivery spike](../spikes/agent-learning-delivery.md) against
one standalone run and a two-stage workflow. Prove creation-time snapshots,
replacement, refusal of revocation during use, and read-only selective delivery.
Compare context policies using the same model/tasks/corpus. Session behavior
and provider event delivery are explicitly outside this experiment.
