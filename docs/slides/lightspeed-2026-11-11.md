---
marp: true
theme: konveyor
paginate: true
title: Trying Lightspeed in Konveyor
description: Replacing Goose with Lightspeed's DeepAgents runtime
author: David Zager
style: |
  section.diagram { justify-content: flex-start; }
  section.diagram h1 { margin-bottom: 12px; }
  section.diagram p { margin: 0; }
  section.diagram img { display: block; margin: 0 auto; }
  section.diagram p:last-of-type { margin-top: 4px; font-size: 26px; }
---

<!-- _class: lead -->

![w:160](konveyor-logo.svg)

# Trying Lightspeed in Konveyor

Replacing Goose with Lightspeed’s DeepAgents runtime

November 11, 2026

<!--
20 seconds.

We want to use the runtime you've built for our application migration work.
This prototype replaces Goose inside our existing setup. We made changes to
your runtime so our users can keep interacting with the agent as they do today.
We'd like to show you that work and get your thoughts on contributing it.

Source: accepted ADR 0021
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

# What we’ll walk through

- Where Lightspeed fits in Konveyor
- What we changed to keep today’s experience
- A planning run against Coolstore
- Your thoughts on contributing these changes

If useful afterward: could your operator run our workloads too?

<!--
20 seconds.

The first part is about the runtime. Our controller and Agent Sandbox still
run the workload. We also brought a few slides about using your operator,
which we can get into if there's time and interest.
-->

---

<!-- _class: diagram -->

# How a run works today

![w:1135](konveyor-run-today.svg)

<!--
35 seconds.

A user chooses an application and task in Konveyor. Our harness gets the
application details from Hub, prepares the repository and starts Goose.
Goose calls the model and uses tools to do the work. It commits locally,
then our harness verifies the work and pushes the result to Git.
The controller creates the Sandbox and watches its status. It doesn't call
Hub or connect to the runtime. This is the setup we're starting from.

Sources: domain glossary and accepted ADR 0021
https://github.com/konveyor/agentic-controller/blob/369070c659a71cecf50d57c376cb13d09d332701/CONTEXT.md#infrastructure
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

<!-- _class: diagram -->

# Lightspeed takes Goose’s place

![w:1135](lightspeed-runtime-swap.svg)

Our harness still prepares the repo, verifies the work and pushes to Git.

<!--
40 seconds.

The highlighted box is the swap. Our controller still creates the Sandbox,
and our Go harness still prepares the application workspace and launches the
runtime. The runtime works on the code and commits locally. The harness does
the verification and Git push afterward, keeping push credentials out of the
runtime. Hub and the UI are still part of the same setup.

Lightspeed brings its DeepAgents integration, including tools, skills, MCP
and checks on tool output. We adapted how our harness starts and configures
it. This diagram shows responsibilities, not a claim that integration needed
no code changes. The prototype makes Lightspeed selectable; Goose remains
the default until we complete adoption.

Adapted in scope from Ian's PR diagram:
https://github.com/konveyor/agentic-controller/pull/280#issuecomment-6372032054
Current implementation:
https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/docs/lightspeed-runtime-prototype.md
-->

---

# What we changed in Lightspeed

- **Sessions:** send another prompt without starting over
- **Human interaction:** approve actions, answer questions, give guidance and stop work
- **Optional ACP adapter:** connect those controls to our existing UI
- **Token reporting:** send usage back to Konveyor

<!--
70 seconds.

These are the main additions in our runtime fork. The session is the reusable
part. It keeps the conversation alive and gives callers a way to interact
with or stop the running agent. The ACP adapter connects that to the UI
through the protocol we already use, and can be left out by other callers.

We reused Lightspeed's DeepAgents implementation, tools and inspection.
We preserved inspection before tool output reaches the model or the viewer.
Our harness and UI also needed integration changes, but they stay on the
Konveyor side. The demo uses both sets of changes.

The usage reporting is partial. We'll cover the remaining accounting work
later. These are changes on review branches, not features already merged
into your upstream runtime.

Sources: runtime session core, ACP adapter and integration report
https://github.com/djzager/lightspeed-agentic-sandbox/blob/ecbd76a613ed43e580c2481fac4cb1f2781edb67/src/lightspeed_agentic/session.py
https://github.com/djzager/lightspeed-agentic-sandbox/blob/ecbd76a613ed43e580c2481fac4cb1f2781edb67/src/lightspeed_agentic/acp/README.md
https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/docs/lightspeed-runtime-prototype.md
-->

