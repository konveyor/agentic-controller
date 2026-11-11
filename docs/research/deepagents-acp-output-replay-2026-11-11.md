# Inspection-approved output and replay with AgentServerACP

November 11, 2026. Focused follow-up to the
[ACP capability investigation](deepagents-acp-capabilities-2026-11-11.md).
This records offline feasibility tests, not an implementation decision or a
production replacement.

Implementation follow-up: the runtime fork now applies the native graph gate
and authorized journal approach at
[`2291ce9`](https://github.com/djzager/lightspeed-agentic-sandbox/tree/2291ce9e449976d080dd1a287a72a4e8c12f2359).
Its [composition contract](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/README.md#official-server-and-native-graph-composition)
and [verification record](../../hack/lightspeed-runtime/official-acp-reference.json)
describe the larger integration and its limits. The experiments below remain
the evidence for the focused feasibility finding.

## Finding

**The tested output inspection and viewer-replay behavior can live in
Lightspeed-owned code while reusing the official AgentServerACP prompt loop.**
A new upstream approved-output hook is not necessary for the tested composition.
The earlier capability investigation overstated that need.

The composition has two responsibilities:

1. A graph wrapper holds tool results until Lightspeed reports completed
   inspection of the relevant model-input boundary, then lets AgentServerACP
   publish them.
2. A viewer-update journal records authorized outgoing updates. Reconnecting
   viewers receive that journal instead of a reconstruction from graph
   checkpoints.

The official server accepts the wrapped graph through its graph factory. The
experiment leaves its `prompt()` implementation unchanged. Replay uses a small
subclass overriding **private** `_replay_session`, plus a connection publisher
wrapper. This works at the pinned version; it is not a guaranteed public replay
extension interface. [Official factory][factory], [official replay][replay],
[first-party graph-proxy precedent][proxy].

## Why the distinction matters

Lightspeed checks effective tool results before the next model call. An
individual result can enter its classification cache before another result in
the same batch fails. Only completion of the entire boundary emits the
acceptance callback. **The classification cache is not permission to display
each cached result.** The model sees wrapped copies; checkpoint ToolMessages
retain effective content without completed display-approval evidence.
[Inspection implementation][inspection], [accepted-result stream events][events].

AgentServerACP does not know this Lightspeed-specific policy. Direct wiring
publishes tool-result content before inspection completes. Its default replay
unions messages from all checkpoint snapshots by message ID and publishes tool
content without checking Lightspeed acceptance. Tests reproduced rejected,
inspection-failed and pending-at-cancellation results appearing on replay.
Removing rejected output from the latest model state did not remove it from
that historical union. [Official live output][live], [checkpoint replay][replay].

## What the working composition does

The graph wrapper keeps consuming the graph while buffering results, so the
inspector can run. It releases only pending results covered by a completed
acceptance event from the **same subgraph namespace**, matching tool name,
result/error status, call ID and exact content hash. Child-agent acceptance
does not authorize a parent result. Changed content or missing acceptance
fails closed. No model-state inspection was moved or bypassed.

The replay experiment records outgoing ACP `session/update` notifications
after that gate. A new recording connection on the same live server receives
the recorded sequence without invoking the model or classifier again. Replay
does not append duplicate entries to the journal. Our existing ACP prototype
already has an in-memory update-history mechanism that can be retained; its
presence alone does not supply the missing live inspection gate.
[Existing history publication][journal], [existing history replay][fork-replay].

Filtering checkpoint history is also possible, but has subtleties: filtering
each snapshot independently resurrected an older approved payload after its
newer same-ID version was changed without approval. A guarded history view
passed the tested cases. Recording authorized viewer events is the narrower
candidate for live-process reconnect because it avoids reconstructing display
permission from model history.

## Executed evidence

All assertions passed:

- **13 live cases** against current Lightspeed upstream: pass, rejection,
  classifier technical failure, passing and mixed-result batches, content and
  namespace mismatch, cancellation while inspecting, nested agents, identical
  child/root result signatures, and child acceptance with parent rejection.
- **38 replay cases**, 19 each against the recorded runtime fork and current
  upstream: 12 checkpoint/history scenarios, two combined actual-live-gate and
  history-view scenarios, and five combined live-gate/journal scenarios per
  source. Journal replay matched authorized outgoing updates; rejected,
  failed-inspection, pending-cancelled and mixed-batch results remained absent.
  Recorded traces showed zero model/classifier calls during replay.
- **Two direct middleware cases** confirmed that a mixed-result batch can leave
  one classification cached while producing no completed acceptance callback
  and no model call. Content and status changes alter result signatures.

Models, classifier decisions, fixture tools and clients were scripted. The
Lightspeed graph preparation, middleware, redaction, classifier orchestration,
LangGraph execution/checkpoints and official ACP server were real. No paid
model, cluster, UI or real transport reconnection was used. Preserved live and
replay scripts were rerun against current upstream after relocation.

Pins: DeepAgents ACP `f2a6e02dc8e2dcb05885c5133b94102ccc79b794`
(`deepagents-acp` 0.0.13 source); Lightspeed upstream
`a23005e8f9ed0edc6e430072f50e4698590de71f`; runtime fork
`403916314aa4f869fc7733bef6e20fff32cf2680`; Python 3.12,
DeepAgents 0.7.22, LangChain 1.4.3, LangGraph 1.2.14, ACP SDK 0.10.1.

Preserved disposable probes:

- [Live graph output gate](probes/lightspeed_acp_output_gate.py)
- [Checkpoint and journal replay](probes/lightspeed_acp_output_replay.py)
- [Inspection-boundary invariants](probes/lightspeed_inspection_boundary.py)

The revised runtime fork's frozen `dev,acp` environment installs the exact
official ACP Git revision with DeepAgents 0.7.22. Its installed `server.py` was
verified byte-for-byte against the researched source. With that environment
and a checkout of the selected Lightspeed revision, run the probes from this
controller repository. The replay script imports the sibling gate. For example:

```sh
PYTHONPATH=/path/to/lightspeed-upstream/src \
  /path/to/runtime-fork/.venv/bin/python -B \
  docs/research/probes/lightspeed_acp_output_gate.py

PYTHONPATH=/path/to/lightspeed-upstream/src \
  /path/to/runtime-fork/.venv/bin/python -B \
  docs/research/probes/lightspeed_acp_output_replay.py
```

## Scope and remaining work

The journal proves **viewer-update replay**, not complete ACP conversation
reconstruction. It does not capture incoming prompts, steering, permission or
question RPC traffic, or pending ask state. Those may need separate records.
Appending before transport write records an authorized publication attempt,
not proof that a viewer received it. Snapshot/live ordering, reconnect races,
owner/viewer separation and concurrent viewers remain integration work.

The journal is in memory. Live-process viewer reconnect is the current need;
durable recovery after process loss remains follow-up work under
[ADR 0021](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md).
The experiments add no durability requirement.

The graph factory, stream shapes and private replay override need compatibility
tests and possibly a supported extension interface upstream. Subgraph display
policy and duplicate ACP call-ID presentation, buffering limits, arbitrary MCP
and transformed/chunked results, resource cleanup, active cancellation,
questions, usage and outcome mapping still need qualification. In particular,
the official cancellation flag did not interrupt a blocked inspection task;
the output gate does not fix active cancellation.

**This narrows the proposed integration:** retain Lightspeed preparation and
inspection, add a graph output gate and authorized viewer history there, and
reuse AgentServerACP. These two concerns alone do not justify maintaining a
separate ACP protocol implementation or requiring an upstream hook first.
No runtime, presentation, ADR, deployment or default was changed by this study.

[factory]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L222-L274
[proxy]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/code/deepagents_code/acp.py#L34-L109
[replay]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L818-L920
[live]: https://github.com/langchain-ai/deepagents/blob/f2a6e02dc8e2dcb05885c5133b94102ccc79b794/libs/acp/deepagents_acp/server.py#L1068-L1107
[inspection]: https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/tool_result_middleware.py#L232-L296
[events]: https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/providers/deepagents.py#L912-L1128
[journal]: https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/acp/server.py#L47-L92
[fork-replay]: https://github.com/djzager/lightspeed-agentic-sandbox/blob/403916314aa4f869fc7733bef6e20fff32cf2680/src/lightspeed_agentic/acp/server.py#L295-L308
