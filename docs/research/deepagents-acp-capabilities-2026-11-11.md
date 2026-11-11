# Can official DeepAgents ACP replace the prototype server?

Checked November 11, 2026 against primary source and offline executable probes.

**Unchanged drop-in: no. Reuse as the ACP base while retaining Lightspeed's
DeepAgents implementation: yes, with specific integration work.**
The official server can execute the actual graph assembled by Lightspeed. It
already supplies multi-prompt conversation history, checkpoint replay and HITL
permission handling. We do not need to implement those again. Direct wiring
nevertheless loses required behavior: most critically, inspection-approved
viewer release and prompt cancellation that stops active work. It also lacks
our steering, question-tool wiring, usage/budget projection and host policy.
These are concrete gaps in the unchanged generic server, not proof that ACP or
LangGraph cannot support them. [Constructor][constructor], [prompt loop][prompt].

**Focused follow-up:** [inspection-approved output and replay](deepagents-acp-output-replay-2026-11-11.md)
demonstrated a Lightspeed-owned graph wrapper and viewer-update journal using
the official prompt loop unchanged. An upstream approved-output hook is not
necessary for that tested composition; replay overrides a private server method.

This is evidence for the direction in [ADR 0021](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md),
not a new transport decision. Runtime reuse, optional Lightspeed operator
qualification and eventual resource migration remain separate. The
[ADR 0016 default](../adr/0016-gateway-crd-as-interim-execution-interface.md)
is unchanged. Hub/Git preparation, verification, push credentials, Git push,
Hub-token cleanup and core outcomes remain Go harness responsibilities.

## Source and execution pins

- Official `deepagents-acp` source, version declared `0.0.13`:
  [`langchain-ai/deepagents@f2a6e02dc8e2dcb05885c5133b94102ccc79b794`][package].
- Actual executed official code: that commit's unmodified `server.py` and
  `utils.py`, rather than assuming a published wheel with the same version has
  identical source. Scripted chat-model helper also comes from that commit.
- Executed dependencies: DeepAgents `0.7.22`, LangChain `1.4.3`, LangGraph
  `1.2.14`, ACP Python SDK `0.10.1`. These match the existing Lightspeed lock for
  the graph dependencies. ACP SDK `0.10.1` is the official package's minimum;
  its immutable tag resolves to `8cd8391e4e5031d734044d8120a89f988a62b54b`.
- Lightspeed graph comparisons: prototype fork
  `403916314aa4f869fc7733bef6e20fff32cf2680` and current upstream
  `a23005e8f9ed0edc6e430072f50e4698590de71f`.
- Newer ACP SDK `1.0.0`, pin
  `9853d8b514d6340000bcbd01ec07808436db80cf`: question-routing source inspected
  separately; the executable matrix was not rerun against that SDK.

The official server imported and executed with the locked Lightspeed graph
versions. This establishes the measured paths below, not complete dependency
compatibility or production qualification. No paid model calls, cluster, UI,
existing venv/lock modification, runtime implementation change or deployment was
performed. The capability probe disables outgoing Python socket connections and
tracing; the shell fixture only touches files in a temporary directory.

## WORKS: reuse these existing capabilities

**Multiple prompts in one conversation, including a later handoff prompt.**
Official `prompt()` uses the ACP session ID as the graph thread and supplies a
memory checkpointer when one is absent. Two actual `create_agent` invocations
retained task, answer, handoff request and handoff answer in one four-message
history. This satisfies conversation continuation; the graph/runtime must still
implement how and when a limit stops the primary work. Factory users must retain
the appropriate checkpointer when rebuilding graphs. [Thread config][config],
[prompt/checkpointer][prompt].

**Session load/replay.** With `load_sessions=True` and a supplied checkpointer,
a second server instance replayed the four messages and rejected a different
cwd. Official code verifies persisted ACP session metadata and restores stored
mode/model options. Our probe reused `MemorySaver` in one process, so it did not
prove disk persistence or restart recovery. Durable recovery requires a durable
checkpointer. Replay is not Konveyor's owner/viewer authorization policy.
[Load validation][load], [replay][replay].

