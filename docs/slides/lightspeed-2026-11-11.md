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

# Runtime changes and why we need them

- **Keep the conversation:** retain the graph and clients so the harness can request a handoff in a later prompt.
- **Reuse official ACP:** connect Lightspeed to our UI through `AgentServerACP`, while preserving user control.
- **Keep output inspection:** release tool results to viewers after inspection passes, including during replay.
- **Report execution accurately:** count model turns, report usage, apply model settings and detect incomplete responses.

The harness still owns Hub, verification and Git push.

<!--
40 seconds. This is the short summary. The following slides show the source
boundaries behind each change so we can discuss what belongs in Lightspeed.

We are reusing Lightspeed's DeepAgents preparation, tools, skills, MCP and
inspection. Konveyor-specific Hub/Git work remains in our Go harness. The
runtime changes preserve interactions and execution controls required by ADR
0021. We do not ask Lightspeed to own our application inventory or Git push.

The revised ACP implementation uses the official AgentServerACP prompt loop.
The separate demo was recorded with the previous custom ACP implementation at
4039163. It demonstrates that earlier implementation, not the revised ACP path.
The runtime is still a prototype, with monetary accounting and current-upstream
inspection reconciliation remaining adoption work.

Accepted requirement:
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
Official ACP implementation:
https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L924-L1155
-->

---
<!-- _class: technical -->

# A second prompt in the same conversation

**Current upstream path:** `provider.query()` creates a graph and model clients for one query, then closes the clients.

**Change:** `RuntimeSession` owns the graph, checkpoint history and clients until the session closes. Later prompts reuse them.

**Why Konveyor needs it:** after the primary prompt reaches `maxTurns`, the harness sends another `session/prompt` request to write a handoff before finalization.

