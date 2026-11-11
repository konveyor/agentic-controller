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
  section.technical { font-size: 27px; }
  section.technical h1 { font-size: 44px; margin-bottom: 18px; }
  section.technical p { margin: 12px 0; }
  section.technical p:last-of-type { font-size: 23px; }
  section.demo { font-size: 25px; }
  section.demo h1 { font-size: 46px; }
---

<!-- _class: lead -->

![w:160](konveyor-logo.svg)

# Adopting Lightspeed’s DeepAgents runtime in Konveyor

November 11, 2026

<!--
15 seconds.

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
15 seconds.

First show our setup with Goose, then the prototype with Lightspeed in its
place. Explain the changes we made in your runtime before showing the demo.
The technical runtime changes come before the demo. A dedicated Questions
slide follows the demo, then the operator adoption problems and roadmap discussion.
ADR 0021 separates runtime reuse, operator qualification and later resource migration.
-->

---

<!-- _class: diagram -->

# Konveyor with Goose

![w:1135](konveyor-run-today.svg)

<!--
45 seconds.

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
45 seconds.

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

<!-- _class: technical -->

# Prompt completion and session lifetime

**Upstream:** `DeepAgentsProvider.query()` creates a graph, model clients and a new conversation thread for each query.

**Our change:** `RuntimeSession` keeps the graph, checkpoints and clients across prompts. Finishing a prompt leaves the session available.

**Why Konveyor needs it:** after a turn limit, the harness can ask the same conversation to write its handoff before verification and Git push.

Components: [DeepAgents provider](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L690), [RuntimeSession](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L49)

<!--
35 seconds.

This changes resource ownership in Lightspeed's provider/session layer around
DeepAgents. In the upstream query path, graph construction and client cleanup
belong to one query. In the prototype a RuntimeSession owns those resources;
prompt() serializes prompts against its stable thread. The first prompt creates
an InMemorySaver and graph. Later prompts reuse them; aclose() ends the session
and closes its clients. Explicit close and ACP owner disconnect end that lifetime.

The practical example in ADR 0021 is unattended wind-down. The harness reserves
part of its turn budget, runs the primary task, then can send a handoff prompt
to the same conversation. The runtime does not take ownership of Git push or Hub.
State survives prompts in the same process, not pod loss. A new workflow stage
still reads committed handoff files rather than inheriting this graph.

Baseline used for the prototype:
https://github.com/openshift/lightspeed-agentic-sandbox/blob/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d/src/lightspeed_agentic/providers/deepagents.py#L700-L710
https://github.com/openshift/lightspeed-agentic-sandbox/blob/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d/src/lightspeed_agentic/providers/deepagents.py#L877-L884
The session path also discovers the harness's HOME/.agents/skills and exposes
it through DeepAgents' native skill mechanism as /skills/user/. Execution cwd
can remain the application repository; this is a filesystem integration seam,
not a replacement skills implementation.
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L799-L810

Session resource reuse and explicit close:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L999-L1036
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L101-L141
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L275-L287
Current upstream checked November 11 still has the query-owned graph and cleanup:
https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L999-L1009
https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L1135-L1140
Accepted requirement:
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

<!-- _class: technical -->

# Human interaction through runtime callbacks

**Upstream:** tools have no session-level host callback for approval or questions.

**Our change:** tool middleware asks for permission immediately before execution. A local `ask_user` tool calls a separate human-input callback.

**Why Konveyor needs it:** tool approval and agent-initiated questions remain independent controls. The session interface works with any host that implements those callbacks.

Components: [session tool middleware](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L290), [question callback](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L336)

<!--
35 seconds.

PermissionCallback and UserInputCallback live with the reusable session, not
inside an ACP type. In approve mode, middleware calls the host before executing
a tool. Without a callback granting permission, that tool is denied. In auto mode
the runtime does not ask for tool approval. The optional question tool invokes
the user-input callback; answering a question is a different interaction from
approving a command, so it does not require a redundant tool-approval round trip.

Konveyor supplies its existing mode and askUser semantics through the adapter.
Another host could implement these callbacks differently. Approval is human
supervision of execution. Tool-output inspection is a separate mechanism and
is described later. Neither is a general guarantee that every command is safe.

Callback types:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L24-L42
Permission boundary and question tool:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L290-L360
Accepted interaction controls:
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

<!-- _class: technical -->

# ACP stays outside the session core