---

# Why we need another prompt

First:

> “Please plan this migration.”

When the run reaches its turn limit:

> “Before you stop, write down where you got to.”

The second prompt needs **the same conversation**.

<!--
60 seconds.

This is a concrete reason for adding sessions, even when nobody is watching.
Our harness needs to ask the agent to save its progress before stopping.
A fresh agent would lose the context of the work it had just done.

People watching a run also need to approve a command, answer a question,
give guidance or stop it. ACP, the Agent Client Protocol, carries those
interactions between the UI and runtime. We kept that adapter separate so
the session support is useful even if you don't want ACP upstream.

The session lasts while the runtime process runs. Restoring it after a pod
restart is separate work. A later workflow stage can already read the handoff
from Git, which is different from continuing this live conversation.

Source: session implementation and interaction contract
https://github.com/djzager/lightspeed-agentic-sandbox/blob/ecbd76a613ed43e580c2481fac4cb1f2781edb67/src/lightspeed_agentic/session.py
https://github.com/djzager/lightspeed-agentic-sandbox/blob/ecbd76a613ed43e580c2481fac4cb1f2781edb67/src/lightspeed_agentic/acp/README.md
-->

---

<!-- _class: lead -->

# Demo: a plan for Coolstore

A Java EE application we want to move to Quarkus

Ask a question. Give guidance. Read the resulting plan.

**Live recording still to come**

<!--
2 minutes 30 seconds, including the recording.

Coolstore is our example application. We want the agent to read its source
and available analysis, then write a migration plan. Show which Agent and
model we selected before starting. Explain the human decision, then find its
effect in the plan and resulting Git changes. Also show which runtime ran.

The live recording is pending cluster/model access and rehearsal. The earlier
recording used scripted model responses. Keep that distinction visible.
Planning does not demonstrate a completed application migration. The selected
verification checks must match what this run actually produces.

Source baseline:
https://github.com/konveyor-ecosystem/coolstore/tree/32c1722efdf883f5f400170adb770a46a9b58116
Demo preparation:
https://github.com/djzager/agentic-controller/blob/docs/lightspeed-discussion-2026-11-11/docs/slides/lightspeed-2026-11-11-demo.md
-->

---

# What works so far

The prototype runs through Konveyor’s UI and Hub.

- We tested approvals, questions and guidance during a run
- We tested stopping a run and saving progress at a turn limit
- We kept Lightspeed’s checks on tool output

**So far, the recorded tests use scripted model responses.**\
The live Coolstore run is next.

<!--
45 seconds.

The UI and Hub are real, and the workload really runs the modified Lightspeed
runtime. The tools, Git operations and verification also execute. Scripted
model responses made those integration tests repeatable.

The UI recording shows questions, approval and guidance. Separate integration
cases cover cancellation and the second prompt used to save progress at a
turn limit. They were not all demonstrated in the same UI recording.

We preserved inspection before tool output reaches either the model or UI.
That checks untrusted tool output for prompt injection; it isn't a promise
that every command is safe. Tests with scripted classifier responses verify
the integration path, not the classifier's detection quality.

Source: prototype evidence and limitations
https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/docs/lightspeed-runtime-prototype.md
-->

---

# Token counts now, cost limits next

- We report token counts from the runtime
- Some calls, including inspection, are still missing from those counts
- We still need dollar costs and spending limits

We’d like to improve Lightspeed’s reporting together and use it in Konveyor.

<!--
45 seconds.

The runtime should tell us what it used. Konveyor can show that information
and use it for execution limits, without building a second billing calculator.

The prototype reports agent, subagent, summarization and shaping calls.
It doesn't count the separate inspection classifier calls or calculate cost.
It rejects maxCost before execution rather than pretending to enforce it.
Spending limits remain necessary for full adoption, but they don't block
this runtime demonstration. Timely updates and cancellation are both needed
to stop work at a budget and ask the agent to save its progress.

Source: prototype usage and limits
https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/docs/lightspeed-runtime-prototype.md#usage-and-limits
-->

---

# What do you think?