**Tool permissions through LangChain HITL.** Real
`HumanInTheLoopMiddleware(interrupt_on=...)` produced one official ACP
permission request. Selecting approve executed the fixture tool; reject did
not. The graph must install that middleware: naming a session mode does not by
itself gate tools. The official handler implements approve/reject and persistent
allow choices, including exact-command allowlisting for `execute`.
[Permission handler][permissions], [official HITL tests][hitl-tests].

**Lightspeed's graph, model-input inspection and newer upstream redaction.**
Twelve comparative cases built the real graph through each Lightspeed provider
and ran it through either the original query driver or the unmodified official
server: two source revisions × two drivers × classifier pass/rejection/exhausted
technical failure. Model, one local tool, classifier and ACP client were
scripted; graph and middleware were real. All assertions passed. Reject/failure
prevented a second main-model call under both drivers. Current upstream's
default redaction sanitized the password fixture before viewer, classifier and
model input. These are separate properties from approved viewer release below.
[Lightspeed graph preparation][ls-builder], [redaction/inspection middleware][ls-middleware].

## NEEDS OUR GLUE: preserve runtime and host responsibilities

**Expose a supported Lightspeed graph/resource seam.** Official
`AgentServerACP` accepts a compiled graph or synchronous graph factory. Lightspeed
already builds that graph with `create_deep_agent(**agent_kwargs)`, after provider
resolution, skills, MCP admission/materialization, inspection and summarization
setup. Current `query()` also owns clients, callbacks, approved event release,
optional output shaping and cleanup. Extract reusable preparation and ownership
instead of copying that implementation or selecting the official demo agent.
Our diagnostic captured the construction seam; that capture technique is not a
production API. Prebuilding a graph is viable; an asynchronous preparation
contract is needed if dynamic per-session setup is required. A graph alone does
not preserve the query driver's output shaping or client cleanup.
[Constructor][constructor], [Lightspeed builder][ls-builder], [driver and cleanup][ls-driver].

**Transport, viewers and workload policy.** Current harness `WSClient` dials
`/acp`; the tee creates a distinct upstream WebSocket per viewer. Official
examples run the SDK over stdio. Changing only the Python module name therefore
cannot work. A stdio-capable harness transport or internal bridge can retain the
external authenticated WebSocket endpoint and viewer fan-out. This is harness
integration work, not missing ACP protocol support. Preserve first-answer-wins
permission/question delivery, timeout/fail-closed behavior, owner versus observer
rights and forwarding cancel/steer to the owner. Pin workspace, admitted tools,
executable and approval floor at the launcher boundary. The generic server
accepted an approve→auto mode switch in the probe; it has no Konveyor policy
floor. It also allowed two graph invocations for the same session to overlap;
the owner needs prompt serialization or rejection. [Harness WS client][ws-client],
[viewer upstream connections][tee-viewers], [official startup][startup],
[mode changes][modes], [graph reset][reset].

**Wire the question tool; distinguish SDK versions.** A real `ask_user` tool
using free-form `interrupt("Which database?")` raised official `RequestError`
`-32600`; no question reached the client. The generic server supports HITL action
requests, not arbitrary question interrupts. This does not imply that ACP cannot
carry questions. SDK `0.10.1` has elicitation schemas but no public typed
`AgentSideConnection.create_elicitation` method/client route; public
`ext_method()` prefixes `_`, so it does not directly preserve Konveyor's current
non-underscored `elicitation/create` name. A raw connection/explicit bridge or
coordinated extension is required at that version. **SDK `1.0.0` adds typed
`create_elicitation(message, mode, **kwargs)` and the client route using
`elicitation/create`.** That reduces the bridge work. Neither SDK version wires a
question tool into generic `AgentServerACP` automatically. Retain opt-in Ask User,
answer validation, timeout/fail-closed semantics and the distinction from tool
approval. [Generic interrupt rejection][interrupt-rejection],
[0.10.1 extension method][sdk-ext], [1.0.0 question method][sdk1-question],
[1.0.0 typed client route][sdk1-question-route].

