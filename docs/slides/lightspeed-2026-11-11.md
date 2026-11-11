---
marp: true
theme: konveyor
paginate: true
title: Adopting Lightspeed’s DeepAgents runtime in Konveyor
description: Konveyor setup, runtime changes and planning demonstration
author: David Zager
style: |
  section.diagram { justify-content: flex-start; }
  section.diagram h1 { margin-bottom: 12px; }
  section.diagram p { margin: 0; }
  section.diagram img { display: block; margin: 0 auto; }
  section.diagram p:last-of-type { margin-top: 4px; font-size: 26px; }
  section.demo { padding: 24px 60px 42px; justify-content: flex-start; }
  section.demo h1 { margin: 0 0 8px; padding: 0; font-size: 42px; }
  section.demo p.demo-caption { margin: 0 0 12px; font-size: 24px; }
  section.demo video, section.demo .demo-poster img { display: block; width: 824px; height: 515px; margin: 0 auto; object-fit: contain; background: #000; }
  section.demo .demo-poster { display: none; }
  section.demo p.demo-download { margin: 8px 0 0; font-size: 18px; }
  @media print {
    section.demo video { display: none; }
    section.demo .demo-poster { display: block; }
  }
---

<!-- _class: lead -->

![w:160](konveyor-logo.svg)

# Adopting Lightspeed’s DeepAgents runtime in Konveyor

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

# Runtime integration

- Konveyor with Goose
- The prototype with Lightspeed
- Changes in Lightspeed’s runtime
- Coolstore planning demo
- Questions

<!--
20 seconds.

First show our setup with Goose, then the prototype with Lightspeed in its
place. Explain the changes we made in your runtime before showing the demo.
Stop for runtime questions afterward. A separate operator agenda follows
only if the group wants to continue.
-->

---

<!-- _class: diagram -->

# Konveyor with Goose

![w:1135](konveyor-run-today.svg)

<!--
70 seconds.

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

# Konveyor with Lightspeed

![w:1135](lightspeed-runtime-swap.svg)

Our harness still prepares the repo, verifies the work and pushes to Git.

<!--
70 seconds.

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
https://github.com/djzager/agentic-controller/blob/447afecfb249b13e95b2c6ef1a156a0f03ce388c/docs/lightspeed-runtime-prototype.md
-->

---

# Lightspeed runtime changes

- **Sessions:** send another prompt without starting over
- **Controls:** approve a tool, answer a question, give guidance and stop work
- **Optional ACP adapter:** connect sessions to our existing UI
- **Token counts:** send usage back to Konveyor
- **Model settings and errors:** pass reasoning effort through and report output cutoffs

We kept Lightspeed’s checks on tool output.

<!--
3 minutes.

We reused your DeepAgents runtime and made a few changes so our users can
keep working with it the way they do today. The agent can receive another
prompt without losing the conversation. People watching can approve a tool,
answer a question, give guidance or stop the work. The optional ACP adapter
connects this to our existing UI. The session support can also be used without
that adapter.

A concrete example is our turn-limit handoff. After asking the agent to plan
a migration, our harness may need a second prompt: write down where you got
to before stopping. That prompt needs the same conversation. Prompt completion
and workload finalization are different events. This session lasts for the
runtime process. Recovery after process loss is separate work, as is a fresh
workflow stage reading a committed handoff from Git.

Guidance is picked up before the next model call. Cancelling waits for active
work to stop and terminates shell child processes. Our harness and UI also
needed changes on the Konveyor side. The runtime keeps the conversation and
model clients alive, and offers approval/question callbacks independently of
ACP. The adapter uses those controls through our existing harness tee.

We also forward explicitly configured reasoning effort to the provider. This
demo selects low effort and an optional per-generation output allowance in its
image, while preserving the runtime defaults. If the provider reports an output
cutoff, the runtime fails before tools or another model call, keeps observed
usage, and allows an explicit same-session follow-up. It does not retry automatically.

We preserved inspection before untrusted tool results reach either the model
or viewers. These are checks on tool output for prompt injection, not a general
command-safety guarantee. Existing integration tests used scripted model and
classifier responses. They verify the interaction and inspection paths, not
model reasoning or classifier quality. The November 9 scripted UI recording covers
questions, approvals and guidance. Separate cases cover cancellation and same-session
turn-limit handoff. Planning alone will not establish all runtime requirements.

Token reporting is partial: agent, subagent, summarization and shaping calls
are observed, while isolated inspection classifier calls are excluded. No
monetary cost is supplied. A configured maxCost is rejected before execution.
We want to improve runtime reporting together and consume it in Konveyor,
including timely updates and cancellation for monetary budget enforcement.
That remains required for full adoption. It is follow-on work to this prototype,
not functionality demonstrated today. These changes are on review branches,
not already merged into Lightspeed upstream.

Sources: runtime session core, optional ACP adapter, evidence and usage limits
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/acp/README.md
https://github.com/djzager/agentic-controller/blob/447afecfb249b13e95b2c6ef1a156a0f03ce388c/docs/lightspeed-runtime-prototype.md
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

<!-- _class: lead demo -->

# Coolstore planning demo

<p class="demo-caption">A Java EE application we want to move to Quarkus</p>

<video controls playsinline preload="metadata" aria-label="Coolstore planning demo, 2 minutes 36 seconds" poster="lightspeed-2026-11-11-demo-poster.jpg" src="lightspeed-2026-11-11-demo.mp4">
  <a href="lightspeed-2026-11-11-demo.mp4">Open the recording (2:36)</a>
</video>
<a class="demo-poster" href="lightspeed-2026-11-11-demo.mp4"><img src="lightspeed-2026-11-11-demo-poster.jpg" alt="Coolstore planning demo recording"></a>
<p class="demo-download"><a href="lightspeed-2026-11-11-demo.mp4">Open recording separately (2:36)</a></p>

<!--
3 minutes, including the 2 minute 36 second recording.

Coolstore is our example application. The live authenticated OpenShift run used
GLM 5.3 through an Anthropic-compatible Gateway and the modified Lightspeed
runtime inside our existing Go harness and Agent Sandbox.

Show the database-reuse question and answer, reviewed tool permissions, and
guidance taken up by the agent. The reviewer requested changes to the first
plan, then approved the corrections and final commit. The harness ran all five
planning checks and pushed commit 9e97db0. The run finished Succeeded, with
Verified=True for that commit. Only docs/plan.md was added against the actual
prepared checkout. Inspect the decision in that plan and the Git diff.

The full run took 28 minutes 9 seconds. The recording contains 2 minutes 36 seconds
of selected normal-speed interactions and labelled pauses to read the actual result.
Idle waits are omitted. This is live-model evidence, separate
from the November 9 scripted recording. Planning and passing planning checks
do not establish a working application migration. Application build/tests and
runtime/cutover checks remain future work.

The runtime reported 33 native model calls, 563,794 input tokens, and 10,391 output
tokens. Inspection-classifier calls are excluded and no monetary cost is supplied.
The deployed image and imported runtime source were independently checked.
Result:
https://github.com/djzager/coolstore/blob/9e97db02b641dc2608fcc399503bbc1a0ed3b842/docs/plan.md

Analysis/input revision:
https://github.com/konveyor-ecosystem/coolstore/tree/32c1722efdf883f5f400170adb770a46a9b58116
Actual pre-write checkout:
https://github.com/konveyor-ecosystem/coolstore/tree/b5752b936f01acd858ca21fd2d3c4f03d3a6321e
32c1722 is the direct child of b5752b9. Their complete tree difference is only
.vscode/extensions.json. The application source/build/configuration is identical.
The existing harness cloned default main rather than the configured source ref;
source-reference selection is follow-up work. The plan and passing checks record
both revisions and compare the result to the actual frozen checkout.
Demo preparation:
https://github.com/djzager/agentic-controller/blob/docs/lightspeed-discussion-2026-11-11/docs/slides/lightspeed-2026-11-11-demo.md
-->

---

# Runtime questions

- Would session support be useful to you?
- Does an optional ACP adapter make sense?
- How would you like to review these changes?

[Runtime changes](https://github.com/djzager/lightspeed-agentic-sandbox/tree/feat/reusable-sessions-acp) and [Konveyor integration](https://github.com/djzager/agentic-controller/tree/feat/lightspeed-runtime-integration)

We can pause here, or talk about your operator.

<!--
30 seconds, then discussion.

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

# Lightspeed operator discussion

- Workload images and startup
- Placement and credentials
- Run completion and cancellation
- Questions and follow-up

<!--
20 seconds. Optional operator section begins after runtime questions.

Ask whether the group wants to continue now or review this material later.
These are operator-adoption requirements and open design questions. The demo
and cluster tour use our controller and Agent Sandbox. We have not deployed
the Lightspeed operator for that demonstration.
-->

---

# Workload images and startup

**We need to choose an image for each run.**

- Different projects need different build tools
- Each image must start our harness
- The image’s normal entrypoint may be enough

Today, the operator’s image setting is shared across runs.

<!--
50 seconds.

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

# Placement and credentials

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

# Run completion and cancellation

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

# Operator questions

- Try two runs with different images
- Work through access to Hub and model credentials
- Test completion, cancellation and reporting failures
- Agree who wants to help and when to follow up

We can keep moving on the runtime while we work out operator integration.

<!--
40 seconds, then discussion.

Those are candidate experiments, and we'd like your thoughts on where to
start. We can go deeper next Wednesday or with a smaller group if useful.
We aren't asking for a delivery commitment today.

We can also revisit our Agent and workflow resources as yours cover the
same needs. That is later work; it doesn't need to be solved to reuse the
runtime or to evaluate your operator.

Source: accepted ADR 0021
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->