- Would session support be useful to you?
- Does an optional ACP adapter make sense?
- How would you like to review these changes?

[Runtime changes](https://github.com/djzager/lightspeed-agentic-sandbox/tree/feat/reusable-sessions-acp)
and [our integration](https://github.com/djzager/agentic-controller/tree/feat/lightspeed-runtime-integration)

We can pause here, or talk about your operator.

<!--
45 seconds, then discussion.

We have review branches for both sides. I want to review the code before
opening implementation PRs, and hear whether this direction is useful to
you. We can agree how to split the contributions and what example would help
your review. A smaller discussion or next Wednesday could be useful if the
group wants to follow up.

Konveyor's accepted direction is in ADR 0021. It records our decision, not
an agreement on your project's behalf.
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

# Could your operator run our workloads?

**We need to choose an image for each run.**

- Different projects need different build tools
- Each image must start our harness
- The image’s normal entrypoint may be enough

Today, the operator’s image setting is shared across runs.

<!--
50 seconds. Optional operator discussion starts here.

This would be a second step after using your runtime. Our demo still uses
our controller and direct Agent Sandbox integration.

We need to run different workload images concurrently, without changing a
global setting. We tried a custom image with the global setting, but we
need a supported way to select one per invocation. We aren't prescribing
a new command field if respecting the image's entrypoint meets the need.
We want to qualify your Agent Sandbox path for this integration.

Source: operator review at 9973d000 and accepted requirements
https://github.com/djzager/agentic-controller/blob/docs/lightspeed-discussion-2026-11-11/docs/briefs/lightspeed-2026-11-11.md#optional-operator-adoption
-->

---

# Where would the workload run?

Hub and its Secrets live in our namespace.\
Your operator uses its namespace for workloads and provider Secrets.

How would the workload get:

- Application data and credentials from Hub?
- Model settings and credentials?
- A connection from our UI for live interaction?

<!--
65 seconds.

There may be a good answer using the existing Secret bindings, or we may
need changes. We want to work through the whole path together before
choosing where to run. Moving the workload alone doesn't answer how it
gets the model credentials or how the UI reaches it.

Agent and LLMProvider are cluster-scoped. Their credential Secrets and the
workloads are namespaced. The operator watches runs across namespaces but
creates workloads and resolves provider Secrets in its configured namespace.
It already supports passing required Secrets as environment values or files.
Reaching Hub over HTTP and having permission to read Kubernetes Secrets
are different parts of the problem. Neither namespace choice is settled.

Source: pinned placement, credential and access review
https://github.com/djzager/agentic-controller/blob/docs/lightspeed-discussion-2026-11-11/docs/briefs/lightspeed-2026-11-11.md#optional-operator-adoption
-->

---

# Finishing or stopping a run

We need time to:

1. Stop the agent
2. Verify and push its work
3. Report what happened
4. Revoke the Hub token and clean up

What should users see if Git push succeeds but publishing the Result fails?

<!--
65 seconds.

Our harness has work to do after the runtime stops. We need to agree how
that fits with your operator's cancellation, deadlines and cleanup.
A hard stop can interrupt that work, so we need clear behavior for both
normal shutdown and emergency suspension.

Your operator requires a Kubernetes Result to report success. If Git push
works but publishing that Result fails, the work must remain available and
the reporting failure must be visible. We want to test those cases together.
The numbered list groups responsibilities; implementation may order token
cleanup and status reporting differently on individual exit paths.

Source: completion, cancellation and Result requirements
https://github.com/djzager/agentic-controller/blob/docs/lightspeed-discussion-2026-11-11/docs/briefs/lightspeed-2026-11-11.md#optional-operator-adoption
-->

---

# Working through this together

- Try two runs with different images
- Work through access to Hub and model credentials
- Test completion, cancellation and reporting failures
- Agree who wants to help and when to follow up

We can keep moving on the runtime while we work out operator integration.

<!--
60 seconds, then discussion.

Those are candidate experiments, and we'd like your thoughts on where to
start. We can go deeper next Wednesday or with a smaller group if useful.
We aren't asking for a delivery commitment today.

We can also revisit our Agent and workflow resources as yours cover the
same needs. That is later work; it doesn't need to be solved to reuse the
runtime or to evaluate your operator.

Source: accepted ADR 0021
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->