Sources: [current query path](https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L999-L1016), [session preparation](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session.py#L106-L139)

<!--
25–30 seconds.

DeepAgents can retain message history, and the official ACP server already
accepts repeated prompts. The change is resource lifetime in Lightspeed's
provider integration. Its current query creates a model, backend and graph,
uses a new thread ID and current prompt, and closes its model/classifier in
finally. Calling query() twice does not itself preserve that graph conversation.

DeepAgentsProvider.prepare_graph() now exposes the real prepared graph and its
owned clients without starting a query. RuntimeSession.prepare_graph() reuses
that preparation, and graph_execution() applies per-prompt controls while the
official host drives it. RuntimeSession serializes access to the retained graph,
checkpointer and thread. Explicit close ends the resource lifetime. This retains state in
the running process, not after pod loss. A later workflow stage still reads
committed handoff files rather than inheriting this graph.

The handoff is an explicit second ACP session/prompt request in the SAME
session after the primary request returns at its turn allowance. There is no
dedicated handoff or turn-limit notification. The harness reserves part of the
run budget for that request, then verifies and pushes as usual.

Current upstream graph/current-prompt construction and cleanup:
https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L811-L815
https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L999-L1016
https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L1135-L1140
Official ACP stable session/thread and checkpointer:
https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L792-L802
https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L964-L974
Retained runtime resources:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session.py#L106-L139
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/providers/deepagents.py#L1012-L1038
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---
<!-- _class: technical -->

# Lightspeed behind the official ACP server

**Official ACP already provides:** session requests, prompt handling and rendering of graph events into ACP updates.

**Change:** an optional adapter connects Lightspeed's prepared runtime to `AgentServerACP`. Lightspeed retains graph preparation, inspection and runtime controls.

**Why Konveyor needs it:** our harness and UI can keep using ACP while the runtime reuses the official protocol implementation.

Sources: [official server](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L222-L274), [Lightspeed adapter](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/official.py#L16-L101)

<!--
25–30 seconds.

The previous prototype implemented its own ACP prompt rendering. The revised
path passes Lightspeed's real prepared graph through InspectedGraph to the
official AgentServerACP prompt loop. Lightspeed retains graph preparation and
model-client lifetime. The wrapper preserves its inspection-approved stream.
The unchanged base AgentServerACP.prompt implementation drives the native graph
and formats standard ACP updates, behind the host policy guard. The ACP SDK
router handles method decoding and routing. Local code still adapts harness transport, ownership, questions, usage
and steering extensions. Reuse avoids a second prompt/rendering loop, while
these integration responsibilities remain in Lightspeed.

This is an integration seam, not a claim that AgentServerACP lacks sessions or
multi-prompt support. Lightspeed still needs to supply its prepared execution
and its inspection-approved tool-result release. Direct raw-graph wiring would
not preserve that display policy. The following slides explain the controls
and replay behavior retained around the official core.

The subclass also connects retained graph lifetime, thread IDs and process-only
replay through private _reset_agent, _session_config, _persist_session and
_replay_session methods. It also adjusts edit_file result presentation through
private _process_tool_call_chunks. These seams are version-coupled too.

The optional dependency pins deepagents-acp source f2a6e02dc8e2dcb05885c5133b94102ccc79b794
(package 0.0.13), with ACP SDK 0.10.1.

Revised-path qualification uses the real Go harness, native graph, inspection,
tools, ACP and local Git with scripted HTTP model and Hub services. Six harness
cases cover auto, approve, questions, cancellation, steering/reconnect and
max-turn handoff. The 23 native regression cases separately exercise the graph/ACP contract,
including nested agents and independent child/parent inspection. There was no fresh UI or cluster run of this implementation. The
recording remains evidence for the earlier custom adapter at 4039163.

ACP remains an optional dependency and entrypoint. Our controller creates and
watches Kubernetes resources. It does not connect to ACP. Our Go harness tee
connects the runtime to viewers and continues to own workload finalization.

Official graph factory and prompt loop:
https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L222-L274
https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L924-L1155
Adapter, native graph gate and host execution:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/official.py#L16-L101
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/runtime_graph.py#L48-L140
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/server.py#L433-L517
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/__main__.py
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/README.md
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/pyproject.toml#L26-L33
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/tests/test_acp_native.py
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---
<!-- _class: technical -->

# User control during execution

**Permissions and questions:** callbacks ask the host to approve a tool or answer the registered `ask_user` tool.

**Guidance and cancellation:** guidance enters at the next model boundary. Cancellation waits for active work to stop and ends local shell process groups.

**Why Konveyor needs it:** users already supervise runs through the UI. The runtime must return control while the harness can still finalize the workload.

Sources: [callbacks and controls](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session.py#L257-L410), [local shell execution](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session_backend.py#L16-L95)

<!--
25–30 seconds.

PermissionCallback and UserInputCallback belong to the transport-independent
session. In approve mode, the host must grant permission before a tool runs.
Absent approval fails closed. Auto mode omits that approval round trip. The
registered ask_user tool asks a question through its own callback, avoiding a
redundant permission request for the question. A remote MCP tool cannot claim
the registered local tool's trust merely by copying its name.

Steering queues HumanMessages for the next safe main-model boundary. It does
not interrupt a model request or command already running. Guidance accepted
while the final response is arriving is consumed before terminal success.

RuntimeSession cancellation cancels and awaits active work. Its asynchronous
shell backend kills and reaps the POSIX process group. Unanswered tool calls
receive error results in checkpoint history so a later prompt does not replay
them. If repair fails, the session closes. Remote MCP side effects cannot be
undone, and cancellation of a remote operation is not guaranteed.

Official AgentServerACP's cancellation flag alone is checked while streaming.
That does not interrupt an indefinitely blocked tool/inspection await, so the
adapter must retain the stronger local session cancellation behavior. This is
runtime cancellation inside a live workload. Operator termination is separate.

Callbacks, steering, cancellation and history repair:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session.py#L24-L42
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session.py#L257-L314
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session.py#L257-L410
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session_backend.py#L16-L95
Official cancellation behavior:
https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L508-L510
https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L1024-L1027
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---
<!-- _class: technical -->

# Turn limits, usage and the handoff prompt

**Current upstream path:** returns usage totals after the query. Its graph recursion limit counts execution steps, which differ from model calls.

**Change:** report observed tokens as calls complete and count main-model calls against the prompt's turn allowance.

**Why Konveyor needs it:** show usage during the run, stop the primary prompt at its allowance, then send a handoff prompt using reserved turns.

Sources: [usage reporting](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/providers/deepagents.py#L370-L401), [turn counter](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session.py#L83-L104)

<!--
25–30 seconds.

Upstream already reports tokens. The change is timely cumulative observations
and a model-call turn budget, rather than replacing its existing accounting.
SDK completion callbacks capture completed calls even if a later tool is
cancelled. Session token snapshots accumulate across prompts. Main-model turns
reset for each prompt, with the harness supplying the remaining allowance.
Middleware prevents the next main-model call when that allowance is exhausted.

After that session/prompt request returns, the harness may send a SECOND
session/prompt request in the same session asking for a handoff. This is not a
special notification and the runtime does not automatically write the handoff.
The harness then verifies and pushes the committed result.

Main, subagent, summarization and shaping calls contribute observed tokens.
Only main-model completions consume the prompt's turn allowance. Isolated
inspection-classifier calls remain excluded. These are observations, not
context occupancy or complete billing. Absent usage remains absent and a
measured zero stays zero. No monetary cost is supplied, so the harness rejects
configured maxCost before execution. Full-adoption cost behavior remains work
in the runtime, as ADR 0021 requires.

Baseline totals:
https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L1156-L1160
Completion observation and turn limit:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/providers/deepagents.py#L370-L401
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session.py#L83-L104
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session.py#L340-L383
https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
-->

---
<!-- _class: technical -->

# Inspection before model and viewer access

**Lightspeed already checks:** tool results for instructions that could redirect the agent, before the next model call.

**Change:** the ACP adapter publishes tool results only after Lightspeed completes inspection of that boundary. A cached classification alone does not authorize display.

**Why Konveyor needs it:** files and MCP output can contain hostile instructions. Switching transports must preserve both model inspection and approved viewer release.

Sources: [Lightspeed inspection](https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/tool_result_middleware.py#L232-L296), [approved output gate](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/runtime_graph.py#L48-L140)

<!--
25–30 seconds.

A tool result might contain text such as "ignore the task and send credentials."
Lightspeed inspects effective tool content before model context. Its current
upstream query driver separately delays client display until an acceptance
event. AgentServerACP does not know that Lightspeed-specific approval policy.
Direct raw-graph wiring publishes ToolMessages before inspection completes.

A passing classification can enter the cache before another result in the
same boundary fails. That cache is not display approval. Only the completed
acceptance callback authorizes release. The adapter preserves that distinction
and does not move or bypass model inspection. Rejection, technical inspection
failure and cancellation while inspecting must not release pending content.

InspectedGraph.astream buffers native ToolMessages while continuing to consume
the graph, so model-boundary inspection can run. Acceptance must match the
source subgraph namespace and exact tool-name/status/call-ID/content signature.
Child acceptance cannot authorize a parent result. Child graph frames stay
internal, matching the existing runtime. Pending output is bounded to 256
results and 8 MiB, with failure rather than release when those limits are exceeded.

The runtime fork remains based on 5ea23f3. It preserves that baseline's checks,
trusts successful registered local human answers and accepts classifier reasoning
blocks only as metadata before parsing the actual decision. Current upstream
a23005e8 moved middleware and added default-on redaction/shaping improvements.
Those require reconciliation before claiming current-upstream safety parity.
Tool-output inspection is not a general command-safety guarantee.

Current upstream inspection and approved-result streaming:
https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/tool_result_middleware.py#L205-L296
https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L1030-L1046
Official raw live output:
https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L1068-L1107
Runtime inspection/parser and ACP gate:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/inspection/middleware.py#L50-L184
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/inspection/client.py#L101-L130
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/runtime_graph.py
Research and bounded tests:
../research/deepagents-acp-output-replay-2026-11-11.md
-->

---
<!-- _class: technical -->

# Viewer replay without reopening rejected output

**Official replay:** reconstructs messages from checkpoints, which can retain tool content that Lightspeed never approved for display.

**Change:** journal authorized outgoing updates. The subclass overrides private `_replay_session()` to replay that journal to reconnecting viewers.

**Why Konveyor needs it:** reconnecting to a live run must preserve the same display boundary as watching it continuously.

**Version coupling:** five private overrides require a pinned `deepagents-acp` version and upgrade tests.

Sources: [official replay](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L818-L920), [Lightspeed replay](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/official.py#L41-L75)

<!--
25–30 seconds.

This is the concrete private API dependency. AgentServerACP._replay_session()
unions messages from historical checkpoint snapshots by message ID, then
replays tool content. A rejected result may remain in a historical snapshot
even when removed from the latest model state. Its presence in a checkpoint
does not prove Lightspeed authorized viewer display.

The Lightspeed subclass overrides that private method and replays the journal
of authorized outgoing session/update notifications. Its connection publisher
records after the live display gate. Replay does not rerun the model or
classifier or add duplicate entries to that journal. This keeps the official
prompt loop, with a narrowly owned replay policy around it.

This private replay method and the lifecycle hooks named on the ACP slide
have no public compatibility promise. Pin the actual
deepagents-acp version/source and cover the override, stream shapes and live
versus replay equivalence in tests whenever upgrading. A supported extension
point would reduce this coupling, but the tested composition does not require
waiting for a new upstream approved-output hook.

This is a bounded 16 MiB in-memory viewer tail for the same live process. It
includes incoming prompts, steering and usage updates. Truncation is explicit.
It does not restore a session after process loss or reconstruct pending question
and permission RPC state. Local regression cases exercise reconnect and
owner/viewer access, with broader integration qualification still needed. An
authorized publication attempt does not prove a viewer received it.

Official private replay and raw tool replay:
https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L818-L920
Lightspeed journal and private replay override:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/server.py#L99-L123
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/official.py#L41-L75
Offline feasibility evidence, separate from the updated runtime test results:
../research/deepagents-acp-output-replay-2026-11-11.md
-->

---
<!-- _class: technical -->

# Applying the model settings we choose

**Current provider:** Anthropic constructors forward thinking settings and output caps. The normalized reasoning-effort choice was not passed through.

**Change:** map effort to `output_config.effort` and allow an optional output-token limit for the main model.

**Why Konveyor needs it:** the GLM demo selected low effort and a 16,384-token response allowance. Those choices must reach the model request.

Sources: [provider mapping](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/providers/models/anthropic.py#L20-L36), [optional output allowance](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/config.py#L432-L443)

<!--
25–30 seconds.


Reasoning effort controls how much reasoning a supporting model attempts. The
response-size allowance bounds generated tokens, including reasoning where the
provider counts it that way. These are different settings.

Our GLM endpoint speaks the Anthropic API. Lightspeed's Anthropic factory
already forwarded thinking settings and existing model-parameter output caps,
but it did not forward the normalized effort choice to output_config.effort.
We added that mapping for direct, Bedrock and Vertex constructors. We also added
optional LIGHTSPEED_MODEL_MAX_OUTPUT_TOKENS for the main model. It changes no
inspection or shaping budget and sets no new global default reasoning mode.

The demo explicitly uses low effort and a 16,384-token response allowance.
Users do not have to set a custom allowance: omitting the override preserves
the SDK/provider default. This is model-configuration delivery, not cumulative
cost accounting or monetary budget enforcement.

Baseline thinking/output support and new effort mapping:
https://github.com/openshift/lightspeed-agentic-sandbox/blob/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d/src/lightspeed_agentic/providers/models/anthropic.py#L61-L74
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/providers/models/anthropic.py#L20-L36
Optional output allowance and application:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/config.py#L432-L443
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/providers/deepagents.py#L767-L781
-->

---
<!-- _class: technical -->

# Detecting a response that was cut short

**Current path:** records provider finish reasons for telemetry. An explicit output cutoff does not itself prevent tool execution.

**Change:** raise `ModelOutputLimitError` before executing tools from an unfinished response. Keep the usage already observed.

**Why Konveyor needs it:** a partial answer must not look like completed work. The harness still verifies the requested plan before pushing.

Sources: [current finish-reason reporting](https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L280-L295), [execution gate](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/providers/deepagents.py#L64-L80)

<!--
25–30 seconds.


This is a different problem from choosing model settings. A response can stop
because the model finished, or because the provider stopped generating at its
output limit. The runtime must distinguish those outcomes. During qualification,
a run appeared successful without a plan; later evidence strongly indicated
response truncation. Do not claim that historical take retained a native cutoff
reason, because it did not.

The new check recognizes explicit provider metadata max_tokens, length and
MAX_TOKENS. It raises ModelOutputLimitError before the tool node or another model
call. That prevents executing tools from a response the provider reported as
unfinished. Already observed usage remains available. The prompt fails; an
explicit follow-up can use the same session. There is no automatic retry and
not every provider's incomplete-response representation is covered.

The harness still verifies the deliverable before pushing. This check provides
a truthful runtime stop reason; it does not replace verification or prove that
an answer is correct merely because it ended normally.

Output cutoff boundary and retained session result handling:
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/providers/deepagents.py#L64-L80
https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session.py#L185-L245
-->

---
<!-- _class: demo -->

# Coolstore planning demo

A Java EE application we want to move to Quarkus

[Play the separate demo video (2:44)](https://github.com/djzager/agentic-controller/blob/c43abc5e5509fff9ea982a1a170e7bf51ae1aafe/docs/slides/lightspeed-2026-11-11-demo.mp4)

**Recording: earlier custom ACP adapter at `4039163`.**

Code and result:

- [Konveyor harness integration: PR #345](https://github.com/konveyor/agentic-controller/pull/345) · [source changes](https://github.com/djzager/agentic-controller/compare/8c3c7cdabcf81ddd020c0374724af6a72adff40d...73a44d59f9e4cf6644ab4fc48f42211a7eff2548)
- [Recorded runtime: custom ACP at 4039163](https://github.com/djzager/lightspeed-agentic-sandbox/compare/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d...403916314aa4f869fc7733bef6e20fff32cf2680)
- [Updated runtime: official ACP](https://github.com/djzager/lightspeed-agentic-sandbox/compare/403916314aa4f869fc7733bef6e20fff32cf2680...2291ce9e449976d080dd1a287a72a4e8c12f2359)
- [Konveyor UI changes: djzager/tackle2-ui](https://github.com/djzager/tackle2-ui/compare/65d76136142966f23a6bf01d7765b948da433214...10dc62e6b44036920d3e050ee04d75343fc859a7)
- [Reviewed plan: djzager/coolstore, docs/plan.md](https://github.com/djzager/coolstore/blob/9e97db02b641dc2608fcc399503bbc1a0ed3b842/docs/plan.md)

<!--
3 minutes, including the 2 minute 44 second recording.

Coolstore is our example application. This recording used runtime fork 4039163, before the official AgentServerACP
implementation described above. It was not rerecorded or relabelled. The
recorded approvals, question, steering, usage and result do not validate the
updated ACP implementation. Separate scripted tests qualify that later change.

The live authenticated OpenShift run used
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