**Usage, cost and per-prompt budgets.** The unchanged official server ignored
`_meta.lightspeed.maxTurns=1`: our graph completed three model calls and two tools,
then returned `end_turn`. Each AI message retained 10 input/2 output tokens, but
the server emitted no usage updates and no response usage. This is missing
projection/configuration, not missing SDK data. A small probe-only server
subclass supplied a completion callback through `_session_config`; existing
`ModelCallLimitMiddleware(run_limit=1)` then allowed one observed model call and
one tool. The callback obtained its tokens. The server still reported
`end_turn`, demonstrating the remaining need for explicit limit reason/turn
measurement mapping and remaining-budget configuration for handoff. Native SDK
usage response/update types exist. Keep accounting in reusable Lightspeed code;
do not add a parallel Konveyor pricing estimator. No source in this experiment
supplies monetary cost: production maxCost still needs timely runtime cost
reporting and effective cancellation. The current prototype harness rejects
configured maxCost before execution. [Official config][config], [prompt][prompt],
[SDK response usage schema][sdk-usage], [SDK usage notification support][sdk-updates],
[prototype budget validation][maxcost].

**Cutoffs and safe error projection.** An AI message carrying native
`stop_reason=max_tokens` finished as `end_turn` in the generic server without
graph middleware. Lightspeed's fork already has cutoff middleware that can
remain on the graph; the host must preserve failure/outcome mapping. SDK
`0.10.1` converted a fixture graph exception to JSON-RPC `-32603` and included the
exception text in `error.data.details`. Preserve payload-safe runtime errors and
inspection failure behavior rather than assuming that generic internal-error
handling provides redaction. The probe used a harmless fixture string.
[Cutoff middleware][cutoff], [SDK error projection][sdk-errors].

## REAL GAPS in the unchanged server: targeted extension/contribution candidates

**Inspection-approved viewer release, including replay.** Direct official
execution emitted a completed `tool_call_update` containing fixture tool output
before the classifier ran, including cases later rejected or failing inspection.
The model gate still blocked the next model call; current upstream redaction
still ran. Lightspeed's original driver instead buffered normalized tool results
until its one-boundary `lightspeed.accepted_tool_results` acknowledgement and
released none on rejection/failure. Official `prompt()` requests `messages` and
`updates`, not that `custom` acceptance stream, and immediately publishes raw
ToolMessages. Its replay path also emits persisted tool content without the
acceptance gate. The [focused follow-up](deepagents-acp-output-replay-2026-11-11.md)
measured that replay exposure and demonstrated a graph output gate plus an
authorized viewer-update journal, with no copied prompt loop or upstream hook.
Thirteen live cases and 38 replay cases passed, including nested and mixed
sibling acceptance, rejection and inspection failure. The replay subclass uses
private `_replay_session`; production compatibility, transport concurrency and
complete conversation state remain unqualified. There is no dedicated public
approved-output filter constructor argument, but its absence does not prevent
the tested Lightspeed-owned composition. Moving inspection solely to tool
return is not an established substitute for the existing post-transformation
model-boundary check. [Official stream][prompt],
[official replay][tool-replay], [Lightspeed approved release][ls-gate].

