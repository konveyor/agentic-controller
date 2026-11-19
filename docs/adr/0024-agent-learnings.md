---
adr: "0024"
title: "AgentLearning: Immutable Shared Knowledge for Agent Invocations"
description: "Proposes reviewed, immutable learnings selected at invocation creation and delivered as organized memory files."
status: proposed
status_note: "Independent proposal for existing AgentRuns and AgentWorkflowRuns; AgentSession is an optional future consumer."
date: "2026-11-11"
last_updated: "2026-11-19"
authors:
  - "David Zager"
last_reviewed: "2026-11-19"
implementation_status: deferred
review_note: "Initial review authority is instance-wide in Hub's managed namespace, admin-only by default with explicit reviewer-role delegation. Hub permission registration, trusted metadata projection, learning endpoints, Kubernetes lifecycle protection, and runtime delivery remain unimplemented."
relates_to:
  - "0001"
  - "0007"
  - "0014"
  - "0021"
---

# ADR 0024: AgentLearning: Immutable Shared Knowledge for Agent Invocations

**Update (2026-11-19):** Sets the initial reviewer policy to instance-wide
authority within Hub's managed namespace, seeded admin-only by default with
explicit delegation through a custom reviewer role. Application-specific
reviewer authorization is deferred; learning applicability remains independent
of reviewer authority. This clarifies the proposal, not its acceptance status.

**Update (2026-11-18):** Clarifies approval through existing Hub user permissions
and Kubernetes RBAC, API-enforced content immutability, label-based applicability
across applications, and a required learning-authoring skill with proposal-only
run credentials. A source review of Hub's permission model also identifies the
required Hub endpoints, role/scope changes, and trusted metadata projection;
application-specific approval authority is not an existing Hub capability.
These refine the proposal; no learning API or runtime support is implemented
by this ADR.

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

### Enforce immutable content and applicability in the Kubernetes API

Introduce a namespaced AgentLearning resource with a concise title/summary,
relevance description, full lesson, evidence references, and generic
applicability selector. The exact schema is a spike output. Content and
applicability are immutable together. A change to either creates a new proposal;
acceptance, rejection, replacement, and revocation are lifecycle state.

