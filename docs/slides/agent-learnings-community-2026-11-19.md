---
marp: true
theme: konveyor
size: 16:9
paginate: true
title: 'AgentLearning: reviewed lessons across runs'
description: 'Five-minute Konveyor community-call presentation of proposed ADR 0024'
author: David Zager
footer: 'ADR 0024 / Proposed'
style: |
  section {
    font-family: Arial, Helvetica, sans-serif;
    font-size: 31px;
    line-height: 1.32;
    padding: 48px 64px 72px;
    background: #1a1a2e;
    justify-content: flex-start;
  }
  h1 { font-size: 43px; margin: 0 0 30px; line-height: 1.15; }
  h2 { font-size: 30px; margin: 0 0 18px; }
  p { margin: 0 0 24px; }
  li { margin-bottom: 18px; }
  footer, section::after { font-size: 17px; color: #a0a0a0; }
  section.cover { display: flex; flex-direction: column; justify-content: center; }
  section.cover h1 { font-size: 66px; border: 0; margin: 28px 0 18px; padding: 0; }
  section.cover p { margin: 0 0 16px; }
  section.cover img { width: 220px; height: auto; }
  .subtitle { font-size: 35px; color: #e0e0e0; }
  .small { font-size: 23px; color: #c0c0cb; }
  .key { font-size: 35px; color: #c4aa6e; }
  .columns { display: grid; grid-template-columns: 1fr 1fr; gap: 52px; }
  .columns p { font-size: 29px; }
  .columns ul { padding-left: 28px; }
  table { display: table; width: 100%; font-size: 27px; line-height: 1.2; margin: 0 0 26px; }
  table th, table td { padding: 17px 20px; }
  table th { white-space: nowrap; }
  section.work table { font-size: 26px; }
  section.work table th, section.work table td { padding: 16px 18px; }
  section.backup { font-size: 28px; }
  section.backup h1 { font-size: 40px; }
  section.backup table { font-size: 25px; }
  section.backup .columns p { font-size: 26px; }
  pre { font-size: 24px; line-height: 1.35; padding: 18px !important; }
  pre code span { color: #e0e0e0 !important; }
  pre code .hljs-attr, pre code .hljs-keyword { color: #c4aa6e !important; }
  code { font-size: 0.88em; }
---

<!-- _class: cover -->
<!-- _paginate: false -->
<!-- _footer: '' -->

![Konveyor](konveyor-logo.svg)

# AgentLearning

<p class="subtitle">Reviewed lessons across runs</p>

Community call / November 19, 2026

<p class="small">ADR 0024 is proposed. Implementation and experiments remain ahead.</p>

<!--
20 seconds. Main talk: slides 1-7, five minutes in total. Slides 8-10 are backup.

We are proposing a way for later agent runs to benefit from lessons discovered
during earlier work. The key contract is reviewed, immutable knowledge with a
fixed selection for each invocation. Today is a proposal discussion. Neither
the resource nor its Hub and runtime integration is implemented by this PR.

Source: https://github.com/konveyor/agentic-controller/blob/f565c0c0c7d9daee90646193e5a41bff52684664/docs/adr/0024-agent-learnings.md
Logo: existing docs/slides/konveyor-logo.svg repository asset, unchanged.
-->

---

# A lesson later runs can reuse

An application run discovers that its default test target skips a required
integration suite.

**AgentLearning** records the lesson, its evidence, and where it applies.

A reviewer accepts the exact content and applicability before later runs use it.

<p class="small">Illustrative scenario. Existing AgentRuns and AgentWorkflowRuns are the consumers.</p>

<!--
40 seconds.

The example is a migration that looks successful until someone notices that
the usual test target omitted an integration suite. The agent can propose a
reusable lesson with evidence. A reviewer checks the wording and applicability.
A later review run or implementation run can receive that accepted lesson,
even when it uses a different Agent template. A SkillCard remains an authored
capability or behavioral rule. AgentLearning adds the reviewed lesson and its
provenance. Finite runs already benefit, without requiring continuing sessions.

Sources: ADR 0024, Use cases and value and Immutable content sections.
https://github.com/konveyor/agentic-controller/blob/f565c0c0c7d9daee90646193e5a41bff52684664/CONTEXT.md#proposed-shared-knowledge
-->

---

# Sharing across applications

Hub puts trusted application labels on each invocation.
The learning carries a selector for **applications 42 and 87**.

| Later invocation | Selected learning |
| --- | --- |
| Application 42 | Includes this revision |
| Application 87 | Includes this revision |
| Application 99 | Excludes this revision |

An archetype selector can cover a broader set of applications.

<!--
45 seconds.

The application where a lesson originated supplies provenance. The approved
selector determines reuse. This example assumes acceptance, authorization,
and the same namespace. Hub projects trusted metadata onto both AgentRuns and
AgentWorkflowRuns, and generic matching selects eligible learnings. An
archetype can provide another label to match. The controller does not look up
application IDs or understand archetypes. The upstream Hub handlers still need
the trusted-label projection. It is planned integration work. Changing the
lesson's scope creates a new proposal and review.

Source: https://github.com/konveyor/agentic-controller/blob/f565c0c0c7d9daee90646193e5a41bff52684664/docs/adr/0024-agent-learnings.md#match-learning-selectors-to-hub-supplied-invocation-labels
-->

---

# Proposal and approval

<div class="columns">
<div>
<h2>The agent proposes</h2>
<p>A learning-authoring skill guides the lesson and evidence.</p>
<p>Each run's Kubernetes identity creates proposals only.</p>
</div>
<div>
<h2>A reviewer accepts</h2>
<p>Hub checks a separate review permission using its existing identities.</p>
<p>Admins review by default. A custom reviewer role allows explicit delegation.</p>
</div>
</div>

<p class="small">Initial review authority covers Hub's managed namespace. Kubernetes RBAC protects approval writes.</p>

<!--
55 seconds.

The agent needs both guidance and a submission identity, with creation rights
and no approval rights. The integration must also validate the originating-run
association. Hub can reuse its local and federated identities, but we need new
learning-specific permissions and handlers. The initial reviewer policy is
instance-wide within Hub's managed namespace, admin-only by default with
explicit delegation through a custom role. Existing run permissions do not
grant review authority. Application-specific reviewer ACLs are deferred.
Hub records the authenticated reviewer and exact revision, then writes through
the Kubernetes API. A create request cannot make a learning accepted.

Sources: ADR 0024, Approval and Authoring sections.
https://github.com/konveyor/agentic-controller/blob/f565c0c0c7d9daee90646193e5a41bff52684664/docs/research/2026-11-18-hub-agent-learning-permissions.md
-->

---

# A fixed set for each invocation

A **Learning Snapshot** pins exact revisions at creation, before queueing.

| Consumer | After a replacement is accepted |
| --- | --- |
| Existing AgentRun | Keeps its pinned revision |
| Existing AgentWorkflowRun | Every stage keeps the same set |
| New invocation | Can select the replacement |

**Revocation is refused** until every unfinished referencing consumer finishes
or actually stops.

<!--
50 seconds.

Creation time is the boundary, rather than the moment a pod starts. A workflow
pins one set for its entire execution, including stages created later. A newly
accepted replacement changes future selections without changing existing
consumers. Content and scope stay immutable, enforced at the Kubernetes API.
Revocation is refused while a queued run, active run, or workflow between stages
still references the learning. Cancellation must actually stop work and release
the reference. The spike must prove admission and revocation coordinate safely.
Content digests and retained content make the exact revision reproducible.

Source: https://github.com/konveyor/agentic-controller/blob/f565c0c0c7d9daee90646193e5a41bff52684664/docs/adr/0024-agent-learnings.md#select-at-invocation-creation
-->

---

<!-- _class: work -->

# Work across the platform

| Owner | Proposed work |
| --- | --- |
| Hub and UI | Learning APIs, permissions, review flow, trusted labels |
| Controller and admission | Generic selection, snapshots, lifecycle protection |
| Harness | Read-only learning files and delivery attribution |
| Agent runtime | Startup guidance, selective reads, context management |

<p class="small">The delivery design must distinguish available files from content that reaches a model call.</p>

<!--
50 seconds.

This crosses repository boundaries. Hub needs registered resource/method scopes,
role grants and identity mappings, review APIs, and trusted label projection on
both invocation types. The UI needs the corresponding review flow and the
operator needs deployment RBAC. The controller remains a generic reconciler.
The harness prepares the pinned files, and runtime integration handles context,
selective reads, compaction, and propagation to subagents. A concise index and
essential guidance enter startup context, while longer lessons remain readable
on demand. File access alone does not establish delivery to a model call.

Sources: ADR 0024, Hub integration, Delivery and Attribution sections.
https://github.com/konveyor/agentic-controller/blob/f565c0c0c7d9daee90646193e5a41bff52684664/docs/adr/0024-agent-learnings.md#implement-the-hub-integration-alongside-the-learning-contract
-->

---

# Spike and community feedback

One standalone run and a two-stage workflow will test:

- Proposal creation and protected human review
- Snapshot stability and safe replacement/revocation
- Actual runtime delivery and task outcomes

**Which lessons from your migrations should we test?**

<p class="small">Next step: validate the contract before accepting the ADR or publishing the API.</p>

[ADR 0024 and discussion: PR #346](https://github.com/konveyor/agentic-controller/pull/346)

<!--
40 seconds. End the five-minute talk here.

The spike compares context policies on the same tasks, model, and corpus, and
reports task success, missed relevant guidance, token/tool cost, and latency.
It must exercise permissions, races, and replacement between workflow stages,
with real runtime evidence distinguished from fixtures. Storage, exact public
fields, admission mechanics, and runtime coverage remain spike outcomes.
We want representative lessons and reviewers for the lifecycle and Hub boundary.
The proposal stays unaccepted and unimplemented until that work establishes the
required guarantees. The backup slides support technical questions.

Source: https://github.com/konveyor/agentic-controller/blob/f565c0c0c7d9daee90646193e5a41bff52684664/docs/spikes/agent-learning-delivery.md
-->

---

<!-- _class: backup -->
<!-- _footer: 'ADR 0024 / Backup' -->

# Selector examples

<div class="columns">
<div>
<h2>Invocation labels</h2>

```yaml
metadata:
  labels:
    konveyor.io/application: "42"
    konveyor.io/archetype: java-ee
```

</div>
<div>
<h2>Learning applicability</h2>

```yaml
matchExpressions:
  - key: konveyor.io/application
    operator: In
    values: ["42", "87"]
```

</div>
</div>

A different learning can use `matchLabels` for the archetype.

<p class="small">Illustrative fragments. Hub still needs to define metadata projection, including multiple archetypes.</p>

<!--
Backup. The exact CRD field containing the LabelSelector is not yet selected.
These are fragments, not deployable AgentLearning resources. Values in one In
expression are alternatives. Requirements within a Kubernetes LabelSelector
are ANDed. The application selector and a separate archetype selector are
different examples, not an implicit OR of two requirements. Acceptance,
authorization, and namespace boundaries still apply.

Source: ADR 0024, label-selector examples.
https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#label-selectors
-->

---

<!-- _class: backup -->
<!-- _footer: 'ADR 0024 / Backup' -->

# Hub permission extensions

| Operation | Illustrative Hub scope |
| --- | --- |
| Read and propose | `agentic.learnings:get`<br>`agentic.learnings:post` |
| Accept or reject | `agentic.learnings.reviews:post` |
| Revoke | `agentic.learnings.revocations:post` |

Hub matches a resource name and HTTP method.
Separate permission resources distinguish proposal POST from review POST.

<p class="small">Register scopes before configuring grants. Hub identities and Kubernetes ServiceAccounts remain separate.</p>

<!--
Backup. Hub Role.Scopes stores strings, and Required(resource) registers names
and checks the request HTTP method. A new approve verb is unnecessary.
Admin's seeded wildcard includes newly registered resources. Explicit custom
roles can delegate review while architect/migrator run rights stay insufficient.
The implementation must test local/federated users, machine identities, forged
reviewer fields, role changes, token freshness, and direct Kubernetes access.
Human-required approval needs an authenticated user identity. No-auth mode
cannot establish it. Proposed scopes and endpoints remain unimplemented.

Source: https://github.com/konveyor/agentic-controller/blob/f565c0c0c7d9daee90646193e5a41bff52684664/docs/research/2026-11-18-hub-agent-learning-permissions.md
-->

---

<!-- _class: backup -->
<!-- _footer: 'ADR 0024 / Backup' -->

# Immutability and delivery evidence

<div class="columns">
<div>
<h2>Immutable revision</h2>
<p>CRD CEL validation rejects changes to the proposal's content and applicability.</p>
<p><code>self == oldSelf</code></p>
<p>UIDs, digests, and retained content preserve snapshot identity.</p>
</div>
<div>
<h2>Observed delivery</h2>
<p><strong>Available:</strong> revision materialized</p>
<p><strong>Advertised:</strong> summary in context</p>
<p><strong>Delivered:</strong> identified content in a model call</p>
<p>Agent-reported consideration remains a separate claim.</p>
</div>
</div>

<!--
Backup. Apply API immutability to title, summary, relevance description, lesson,
evidence references, and selector. Lifecycle state uses a separately protected
write path. The repository already uses CEL transition rules for immutable run
specs. Externally stored lesson bytes also need retention and digest checks.
Deleting and recreating the same resource name does not preserve its UID.
Observe delivery after filtering and truncation. A shell read or subagent
access may require additional runtime coverage. Delivery does not prove
understanding or compliance. Machine-checkable verification remains independent.

Sources: ADR 0024, API immutability and Attribution sections.
https://github.com/konveyor/agentic-controller/blob/f565c0c0c7d9daee90646193e5a41bff52684664/docs/adr/0024-agent-learnings.md
https://kubernetes.io/blog/2022/09/29/enforce-immutability-using-cel/
-->