**Active task cancellation.** Official `cancel()` only sets a session flag;
`prompt()` checks it between stream chunks and before another resume. In the
probe, cancel returned while a graph node was still blocked; the prompt remained
blocked until we explicitly released it. It also remained blocked waiting for a
permission answer. A real LocalShellBackend command completed its delayed file
effect after cancel, then the prompt eventually returned `cancelled`. This is
not cancellation of active work. The SDK can run request/notification handlers
concurrently and cancels handler tasks on connection shutdown, but session
cancel does not invoke that shutdown. An adapter task registry/cancel-and-await
hook must cancel model/tool work and repair outstanding tool history; the local
backend must kill/reap shell groups when cancellation arrives. Our fork already
implements the latter. Remote MCP side effects cannot be rolled back by this.
[Official cancel][cancel], [prompt flag checks][prompt], [SDK task shutdown][sdk-shutdown],
[cancellable shell backend][shell-cancel].

**Active steering and run identity.** Calling the existing Konveyor
`_goose/unstable/session/steer` route through SDK `0.10.1` returned null and
performed no steering. The generic server inherits the protocol's empty
extension method rather than implementing this behavior. An extension handler
can queue guidance and graph middleware can consume it at the next safe
main-model boundary; preserve active run ID validation, user-message identity,
idle errors and guidance arriving at final response. The SDK's extension router
provides a seam. This is additional control behavior, not a reason to rewrite
ordinary ACP requests. [SDK extension routing][sdk-router], [generic prompt][prompt].

The specialized `deepagents_code.acp.AgentServerACP` at the same official pin
adds trusted Auto-classifier context around its own graph. That class does not
itself add question, usage, steering, approved-output or active-cancellation
handlers. The rest of the Dcode CLI's graph setup was not qualified by this
comparison; this class alone establishes no complete substitute for the missing
generic-server behavior. Choosing its prebuilt graph would also change the
Lightspeed implementation we intend to reuse. [Dcode specialization][dcode].

## Reproduction and measured results

Supporting diagnostics are [the 13-group capability probe](probes/deepagents_acp_capabilities.py)
and [the 12-case Lightspeed safety comparison](probes/lightspeed_acp_safety.py).
They are research fixtures, not runtime implementation or product tests.
The capability probe uses real LangChain/LangGraph execution with scripted model
responses, a recording ACP client, and a real local shell fixture. Its SDK wire
error case calls the actual request/error projection with a recording sender;
it does not open a network transport. The safety probe's docstring describes
its model/classifier substitutions and source-selection requirements.

The original measurements used a private venv with the source staged at the
pinned revision. The revised runtime fork now installs that exact official Git
revision with its frozen `dev,acp` dependencies; installed server source was
verified byte-for-byte against the research copy. Use that locked environment
for reproduction rather than a same-version wheel with potentially different
source or an incidental framework upgrade. The preserved capability script
uses LangChain's public scripted model with a local tool-binding override; it
has no uncommitted helper dependency.

```sh
/path/to/runtime-fork/.venv/bin/python -B \
  docs/research/probes/deepagents_acp_capabilities.py \
  > /path/to/private-evidence/probe-results.json
```

The 13 asserted capability groups completed successfully, meaning the following
observations held; several deliberately assert missing behavior:

1. Two prompts retained four messages; load replayed all four; wrong cwd failed.
2. HITL approve executed the tool after one permission request.
3. HITL reject prevented its effect after one permission request.
4. Client mode downgrade from approve to auto was accepted.
5. Free-form question interrupt raised `RequestError -32600` without a question.
6. Steering returned null; cancel did not unblock an active node within 100 ms.
7. Cancel did not unblock a pending permission request; its eventual reply did
   not execute the tool because the cancellation flag was then observed.
8. A local shell effect completed after cancel; eventual result was cancelled.
9. Requested maxTurns=1 still allowed three model calls/two tools; usage stayed
   in checkpoint messages and was absent from ACP response/updates.
10. Native max_tokens metadata without graph middleware returned end_turn.
11. Existing model-call-limit middleware plus callback limited execution to one
    model call/one tool and observed 10 input/2 output tokens; ACP still said
    end_turn, so explicit limit projection remains required.