**Upstream:** the batch entrypoint consumes provider events; it has no ACP session endpoint.

**Our change:** a separate, optional ACP adapter translates session events and callbacks into the messages Konveyor already uses.

**Connection path:** the UI connects to the Go harness's ACP tee, which connects to the local Lightspeed adapter. `RuntimeSession` owns the conversation.

Components: [ACP adapter](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/acp/README.md), [session core](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py)

<!--
35 seconds.

The new python -m lightspeed_agentic.acp entrypoint is packaged with an optional
acp dependency. Its ACPConnection maps permission callbacks to
session/request_permission and questions through the configured harness stdio
ask_user bridge and elicitation/create. It maps normalized runtime events and
stop reasons onto the existing harness/UI transport. The session core remains
usable without that package, and the existing batch entrypoint is retained.

The adapter listens on an authenticated loopback endpoint behind the Go harness
tee. The launcher pins the working directory, optional logical workspace aliases,
approval floor and executable used for the trusted question bridge. Transcript
replay lets viewers attach; it does not grant them the execution owner's rights
or reconstruct a session after process loss. The Kubernetes controller watches
resources and is not in this streaming path, as required by ADR 0021.

Optional dependency and launcher:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/pyproject.toml#L26-L28
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/acp/__main__.py#L16-L69
Callback projection, pinned configuration, replay and authentication:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/acp/server.py#L243-L324
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/acp/config.py#L24-L71
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/acp/server.py#L473-L520
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

<!-- _class: technical -->

# Steering and cancellation during a prompt

**Upstream:** batch cancellation exists, but there is no active-session steering interface.

**Our change:** guidance is queued before the next main-model call. Cancellation awaits active graph/tool work and kills and reaps local shell process groups.

**Why Konveyor needs it:** a user can redirect or stop work, then the harness can continue wind-down with consistent tool history.

Components: [RuntimeSession controls](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L215), [session shell backend](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session_backend.py)

<!--
35 seconds.

Steering does not interrupt a command or a model request already in progress.
It queues HumanMessages for the next safe main-model boundary. Guidance arriving
during the final response is consumed before publishing terminal success, so
accepted guidance is not silently lost at the end of a prompt.

cancel() cancels and awaits the active task. The session-only shell backend uses
asynchronous subprocesses and a POSIX process group; cancellation or timeout
kills and reaps that local group. The session fills unanswered tool calls in
checkpoint history with error results so a later prompt cannot replay them.
If history repair fails, the session is closed rather than continued unsafely.

This is runtime cancellation, inside the workload. It is not deletion of the
Sandbox, and it cannot undo completed effects or guarantee cancellation of a
remote MCP operation. The operator shutdown discussion later concerns that
separate workload lifecycle.

Existing batch cancellation:
https://github.com/openshift/lightspeed-agentic-sandbox/blob/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d/src/lightspeed_agentic/run_agent.py#L316-L320
Steering, checkpoint repair and cancellation:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L152-L175
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L215-L273
Local process groups:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session_backend.py#L16-L65
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session_backend.py#L92-L95
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

<!-- _class: technical -->

# Usage reporting and turn-limit handoff

**Upstream:** token totals are returned at query completion; `max_turns` sets a graph recursion limit.

**Our change:** model-completion callbacks publish cumulative session tokens. An explicit main-model counter enforces each prompt's turn allowance.

**Why Konveyor needs it:** the harness gets a limit reason and can send a handoff prompt with the remaining run budget, in the same session.

Components: [generation callback](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L335), [session budget](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L81), Konveyor harness

<!--
35 seconds.

Upstream already reports tokens; we are changing when and how the host receives
them, not introducing token accounting from nothing. SDK completion callbacks
observe completed calls even when a later tool is cancelled. Session token
snapshots accumulate across prompts. Main-model turns reset for each prompt;
the harness supplies the remaining allowance to enforce the run-level budget.
Session middleware prevents the next main-model call once that allowance is used.

Main, subagent, summarization and shaping calls contribute observed tokens.
Only main-model completions consume this prompt's turn budget. Isolated
inspection-classifier calls are excluded. These are token observations, not
context occupancy or a complete billing total. Absent usage remains absent;
measured zero stays zero. The runtime supplies no monetary cost, so the Konveyor harness rejects configured
maxCost before execution. ADR 0021 still requires full-adoption monetary behavior; that
remains runtime reporting/enforcement work to contribute, not a parallel
Konveyor pricing estimator.