Use CRD CEL transition validation (`self == oldSelf`) on the immutable proposal
fields to reject updates in the API server. Inline content is compatible with
this mechanism; it already protects AgentRun and AgentWorkflowRun specs in this
repository. The immutable portion includes the title, summary, relevance description,
lesson, evidence references, and applicability selector. Lifecycle state uses
a separately authorized write path. Reconciliation is not the immutability
enforcement boundary. See [Kubernetes CRD immutability](https://kubernetes.io/blog/2022/09/29/enforce-immutability-using-cel/).

A new proposal has a new immutable identity. Learning Snapshots retain the
resource UID and content digest, so deleting and recreating a resource under
the same name cannot replace the pinned revision. Externally stored lesson
content must also be retained and digest-verified; validating an unchanged URL
alone does not make the referenced bytes immutable.

### Authorize approval through Hub identities and Kubernetes RBAC

In Konveyor, approval uses Hub's existing user accounts and authorization,
regardless of the configured authentication provider. Extend that model with
an explicit learning-review permission, checked before Hub submits the decision
through the Kubernetes API using Hub's service account. Record
the Hub user's identity and exact immutable proposal with the decision: the
Kubernetes caller is Hub, which is responsible for authenticating and
authorizing the person behind that call. ACP may carry a review interaction,
but a viewer response alone is not authorization to approve a learning.

Hub currently authorizes a registered resource name plus HTTP method, not an
application ID or applicability selector. Possessing learning-review permission
therefore does not express "may approve only for application 42." The initial
policy is instance-wide learning review within Hub's managed namespace:
seeded admin-only by default, with explicit delegation through a custom reviewer
role containing the learning read/review scopes. Architect, migrator, and other
built-in roles do not gain review permission through their existing run rights.
Application-specific reviewer authorization is deferred as separate work.
A reviewer's permission to act and a learning's applicability selector are
separate concepts. See the
[Hub source review](../research/2026-11-18-hub-agent-learning-permissions.md).

Direct Kubernetes clients are governed by Kubernetes RBAC. Ordinary users and
agent service accounts receive no approval write permission. Separate proposal
creation from the protected approval operation; a create request must never be
able to supply effective approval or a trusted reviewer identity. The spike
must select and test a separately protected status write or approval resource,
including who may record decisions and who applies lifecycle transitions.
RBAC authorizes resources, subresources, and verbs, not arbitrary fields inside
a create request; content-level restrictions require validation. See
[Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/).

Only accepted proposals are eligible for selection. Rejected proposals remain
history. Background reflection may generate candidates for the same review
path; writable scratch notes remain separate from accepted shared knowledge.

### Implement the Hub integration alongside the learning contract

The existing auth providers and role APIs can be reused, but support for
learnings requires changes in Hub and its deployment. The
[source review](../research/2026-11-18-hub-agent-learning-permissions.md) pins the
implemented behavior and separates it from these proposed additions:

- **Endpoints and permissions:** extend `internal/api/agentic.go` and
  `shared/api` with learning list/get/propose, review, and revocation operations.
  Register distinct `Required(...)` resources for proposal and review paths,
  even when both use POST. For example, `agentic.learnings:get/post`,
  `agentic.learnings.reviews:post`, and `agentic.learnings.revocations:post`
  fit the current scope model. These are proposed permission names, not a
  published endpoint/schema contract; approval is not a new HTTP-method verb.
  An accepted replacement must also pass the review permission check.
- **Roles and identity mappings:** extend `internal/auth/seed/roles.yaml` and
  document custom-role assignments and external identity mappings for the new
  scopes. Existing run-creation rights do not imply review rights. Hub's seeded
  admin wildcard includes newly registered resources; keep seeded review grants
  admin-only and use explicit custom-role delegation. View/propose grants are
  separate from review grants. Verify when changed
  assignments reach refreshed tokens rather than promising immediate revocation.
- **Reviewer attribution:** derive the stable Hub subject, login, decision time,
  and exact revision from the authenticated request and server-side state.
  Validate that a human-required approval is attributable to a user or external
  user identity, not merely a service account or OAuth client with POST scope.
  Unauthenticated/no-auth operation cannot supply authenticated human approval.
  Permission checks alone do not establish who personally operated a credential.
- **Trusted workload metadata:** extend both run-create handlers to validate
  the application association and stamp reserved application/archetype labels
  from Hub's own data before invocation admission. Define a representation for
  multi-valued archetypes and keep labels consistent with runtime application
  identity. Caller-supplied labels must not establish application membership.
  This is the narrow create-time projection needed for selection; the harness
  still resolves git URLs, credentials, and runtime grounding from Hub.
- **Kubernetes access:** extend Hub's namespaced Role and the operator's
  corresponding deployment RBAC for the learning resource and chosen protected
  approval write path. Keep approval writes separate from ordinary proposal
  creation. Hub's internal `agentic.harness` service account and HUB_TOKEN are
  Hub API identities; the proposed per-AgentRun Kubernetes ServiceAccount is a
  separate identity and receives no Hub review permission.
- **Clients and review UI:** add shared bindings/OpenAPI definitions and a
  review flow showing the immutable content, evidence, applicability, reviewer,
  and replacement lineage. UI visibility follows the new scopes, while Hub
  enforces every operation independently of whether a button is shown. Verify
  that role management exposes the registered learning scopes.

The upstream Hub source reviewed here has agentic resource permissions and
run-create endpoints, but no learning endpoints or trusted application/archetype
label projection. ADR 0006's application-label contract is a target to reconcile
with the Hub version used by the spike, not evidence that this path is already
implemented upstream. Keep the controller domain-agnostic: Hub owns this
projection and its user-permission policy.

### Require learning-authoring guidance and proposal-only run identities

An Agent that proposes learnings must receive a learning-authoring skill through
the existing SkillCard mechanism. The skill defines when an observation is
reusable and how to provide its summary, full lesson, evidence, relevance,
originating run, and suggested applicability. Discovering a lesson and proposing
its scope require agent judgment; the skill neither grants approval authority
nor manages Kubernetes credentials or RoleBindings.

Automatically provision a ServiceAccount and RoleBinding for each proposing
AgentRun, granting creation of AgentLearning proposals in its namespace without
approval, lifecycle-update, or deletion permission. The submission integration
uses that identity and binds trusted provenance to the originating run; Hub
retains ownership of application semantics. Kubernetes RBAC alone cannot
restrict created objects by their supplied application labels or selectors.
Admission or a trusted submission API must validate any claimed run/application
association against the authenticated identity. The exact submission interface
and credential delivery are spike outputs, alongside the skill content.

Run credential cleanup must not delete shared learnings when the originating
AgentRun is pruned. Preserve its identity as provenance without using that
ephemeral run as a cascading owner of the learning. An agent may propose a
replacement, but acceptance always follows the protected review path.

### Match learning selectors to Hub-supplied invocation labels

Hub or another consumer supplies domain labels on workloads. Generic selector
matching can cover multiple applications, for example
`konveyor.io/application In ["42", "87"]`. The controller does not interpret
application IDs or call inventory systems. Acceptance, matching, and
authorization determine eligibility; labels do not grant access. Initial
resolution is namespace-scoped. Descriptive provenance belongs in annotations
or content, not in an implicit delivery/ownership rule.

For example, a run for application 42 discovers a useful lesson. Its proposal
records that run as evidence and suggests an applicability scope. A reviewer
can accept the exact proposal, or request a new proposal with revised content
or scope before accepting it. An accepted proposal selecting applications 42
and 87 becomes eligible for later invocations of either application. The
originating application does not limit reuse; accepted applicability does.

Hub stamps the relevant application metadata onto each AgentRun or
AgentWorkflowRun when it creates the invocation, extending the intended
application-label contract in [ADR 0006](0006-hub-addon-pattern-for-agent-resources.md).
These are illustrative fragments, not an implemented AgentLearning schema:

```yaml
# Hub-supplied metadata on an AgentRun or AgentWorkflowRun
metadata:
  labels:
    konveyor.io/application: "42"
    konveyor.io/archetype: java-ee
```

```yaml
# AgentLearning applicability: either application 42 or application 87
matchExpressions:
  - key: konveyor.io/application
    operator: In
    values: ["42", "87"]
```

A different learning can select an archetype shared by many applications:

```yaml
matchLabels:
  konveyor.io/archetype: java-ee
```

The archetype label is an illustrative Hub projection, not an existing label
contract. Hub decides which metadata to project, including how to represent
multiple archetypes or other multi-valued attributes. Application IDs in one
`In` expression are alternatives; requirements within a single Kubernetes
LabelSelector are ANDed. The two learning selectors above are separate examples,
not an implicit application-or-archetype expression. See
[Kubernetes label selectors](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#label-selectors).

At invocation creation, generic admission matches accepted learning selectors
against these labels, applies authorization, and pins the resulting Learning
Snapshot. A workflow's later stage AgentRuns inherit that Learning Snapshot
instead of selecting again. The harness materializes the pinned revisions;
it does not query Hub or re-evaluate applicability for learning selection.
Unaccepted or unauthorized matches are excluded, and later label changes do
not alter an existing invocation's selection.

### Select at invocation creation

A **Learning Snapshot** is the fixed set of immutable learning revisions selected
at invocation creation for one run or an entire workflow invocation.

A standalone AgentRun pins its selected learning revisions when created, before
any queueing delay. An AgentWorkflowRun pins one set at creation for every stage,
including stage AgentRuns created later. Workload-label changes and accepted
replacements do not alter an existing snapshot.

The Learning Snapshot retains immutable identities and content digests with
retained content or durable artifact references. A selector, mutable name, or pointer to
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

The harness materializes a consumer's Learning Snapshot; the runtime handles
context and selective reads. Illustrative paths are:

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
learning-authoring guidance, run-scoped proposal credentials, and runtime
instrumentation. The independent spike must establish these costs
and guarantees before public fields or a storage backend are selected.

## Proposed next step

Run the [learning-delivery spike](../spikes/agent-learning-delivery.md) against
one standalone run and a two-stage workflow. Prove creation-time snapshots,
replacement, refusal of revocation during use, and read-only selective delivery.
Demonstrate the authoring skill and proposal-only identity, protected approval,
API-enforced immutability, and both application-ID and archetype selectors.
Compare context policies using the same model/tasks/corpus. Session behavior
and provider event delivery are explicitly outside this experiment.
