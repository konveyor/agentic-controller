# Explaining the Lightspeed runtime changes to its code owners

Source review, November 11, 2026. These are speaking notes for the runtime
discussion, not operator requirements. The accepted boundary is
[ADR 0021](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md):
Lightspeed owns model/tool execution, inspection and usage; the Go harness owns
Hub/repository preparation, verification, push, token cleanup and finalization.

Source pins: original Lightspeed base `5ea23f34ad2ad5fc01fb6264fb9304116664dd3d`;
reviewed current upstream `a23005e8f9ed0edc6e430072f50e4698590de71f`; original
Konveyor runtime prototype `403916314aa4f869fc7733bef6e20fff32cf2680`; official
DeepAgents ACP `f2a6e02dc8e2dcb05885c5133b94102ccc79b794`. Links to the original
prototype describe its demonstrated implementation, not the subsequent official
ACP migration described below.

## Implemented follow-up

The revised fork is pinned at
[`2291ce9`](https://github.com/djzager/lightspeed-agentic-sandbox/tree/2291ce9e449976d080dd1a287a72a4e8c12f2359).
The original prototype citations below establish the before/after context;
these are the updated entry points to show the Lightspeed authors:

- [`DeepAgentsProvider.prepare_graph()`](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/providers/deepagents.py#L711)
  exposes the existing model, backend, skills, admitted MCP tools and inspection
  preparation without starting a prompt. The batch query path reuses it.
- [`RuntimeSession.prepare_graph()` and `graph_execution()`](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/session.py#L106)
  keep the actual graph/checkpointer and clients, serialize execution, and retain
  callbacks, cancellation and model-turn accounting.
- [`InspectedGraph.astream()`](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/runtime_graph.py#L47)
  forwards native graph frames after the completed inspection boundary. It
  continues consuming while buffering, matches exact result signatures and
  source namespaces, and keeps child graph events private.
- [`ACPConnection.consume()`](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/server.py#L433)
  invokes the unchanged base `AgentServerACP.prompt()` with that native graph.
  Host policies, custom extensions and authenticated WebSocket framing remain
  local. This is reuse of the official prompt loop and rendering, rather than
  elimination of all integration code.
- [`LightspeedACPServer`](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/official.py#L16)
  adapts graph selection/thread configuration and authorized journal replay.
  Its [contract](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/README.md#official-server-and-native-graph-composition)
  identifies all five private helper dependencies, including metadata and
  edit-result presentation, and their upgrade compatibility tests.

The [verification record](../../hack/lightspeed-runtime/official-acp-reference.json)
separates the new scripted-model/native-graph tests from the historical live
recording at `4039163`. No new browser or cluster run accompanies this revision.

## 1. A session must outlive one prompt

**Say:** “Your `query()` builds and tears down the execution resources for one
query. We need a supported preparation/session boundary so the harness can send
a handoff prompt to the same conversation before closing it.”

The restriction is in Lightspeed's query wrapper. Current upstream creates a
chat model at line 811, backend at 815, compiled graph at 1002, fresh UUID
`thread_id` at 1004, and an input containing only this call's prompt at 1016.
No checkpointer is supplied in that graph construction. Its `finally` closes
the chat and classifier clients at 1135–1140. The original base has the same
[query-owned graph/thread](https://github.com/openshift/lightspeed-agentic-sandbox/blob/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d/src/lightspeed_agentic/providers/deepagents.py#L700-L710)
and [client cleanup](https://github.com/openshift/lightspeed-agentic-sandbox/blob/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d/src/lightspeed_agentic/providers/deepagents.py#L877-L884).
See current [preparation](https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L811-L832),
[graph/config](https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L1002-L1016)
and [cleanup](https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L1135-L1140).

A second `query()` call is possible; it constructs another execution. A UUID
alone does not preserve graph history. The original prototype retained graph,
`InMemorySaver`, classifier/model clients and stable thread until explicit
[session close](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L999-L1036).
Official `AgentServerACP` already retains a graph, ensures a `MemorySaver`, and
uses session ID as thread ID on later prompts:
[official session config](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L792-L811),
[official prompt setup](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L948-L995).
Lightspeed needs to expose its prepared graph and resource lifetime to that
host. It does not need a second conversation engine.

The concrete sequence is primary `session/prompt` → limit stop response →
second `session/prompt` with the **same session ID**, asking the agent to write
`.konveyor/handoff.md` → harness verification/push → session/process close.
The handoff request is a normal ACP prompt request, not a generic notification.
[ACP prompt lifecycle](https://agentclientprotocol.com/protocol/v1/prompt-turn).
Live-process continuation does not imply durable recovery after Pod loss.

## 2. Tool approval and asking a question are separate interactions

**Say:** “We connected our existing approval and question policies at the tool
execution boundary. Approval returns a decision before execution; `ask_user`
returns the person's answer as input to the agent.”

The original prototype's [permission middleware and local question tool](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L290-L360)
implement fail-closed approve mode and a separately enabled user-input callback.
Official ACP already handles DeepAgents HITL action-request interrupts, calls
`request_permission`, and resumes with decisions; use that existing protocol
machinery where it fits. [Official interrupt handling](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L1165-L1318).
The reviewed generic server does not implement Konveyor's local `ask_user`
callback/elicitation bridge. Its warning about unsupported free-form LangGraph
interrupts describes that implementation, not the entire ACP protocol:
[official prompt handler](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L1031-L1055).
ACP itself defines [elicitation requests and negotiated capabilities](https://agentclientprotocol.com/protocol/v1/elicitation).
An SDK schema or extension method name alone does not implement the callback.

## 3. Host Lightspeed's graph in official ACP; adapt the harness transport

**Say:** “The official server can host your compiled DeepAgents graph. We need
to expose that graph without losing your preparation, tool admission, callbacks
or output-release behavior, then connect its transport to our harness.”

`AgentServerACP` accepts a compiled graph or synchronous factory. Lightspeed
preparation is asynchronous because it discovers/materializes admitted MCP
tools, so simply replacing the CLI module does not expose the right boundary.
Prepare asynchronously before handing off the graph, or agree an async factory
contract. Preserve model routing/TLS, skills, admitted MCP names, collision
checks, inspection and subagent middleware. [Official constructor](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L223-L245),
[Lightspeed graph preparation and MCP filtering](https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L940-L1002).

Official examples use SDK stdio `run_agent(server)`; Konveyor's harness currently
speaks authenticated WebSocket ACP. An internal bridge or stdio client adapts
that transport; viewer authorization, fan-out and fail-closed answering remain
harness responsibilities. Choosing official ACP does not require its example
agent or dcode's prebuilt graph. [Official custom-agent example](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/README.md#L84-L130),
[harness launcher](https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/harness/internal/runtime/lightspeed.go#L47-L119).

## 4. Steering and cancellation need execution semantics

**Say:** “Guidance is queued for the next main-model boundary. Cancellation
stops and awaits active work while leaving a usable conversation for follow-up.”

The original prototype adds a steering queue consumed before the next model
call, handles guidance arriving during the final response, cancels/awaits the
active task, and repairs unanswered tool calls in checkpoint history before
continuation. Its async local shell backend kills and reaps the local process
group on cancellation/timeout. [Session behavior](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L152-L273),
[shell lifecycle](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session_backend.py#L16-L95).
This does not interrupt a completed side effect or guarantee remote MCP cleanup.

Official ACP has cancellation already: `cancel()` sets a flag and `prompt()`
checks it between streamed events. That alone does not prove interruption of an
active long-running tool await. The reviewed server has no Konveyor steering
implementation. Reusing its prompt loop still requires the stronger local
execution/steering integration. [Official cancellation flag](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L508-L510),
[stream polling](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L1004-L1027).
Runtime cancellation and operator termination of the workload are different
controls.

## 5. Count explicit turns and report observed usage during execution

**Say:** “Your wrapper uses a graph recursion limit and returns token totals at
the end. We need explicit main-model turns and completion-time usage updates so
the harness can reserve a handoff allowance and report progress.”

Upstream passes `max_turns` directly to LangGraph `recursion_limit`; graph
supersteps are not an explicit main-model completion counter. It emits the
normalized token totals in the final `ResultEvent`.
[Current upstream config](https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L1004-L1014),
[final totals](https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L1156-L1160).
The original prototype observes completed model calls through SDK callbacks,
retains observed token counts even if a later tool is cancelled, and prevents
the next main-model call after its prompt allowance. The harness supplies the
reserved allowance on the second prompt. [Completion observer](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L370-L401),
[counter and token snapshots](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L81-L99),
[model-call gate](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session.py#L306-L311).

This observed usage includes main, subagent, summarization and shaping calls;
isolated inspection-classifier calls are excluded. Only main-model completions
consume the turn allowance. The old adapter projects token snapshots through a
Lightspeed extension, not standard context-window occupancy. It supplies no
monetary price: configured `maxCost` remains rejected by the harness, and
full-adoption monetary reporting/enforcement is outstanding. Official ACP's
reviewed prompt config/loop does not add these Lightspeed budget callbacks.
[Extension and stop-reason projection](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/acp/server.py#L453-L469).

## 6. Preserve the model gate and inspection-approved client output

**Say:** “Keeping your middleware protects the model boundary. We also have to
preserve the query driver's delayed client release, including viewer replay.”

Current upstream redacts tool output in `awrap_tool_call`, then classifies it in
`awrap_model_call`; the query driver buffers results until the one-boundary
`lightspeed.accepted_tool_results` acknowledgement. These are separate steps.
[Current middleware](https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/tool_result_middleware.py#L188-L268),
[query-driver release](https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L1028-L1046).
The original fork retains its older base's acceptance gate, but predates current
upstream redaction; do not claim current-upstream safety parity for `4039163`.

Unmodified official ACP publishes a ToolMessage's content immediately and its
checkpoint replay republishes tool content without checking Lightspeed
acceptance. Direct graph wiring therefore retains the model gate but changes
the viewer-release boundary. A Lightspeed stream gate plus journal of approved
published updates can protect both live output and same-process replay while
inheriting the official prompt loop. Overriding `_replay_session` couples to a
private method at this pin; keep a version/compatibility test or seek a supported
hook. [Official live output](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L1075-L1105),
[checkpoint replay](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L818-L840),
[tool replay method](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L895-L921).

The original fork also trusts only the registered local human-input response
and rejects an admitted MCP tool that collides with that `ask_user` name. This
keeps actual human instructions out of untrusted-tool classification without
giving arbitrary tools that trust. [Trust registration and collision guard](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L906-L915),
[collision rejection](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L979-L990).
Inspection is tool-output prompt-injection defense, not blanket command safety.

## 7. Deliver the selected reasoning/output settings to the provider

**Say:** “Your Anthropic constructors already forwarded thinking and model
output caps. The normalized reasoning-effort choice needed an additional
`output_config.effort` mapping; we also exposed an optional main-model cap.”

This fixes a dropped setting in the model adapter, not a DeepAgents/ACP
conversation limitation. The GLM demo endpoint speaks Anthropic's API; low
effort and a 16,384-token allowance are explicit demo settings. Omitting the
override preserves provider/SDK defaults. Reasoning effort, per-response output
allowance and cumulative cost are separate controls.
[Original thinking/cap support](https://github.com/openshift/lightspeed-agentic-sandbox/blob/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d/src/lightspeed_agentic/providers/models/anthropic.py#L61-L74),
[effort mapping](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/models/anthropic.py#L20-L36),
[main-model override](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L763-L776).

## 8. Fail an explicitly incomplete generation before executing its tools

**Say:** “A provider-reported output cutoff must not look like normal prompt
completion or execute tool calls from that unfinished response.”

Upstream records finish reasons for telemetry; that alone is not an execution
gate. The prototype's model middleware recognizes explicit `max_tokens`,
`length` and `MAX_TOKENS` metadata and raises `ModelOutputLimitError` before the
tool node or next model call. Already observed usage remains available; an
explicit follow-up can reuse the session. It does not automatically retry or
claim every provider's incomplete representation is covered.
[Upstream finish-reason telemetry](https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L280-L295),
[prototype execution gate](https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L59-L80).
The reviewed official ACP server ordinarily returns `end_turn` after its graph
finishes; retaining this Lightspeed middleware is what supplies the stronger
cutoff behavior. [Official terminal response](https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L1126-L1141).

## Phrases to avoid

- “Lightspeed/DeepAgents can only send one prompt.” The limitation is the
  existing Lightspeed query entrypoint's resource/checkpoint ownership.
- “We added approval/cancellation to ACP.” Official ACP already supports them;
  this is host policy and stronger execution integration.
- “The middleware makes official ACP output safe automatically.” Its client
  release/replay paths need Lightspeed's acceptance boundary too.
- “We added token accounting.” Upstream already accounts for tokens; the
  contribution changes observation timing, session scope and control semantics.
- “The operator needs to change for session continuation.” These changes are
  inside the workload runtime and harness; operator adoption is separate.