Baseline totals:
https://github.com/openshift/lightspeed-agentic-sandbox/blob/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d/src/lightspeed_agentic/providers/deepagents.py#L1013-L1031
Completion observation, budget and ACP projection:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L370-L401
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L81-L99
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L306-L311
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/acp/server.py#L340-L381
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/acp/server.py#L453-L469
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

<!-- _class: technical -->

# Tool-output inspection in the session path

**Prototype baseline:** untrusted tool output is inspected before entering model context and before release to viewers.

**Our change:** keep both gates through sessions and ACP. Add trust for registered human answers; ignore native thinking blocks when parsing classifier decision text.

**Upstream update:** newer tool-output redaction and shaping work must be reconciled when this prototype is ported forward.

Components: [inspection middleware](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/inspection/middleware.py), DeepAgents event release

<!--
35 seconds.

Inspection is existing Lightspeed behavior that ADR 0021 requires us to retain.
The prototype is based on 5ea23f3. It preserves that baseline's model-boundary
classification/wrapping and acceptance-gated viewer release; unlike the older
ADR experiment, it does not bypass the client-display gate.

We added explicit trust for successful responses from the registered local
human-input callback. This prevents treating a user's answer as untrusted tool
content. Remote MCP tools cannot impersonate that registered ask_user tool.
The classifier parser accepts valid Anthropic thinking blocks as metadata while
still requiring the canonical decision text and retaining refusal/cutoff checks.
These changes do not make tool-output inspection a command-safety guarantee.

Current upstream a23005e8, checked November 11, moved this logic into
providers/tool_result_middleware.py and added default-on redaction plus shaping
improvements. Our recorded fork predates that work. Contributing the prototype
requires reconciling those changes; this demo does not prove current-upstream
safety parity. This qualification is distinct from the session interface design.

Baseline gates and prototype trust/parser changes:
https://github.com/openshift/lightspeed-agentic-sandbox/blob/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d/src/lightspeed_agentic/inspection/middleware.py#L120-L184
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/inspection/middleware.py#L50-L76
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L906-L915
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L979-L990
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/inspection/client.py#L101-L130
Current upstream redaction:
https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/tool_result_middleware.py#L188-L230
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

<!-- _class: technical -->

# Model settings and incomplete generations

**Upstream:** Anthropic wrappers accept thinking settings and output caps, but do not forward normalized reasoning effort.

**Our change:** forward effort into `output_config`; allow an explicit main-model output allowance; report known output cutoffs before tools execute.

**Why Konveyor needs it:** chosen model settings take effect, and a reported output cutoff is a failure instead of apparent completion.