12. Two prompts in one session reached two simultaneous graph invocations.
13. SDK error normalization exposed the fixture exception in data.details.

The 12 safety comparisons all passed their expected assertions. Official ACP
released tool output before classification; original Lightspeed released it
only on pass. Both stopped subsequent model execution on rejection/failure;
current upstream redacted the credential fixture in both drivers. Full traces
are local `.scratch/deepagents-acp-check/lightspeed-safety-{fork,upstream}.json`;
the diagnostics reproduce them. These were single-tool, top-level cases, not
qualification of all subagent/MCP, replay, provider cleanup, concurrent viewer,
shell-process or long-running transport behavior.

## What this evidence permits us to say

We can replace our Python protocol implementation with an integration based on
official `AgentServerACP`, while reusing Lightspeed's real graph and upstream
ACP request/permission machinery and a Lightspeed-owned authorized-update
replay path. We cannot replace it unchanged today
and claim ADR 0021 behavior is preserved. A defensible integration retains
Lightspeed graph/resource preparation and runtime controls, adds the tested
output gate/journal and the remaining cancellation and interaction integration,
and adapts the Go harness transport and viewers. The exact API contribution and transport choice remain design
work; no backend switch, new resource contract or production parity was proved.

[package]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/pyproject.toml#L5-L31
[constructor]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L222-L274
[config]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L792-L812
[prompt]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L948-L1141
[load]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L394-L428
[replay]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L818-L920
[tool-replay]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L895-L920
[permissions]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L1165-L1321
[hitl-tests]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/tests/test_agent.py#L769-L856
[modes]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L430-L503
[reset]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L931-L946
[interrupt-rejection]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L1029-L1059
[cancel]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L508-L510
[startup]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/README.md#L84-L130
[dcode]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/code/deepagents_code/acp.py
[ls-builder]: https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L811-L1002
[ls-driver]: https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L1002-L1160
[ls-gate]: https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L1026-L1128
[ls-middleware]: https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/tool_result_middleware.py#L188-L296
[cutoff]: https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/providers/deepagents.py#L64-L80
[shell-cancel]: https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/session_backend.py#L16-L95
[ws-client]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/harness/internal/acp/wsclient.go#L65-L103
[tee-viewers]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/harness/internal/tee/tee.go#L678-L807
[maxcost]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/harness/internal/runtime/runtime.go#L46-L59
[sdk-ext]: https://github.com/agentclientprotocol/python-sdk/blob/8cd8391e4e5031d734044d8120a89f988a62b54b/src/acp/agent/connection.py#L203-L207
[sdk-router]: https://github.com/agentclientprotocol/python-sdk/blob/8cd8391e4e5031d734044d8120a89f988a62b54b/src/acp/agent/router.py#L114-L125
[sdk-usage]: https://github.com/agentclientprotocol/python-sdk/blob/8cd8391e4e5031d734044d8120a89f988a62b54b/src/acp/schema.py#L3109-L3140
[sdk-updates]: https://github.com/agentclientprotocol/python-sdk/blob/8cd8391e4e5031d734044d8120a89f988a62b54b/src/acp/agent/connection.py#L80-L100
[sdk-errors]: https://github.com/agentclientprotocol/python-sdk/blob/8cd8391e4e5031d734044d8120a89f988a62b54b/src/acp/connection.py#L209-L254
[sdk-shutdown]: https://github.com/agentclientprotocol/python-sdk/blob/8cd8391e4e5031d734044d8120a89f988a62b54b/src/acp/task/supervisor.py#L69-L80
[sdk1-question]: https://github.com/agentclientprotocol/python-sdk/blob/9853d8b514d6340000bcbd01ec07808436db80cf/src/acp/agent/connection.py#L280-L285
[sdk1-question-route]: https://github.com/agentclientprotocol/python-sdk/blob/9853d8b514d6340000bcbd01ec07808436db80cf/src/acp/interfaces.py#L230-L243
