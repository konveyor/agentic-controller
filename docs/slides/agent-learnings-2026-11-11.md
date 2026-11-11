---
marp: true
theme: konveyor
size: 16:9
paginate: true
title: AgentLearning: reviewed knowledge for finite invocations
description: Independent AgentLearning proposal and spike for ADR 0024
author: David Zager
footer: 'ADR 0024 · Proposed'
style: |
  section {
    font-family: Arial, Helvetica, sans-serif;
    font-size: 30px;
    line-height: 1.35;
    padding: 48px 64px 72px;
  }
  h1 { font-size: 44px; margin: 0 0 28px; line-height: 1.15; }
  h2 { font-size: 31px; margin: 12px 0 10px; }
  p { margin: 0 0 22px; }
  li { margin-bottom: 16px; }
  footer, section::after { font-size: 18px; color: #a0a0a0; }
  section.lead { background: #1a1a2e; }
  section.lead h1 { font-size: 56px; max-width: 1040px; margin: 24px 0; }
  section.lead p { font-size: 30px; }
  section.lead img { width: 200px; height: auto; }
  table { display: table; font-size: 27px; line-height: 1.25; }
  table th, table td { padding: 15px 17px; }
  section.compact table { font-size: 25px; }
  section.compact li { margin-bottom: 12px; }
  section.references { font-size: 26px; }
  section.references li { margin-bottom: 17px; }
  pre { font-size: 25px; line-height: 1.5; }
  code { font-size: 0.88em; }
  blockquote { margin: 22px 0 0; font-size: 29px; border-radius: 0; }
---

<!-- _class: lead -->
<!-- _paginate: false -->
<!-- _footer: '' -->

![Konveyor](konveyor-logo.svg)

# AgentLearning

Reviewed knowledge for existing runs and workflows

Independent proposal · November 11, 2026

<!--
ADR 0024 has been split from the original joint proposal. This deck covers
AgentLearning independently of durable sessions and event routing. All behavior
is proposed and unimplemented.
Source: docs/adr/0024-agent-learnings.md. Logo: existing repository asset.
-->

---

# Lessons we should not rediscover

- A repository's default tests omit a required verification suite.
- A validated migration procedure applies to several applications.
- A review run and an implementation run need the same approved convention.

**Record the lesson once, review it, and make it available to later work.**

<!--
These are target scenarios rather than measured product results. Existing finite
AgentRuns and AgentWorkflowRuns can benefit; a permanent session is not necessary.
Shared selectors can span applications or Agent templates, subject to authorization.
Source: ADR 0024, Use cases and value.
-->

---

# Independent scope

**AgentLearning:** a reviewable lesson with immutable content and applicability.

**Consumers now:** AgentRuns and AgentWorkflowRuns.

**Delivery:** a pinned collection of read-only memory files.

No session, webhook receiver, or event router is required.

<!--
AgentLearning is not a synonym for SkillCard. A SkillCard declares a capability
or behavioral constraint; a learning records a reviewed lesson with applicability
and provenance. Their delivery can reuse runtime mechanisms. An AgentSession may
later use the same contract at each input, but neither proposal blocks the other.
Source: ADR 0024, Use cases and value; Boundaries and alternatives.
-->

---

# Review and immutable revisions

**Propose → review → accept or reject**

- Review the summary, full lesson, evidence, and applicability together.
- Changing content or scope creates a new proposal.
- Agent discoveries remain candidates until the required approval occurs.

Labels express applicability; **they do not grant authority**.

<!--
When policy requires human acceptance, record an authenticated human decision
on the exact revision. Rejected proposals remain history. The model cannot
assert that its own proposal was human-approved. Initial resolution is scoped
to a namespace and combines acceptance, matching, and authorization.
Source: ADR 0024, Immutable content and applicability; explicit review.
-->

---

# Snapshot at invocation creation

| Consumer | Selection boundary |
| --- | --- |
| **AgentRun** | At creation, before any queueing delay |
| **AgentWorkflowRun** | At creation; every stage inherits the same set |

Store exact revisions and content digests, with retained content.

A later pod or stage must not select today's collection again.

<!--
Creation-time admission and consumer registration must coordinate with revocation.
An eventual lookup when a pod starts is too late. Workload label changes do not
modify the selection. The spike must prove the transaction/recovery mechanism;
adding reference fields alone does not establish that guarantee.
Source: ADR 0024, Select at invocation creation.
-->

---

# Replacement and revocation

**Accepted replacement:** future invocations select the new revision.

**Existing consumers:** continue with their pinned revision.

> Refuse revocation until every unfinished referencing consumer has finished or actually stopped/cancelled.

Queued runs and workflows between stages still count.

<!--
This is not disable-now-and-drain behavior. Failed revocation must not silently
change selectability. Admission and revocation need an authoritative coordination
boundary; a cached list or asynchronous count alone cannot prove safety. Retaining
an artifact for history is distinct from allowing future selection.
Source: ADR 0024, Replace for future work; refuse revocation during use.
-->

---

# Memory files, loaded selectively

```text
memory/index.md                   approved summaries and paths
memory/learnings/<revision>.md    full lesson and evidence
memory/manifest.json              identities and digests
```

Load a concise index and essential guidance automatically.
Read longer procedures when relevant. **No MCP required.**

Keep scratch memory separate from accepted knowledge.

<!--
Illustrative paths, explicitly configured as startup memory rather than an
assumed DeepAgents convention. The harness renders approved metadata without
inventing summaries. Prefer one reusable lesson per record; show size in review,
use subordinate indexes as collections grow, and do not silently truncate
required instructions. The runtime handles selective reads and compaction.
Sources: ADR 0024; https://docs.langchain.com/oss/python/deepagents/memory
https://www.anthropic.com/research/components-of-agent-memory
These sources support evaluation, not a universal performance claim.
-->

---

# What reached the model

| Record | Evidence |
| --- | --- |
| **Available** | Revision materialized in the snapshot |
| **Advertised** | Summary entered model context |
| **Delivered** | Content or an identified excerpt reached a model call |
| **Agent-reported consideration** | Explicit applicability or conflict report |

A file read does not prove that the learning was followed.

<!--
The harness owns durable attribution using runtime events after inspection and
truncation. Filesystem reads can include another process or content never shown
to the model. Qualify coverage for shell access and subagents. Model-reported
consideration is not proof of internal reasoning or compliance.
Source: ADR 0024, Record what was supplied without claiming to observe reasoning.
-->

---

# A focused learning spike

Use one standalone run and a two-stage workflow.

- Replace a learning while consumers are queued or between stages.
- Race new invocation admission against revocation.
- Reproduce the exact files and observe real-runtime delivery.
- Compare context policies using the same tasks, model, and corpus.

**No event-ingress or session implementation in this spike.**

<!--
Measure task success, required-instruction adherence, missed lessons, actual
content delivered, token/tool cost, and latency. State counts and configuration.
Re-materializing files in a replacement process proves knowledge stability,
not recovery of arbitrary external actions. Select storage/fields only after
review of lifecycle and runtime evidence.
Source: docs/spikes/agent-learning-delivery.md.
-->

---

<!-- _class: references -->

# Review the learning contract

**Review now:** scope, immutability, approval, snapshot timing, and revocation.

**Spike returns:** admission mechanics, storage, context budgets, and runtime coverage.

- ADR 0024: `docs/adr/0024-agent-learnings.md`
- Spike: `docs/spikes/agent-learning-delivery.md`
- [DeepAgents memory](https://docs.langchain.com/oss/python/deepagents/memory)
- [Anthropic memory research](https://www.anthropic.com/research/components-of-agent-memory)

<!--
The proposal is independent of continuing sessions. Its value is reusable reviewed knowledge
for finite invocations, not a reason to create a continuing session. Full primary
research and limitations: docs/research/2026-11-11-durable-agent-learning-delivery.md.
-->