Components: [Anthropic model factory](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/models/anthropic.py#L20), [output-limit middleware](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L64)

<!--
35 seconds.

These are provider-integration changes made while qualifying the live demo.
The Anthropic factory already forwarded thinking and existing model-parameter
output caps. It now maps normalized effort to native output_config.effort for
direct, Bedrock and Vertex constructors. The optional
LIGHTSPEED_MODEL_MAX_OUTPUT_TOKENS reaches the main model without changing
inspection or shaping budgets or the runtime's default reasoning mode.

The demo image explicitly selects GLM low effort and a 16,384-token output
allowance. Those settings are not global adoption requirements. When known
provider metadata says max_tokens, length or MAX_TOKENS, new middleware raises
ModelOutputLimitError before the tool node or another model call. Already
observed usage is retained. An explicit same-session follow-up is possible;
there is no automatic retry and not every provider's incomplete-response form
is covered. Cutoff detection is a runtime failure signal; the harness still
verifies the deliverable before Git push.

Baseline thinking/output support, effort mapping and output boundary:
https://github.com/openshift/lightspeed-agentic-sandbox/blob/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d/src/lightspeed_agentic/providers/models/anthropic.py#L61-L74
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/models/anthropic.py#L20-L36
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/config.py#L432-L443
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L64-L80
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L763-L776
-->

---

<!-- _class: demo -->

# Coolstore planning demo

A Java EE application we want to move to Quarkus

[Play the separate demo video (2:44)](https://github.com/djzager/agentic-controller/blob/c43abc5e5509fff9ea982a1a170e7bf51ae1aafe/docs/slides/lightspeed-2026-11-11-demo.mp4)

Code and result:

- [Konveyor harness integration: PR #345](https://github.com/konveyor/agentic-controller/pull/345) · [source changes](https://github.com/djzager/agentic-controller/compare/8c3c7cdabcf81ddd020c0374724af6a72adff40d...73a44d59f9e4cf6644ab4fc48f42211a7eff2548)
- [Lightspeed runtime changes: djzager/lightspeed-agentic-sandbox](https://github.com/djzager/lightspeed-agentic-sandbox/compare/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d...403916314aa4f869fc7733bef6e20fff32cf2680)
- [Konveyor UI changes: djzager/tackle2-ui](https://github.com/djzager/tackle2-ui/compare/65d76136142966f23a6bf01d7765b948da433214...10dc62e6b44036920d3e050ee04d75343fc859a7)
- [Reviewed plan: djzager/coolstore, docs/plan.md](https://github.com/djzager/coolstore/blob/9e97db02b641dc2608fcc399503bbc1a0ed3b842/docs/plan.md)

<!--
3 minutes, including the 2 minute 44 second recording.

Coolstore is our example application. The live authenticated OpenShift run used
GLM 5.3 through an Anthropic-compatible Gateway and the modified Lightspeed
runtime inside our existing Go harness and Agent Sandbox.

Show the database-reuse question and answer, reviewed tool permissions, and
guidance taken up by the agent. The reviewer requested changes to the first
plan, then approved the corrections and final commit. The harness ran all five
planning checks and pushed commit 9e97db0. The run finished Succeeded, with
Verified=True for that commit. Only docs/plan.md was added against the actual
prepared checkout. Inspect the decision in that plan and the Git diff.

The full run took 28 minutes 9 seconds. The recording contains 2 minutes 44 seconds
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
https://github.com/djzager/agentic-controller/blob/4b982fc6255a5cc0e518afdaad491e146ca987cc/docs/slides/lightspeed-2026-11-11-demo.md
-->

---

<!-- _class: lead -->

# Questions

---

<!-- _class: technical -->

# Lightspeed as the execution backend

**Today:** Konveyor's `AgentRun` reconciler creates a Sandbox directly. Our Go harness owns preparation, agent execution and finalization.

**Candidate integration:** delegate execution to a Lightspeed `AgenticRun`; its operator owns the SandboxClaim and workload lifecycle.

**Adoption bar:** per-run image/startup control, complete access to inputs and credentials, and preservation of harness finalization and outcomes.

Agent Sandbox remains underneath Lightspeed's SandboxClaim path. [ADR 0021](https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md) separates this qualification from runtime reuse.

<!--
35 seconds.

This describes a candidate backend integration boundary, not an approved new API.
Our controller would reconcile backend resources and translate generic status;
it would not interpret Hub data or relay ACP. The harness inside the selected
workload would still resolve Hub/application data, protect push credentials,
run the agent, verify, push and clean up.

Lightspeed would take responsibility for creating and managing the execution
resources we create directly today. Its current SandboxClaim mode uses Agent
Sandbox beneath it. We are discussing replacing our direct integration, not
removing the Agent Sandbox dependency. Resource migration is later: retaining
or replacing Konveyor Agents/workflows is not a prerequisite for this backend.
The following slides describe requirements and candidate ways to meet them.

Accepted boundaries:
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
Current operator mode dispatch and Sandbox resources:
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/sandbox_manager.go#L197-L276
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/sandbox_manager.go#L601-L723
-->

---

<!-- _class: technical -->

# Roadblock: selecting the workload image

**Current:** one shared `sandbox-pod-spec` supplies the base image. Agent and AgenticRun have no per-invocation image selector.

- Java EE to Quarkus needs a JDK and Maven in the workload.
- A future Go Migrator needs the Go toolchain.
- Both images must be usable in concurrent runs.

**Required:** select the image per invocation and start its Konveyor harness.

Possible contract: a per-run image override or template selection, honoring the image's entrypoint. [ADR 0021](https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md) makes image/startup control a firm requirement.

<!--
55 seconds.

Skills tell the runtime how to use tools; they do not supply the Java or Go
binaries. A single Python runtime image does not meet these workloads. Downloading
toolchains during a run is a different provisioning mechanism to design; it is
not supplied by selecting skills, and is not our proposed dependency.

At the reviewed operator revision, PodSpecBuilder starts from the shared
sandbox-pod-spec and preserves its image, command and args. That permits a global
custom image, as our earlier experiment showed. It does not let two invocations
select different images without changing shared configuration.

One possible solution is an AgenticRun image override, potentially with an Agent
default; another is per-run template selection with equivalent image and startup
control. These are discussion candidates, not selected API fields. Respecting
the chosen image's ENTRYPOINT may satisfy startup. The ADR does not require a
separate command override if the existing entrypoint contract is sufficient.

Current API/configuration/builder:
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/api/v1alpha1/agent_types.go#L99-L154
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/api/v1alpha1/agenticrun_types.go#L263-L390
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/pkg/configuration/config.go#L185-L195
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/podspec_builder.go#L58-L227
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

<!-- _class: technical -->

# Problem: workload placement and access

**Current:** Lightspeed watches runs across namespaces but creates workloads in its operator namespace. Agent/LLMProvider are cluster-scoped; Secrets are namespaced.

**Problem:** the workload needs Hub's run-scoped inputs/token, model credentials and a reachable ACP endpoint for Konveyor's UI.

**Options to qualify:** run near Hub and bind provider credentials there, or run centrally and deliver Hub inputs/token with the required network access.

Components: Hub, provider/Secret binding, PodSpecBuilder and workload networking. [ADR 0021](https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md) leaves placement open.

<!--
60 seconds.

Cross-namespace watching does not imply execution in the run namespace. The
current workload/input placement and provider Secret lookup use the configured
operator namespace. Agent and LLMProvider being cluster-scoped does not make
their Secret references cluster-scoped. RequiredSecrets already supports env and
file bindings; the missing work is a supported provisioning/authorization/access
arrangement, not inventing credential delivery from nothing.

Hub HTTP reachability with a scoped token is different from Kubernetes Secret
read permission. A workload in another namespace may be able to call Hub if
service DNS, TLS and network policy allow it; it does not need to read Hub's Git
Secret directly because our harness gets Git credentials through Hub. A Secret
mounted into a Pod must still be in that Pod's namespace.

Qualify Hub, Git, model and MCP connections plus UI-to-workload ACP access under
the intended network and identity policies. Moving the Pod alone does not solve
provider binding; copying all Secrets is not the selected design. Neither
centralized execution nor namespace-local execution is chosen by the ADR.

Current placement, provider lookup and Secret mounts:
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/sandbox_manager.go#L197-L246
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/resolve.go#L62-L81
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/podspec_builder.go#L481-L524
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/api/v1alpha1/tools_types.go#L87-L147
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

<!-- _class: technical -->

# Problem: harness input and run completion

**Current:** Lightspeed mounts batch inputs and a Result template under `/input`; its operator uses the Pod and a completed Kubernetes Result to determine success.

**Adapter needed:** our harness must consume those inputs, prepare the repo, run the agent, verify/push, clean up and publish the backend Result before exiting.

**Why:** an agent finishing its answer is earlier than the workload finishing.

If push succeeds but Result publication fails, preserve the work and report the integration failure. [ADR 0021](https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md) requires that distinction.

<!--
60 seconds.

The current operator is batch-driven. It mounts query, output schema, context,
Result template and optional system prompt under /input, plus model/limit env and
credential bindings. It does not require a new HTTP /v1/agent/run server for this
integration. Our custom workload can adapt that input contract to the Go harness,
while preserving the common preparation, execution and core outcome behavior.

The existing harness consumes /run/konveyor/params.json and Hub env today. A
Lightspeed workload adapter must map the backend inputs without teaching the
Kubernetes controller about Hub or changing the shared workload contract solely
for this backend. Publication of Lightspeed's Kubernetes Result is additive.
The operator already provisions per-step Result create and name-scoped
read/update/patch/status permissions; that mechanism needs qualification, not a
blanket request for unrestricted Kubernetes credentials.

On normal completion, agent prompt return is not Pod completion. The harness
must finish verification/push and token cleanup and publish the Result before
exiting. A successful process exit alone is insufficient for the operator's
success contract. If push worked but publication failed, keep the pushed work
and core outcome and expose the backend integration failure. Outcome taxonomy is still
unresolved in proposed ADR 0020; this slide selects no new enum.

Current PodSpecBuilder and completion handling:
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/podspec_builder.go
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/input_configmap.go#L118-L214
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/rbac.go#L510-L566
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/pod_handler.go#L253-L324
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/pod_handler.go#L542-L597
Accepted common workload/outcome contract:
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

<!-- _class: technical -->

# Problem: stopping the agent or its workload

**Runtime cancellation:** stops active prompt/tool work inside the Pod; the Go harness can still finalize.

**Operator hard stop:** terminates the workload and may prevent handoff, verification, push, token cleanup and Result publication.

**Needed:** an agreed normal stop path and time to finalize within the workload deadline, with emergency termination handled explicitly.

Discussion: can operator cancellation request harness wind-down before killing the workload? A per-run hard-stop proposal is open in [PR #588](https://github.com/openshift/lightspeed-agentic-operator/pull/588).

<!--
55 seconds.

There are two lifecycle controls, owned by different components. The viewer's
ACP cancel reaches RuntimeSession.cancel() through the existing harness tee.
That can leave the process and Pod alive for harness finalization. A turn-limit
handoff is also inside that process and depends on session continuation.

Lightspeed's current global suspension path hard-stops workloads. Its open
PR 588 proposes per-run cancellation through a hard-stop path; it is not merged
at the November 11 main pin. We must agree how user cancellation, step deadlines,
platform suspension, and deletion interact with the harness's remaining work.
A Pod deletion grace period alone does not establish that the harness is asked
to perform the right wind-down or that the Result remains publishable.

Current Pod active deadline is five minutes startup plus resolved step timeout
plus one minute grace, with a separate timeout reconciliation loop. Those are
whole-process limits from our harness's perspective. We can configure step
timeouts and reserve runtime versus finalization time; normal batch execution
can already keep the process alive through finalization. A separate graceful
cancellation contract still needs agreement.

Possible integration is a graceful request to the harness followed by a bounded
finalization interval, with emergency stop kept available separately. Another
supported contract may work; the ADR selects required behavior, not a specific
signal or API. Abrupt loss remains possible, so preserved commits/core outcomes,
visible reporting failure and token expiry remain relevant. Cancellation cannot
promise that every remote side effect is undone.

Current stop contract and proposal:
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/.ai/spec/what/sandbox-execution.md#L117-L119
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/suspension_cleanup.go#L885-L968
https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/sandbox_agent.go#L171-L172
https://github.com/openshift/lightspeed-agentic-operator/pull/588
Runtime cancellation:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L266-L287
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---

<!-- _class: technical -->

# OpenShell roadmap and integration contract

**Roadmap question:** are you planning OpenShell in this execution path, and on what timeline?

**If so:** which layer would own workload image/startup, placement, network policy and credential binding?

**Integration impact:** Hub/Git/model/MCP/ACP access and Kubernetes Result publication need a supported contract under that backend.

Should we qualify today's batch contract, or align on one that remains stable through that change?

[ADRs 0016](https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0016-gateway-crd-as-interim-execution-interface.md) and [0021](https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md) keep OpenShell deferred and preserve today's Gateway selection contract.

<!--
60 seconds.

This is a question for the Lightspeed team, not an asserted migration or date.
The public operator/runtime sources and issue/PR searches reviewed November 11
provided no published Lightspeed OpenShell adoption schedule. Ask what they are
planning and whether that should change the order of operator integration work.

OpenShell is a separate execution/control-plane layer, not the DeepAgents
runtime and not Konveyor's Gateway CRD. Its Kubernetes path also uses Agent
Sandbox. If Lightspeed adopts it, the custom-harness contract must cover image
startup, provider/profile binding, permitted Hub/Git/model/MCP/ACP connections
and an identity that can publish the required Kubernetes Result. OpenShell's
policy mediation and workload identity defaults can change how these are
provided; they do not imply these connections are impossible.

ADR 0016's October amendment and ADR 0021 explicitly reject treating OpenShell
as an automatic replacement for Gateway/provider configuration or a predetermined
execution destination. Keep the current Gateway selection contract until an
integration design supplies an equivalent supported path. We need to decide
with Lightspeed whether to target the current operator contract first or share
a stable boundary across their potential backend changes.

Accepted decisions and research:
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0016-gateway-crd-as-interim-execution-interface.md
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
../research/lightspeed-operator-integration-2026-11-11.md
https://github.com/NVIDIA/OpenShell/blob/7d9303690f7de689bddb487efde77365c02f8555/crates/openshell-driver-kubernetes/README.md#L18-L108
https://github.com/NVIDIA/OpenShell/blob/7d9303690f7de689bddb487efde77365c02f8555/crates/openshell-driver-kubernetes/README.md#L355-L375
-->

---

<!-- _class: lead -->

# Questions
