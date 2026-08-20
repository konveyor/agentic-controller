# ADR 0017: In-turn human questions via the ask_user tool and ACP elicitation

- **Status:** proposed
- **Date:** 2026-08-20
- **Relates to:** ADR 0002 (ACP transport and observability), ADR 0008
  (harness owns the pod ACP port), issues #55/#56 (interactive input and
  approval flow), PR #96 (ACP tee: watch and steer), PR #161
  (implementation of this ADR)

## Context

The tee (PR #96) made a run watchable and steerable, but nothing lets the
agent **wait** on a human. A question the model writes in prose is only
seen when the run is over, and a turn that ends on a question is a
finished run — the controller reports Succeeded while the question was
never answered. Steer is the inverse shape: a nudge *into* a running
turn; it cannot make the turn block. Both failure modes were observed
live: an execute-stage run that found no `PLAN.md` asked "Would you like
me to 1/2/3?" in prose, then answered itself and wandered into the plan
stage; a "pause for a status report" steer was read and then the agent
carried on.

Issue #55's open task — how does interactive input reach the agent — has
two halves with different lifetimes:

| | Stage-gate | In-turn |
|---|---|---|
| Example | approve SPEC.md | "which database do you want?" |
| Granularity | stage boundary | mid-turn |
| Lifetime | durable, resumable | ephemeral, dies with the turn |
| Answerable by | someone not watching right now | only someone watching |

This ADR records the **in-turn** half: a real "stop and confirm" where
the turn parks until a human answers. The stage-gate half (durable
`.konveyor/` file contract) is a separate mechanism and deliberately out
of scope here.

Constraints that shaped the decision:

- Runs are headless by default. The mechanism must degrade to headless
  without hanging the turn and without inventing an answer.
- The harness speaks ACP to the agent runtime (ADR 0002). goose ≥ 1.45
  relays MCP elicitation over ACP, gated on the client advertising
  `clientCapabilities.elicitation.form`.
- The tee already owns exactly one forwarding surface for agent→human
  asks (`session/request_permission`), including fail-closed timeout
  policy and late-viewer replay.

## Decision

### 1. Asking is a tool call, not a protocol extension or prompt convention

The agent asks by calling one tool: `ask_user(question, options?)`.
Models reliably invoke tools mid-turn, and the tool result re-grounds
the model with the authoritative outcome — "The human answered: …", the
human declined, or nobody answered — never an invented answer. Whether
the agent *can* ask is per-session policy: `HARNESS_HITL_ASK=off` simply
leaves the tool out of the session (default on).

### 2. The harness binary is itself the MCP server

`internal/askuser` is a minimal stdio MCP server exposed as a hidden
subcommand (`migration-harness ask-user-mcp`); `session/new` lists the
harness's own binary under `mcpServers`. No new image dependency, and
the tool is version-locked to the harness that mounts it.

### 3. MCP elicitation is the carrier

An `ask_user` call becomes an MCP `elicitation/create` with a flat form
schema — required string `answer`, enum when options are given. This is
the protocol's own primitive for soliciting structured human input, and
it is what makes the design portable across agent runtimes: the harness
side speaks only ACP. What a runtime must provide to compose with this
mechanism (the acceptance checklist for future runtime support):

1. Mount stdio MCP servers passed in `session/new`.
2. Relay MCP elicitation to the ACP client, gated on the client's
   `elicitation.form` capability.
3. Keep the tool call — and therefore the turn — blocked until the
   elicitation resolves.

goose ≥ 1.45 does all three. A runtime that does not relay elicitation
degrades safely: the tool reports that nobody answered.

### 4. Questions ride the permission-ask path, under a distinct id namespace

The tee forwards `elicitation/create` to viewers exactly like a
permission ask — first answer wins, answers intercepted off the viewer
pipe — but under `kask-<n>` ids (permission asks keep `kperm-<n>`). One
forwarding, timeout, and replay surface; two namespaces so UIs can
render authorization and information asks differently. A viewer
attaching mid-question is replayed the pending ask: questions wait
minutes, and the harness status ring (full-state snapshots) would not
carry them.

### 5. Every unanswered path fails closed, and nobody answers for the human

No viewer, no tee, or no answer within `HARNESS_HITL_TIMEOUT_SECONDS` →
`{action: "cancel"}`, and the model is told no human answered. This is
the same policy already settled for permission asks: an ask that
self-approves on a timer is no ask at all. Answers are relayed verbatim;
the harness never composes one.

### 6. The prompt guideline ships with the tool

Only when the tool is mounted, the system prompt instructs: call
`ask_user` for decisions only a human can make — never ask in prose —
and if a mid-turn redirect asks to pause, check in, report status, or
wait, call `ask_user` with a status summary and choices and do not
continue until it answers. The second clause is what makes steer and the
blocking question compose instead of compete.

### 7. Answering a question is solicited input

Authorization-wise, answering an `ask_user` question is the same act as
answering a permission ask: the agent solicited it. Unsolicited input
(steer) stays separately gated (`HARNESS_HITL_STEER`). Which humans may
write on the stream at all is the Hub's authorization question
(tackle2-hub#1116) and is out of scope here.

## Alternatives considered

- **Prose questions plus steer (status quo).** The question is invisible
  until the run is over and the turn cannot block; observed to
  self-answer. Rejected as not being HITL at all.
- **Reusing `session/request_permission` for questions.** Its outcome
  vocabulary is authorization (selected option / cancelled), not
  information: no free-text answer, and UIs could not distinguish "may
  I?" from "which one?". Elicitation carries a schema; permission
  carries options. Rejected.
- **A goose-specific channel (custom notifications, or answering by
  steering the answer in as a user message).** Not portable across
  runtimes, and a steered-in answer does not block the turn — the model
  can proceed before the human types. Rejected.
- **Stage-gate file contract for questions.** Durable and answerable by
  someone who is not watching, but stage-granularity: it cannot park a
  single tool call. Complementary mechanism, not a substitute (see the
  table above).

## Consequences

- The turn genuinely blocks on the human. Proven live
  (`TestAskUserBlocksLiveRun`, goose 1.45 + Bedrock): the question
  reached the viewer as `kask-1` with the enum schema, the run stayed
  parked until "postgres" was chosen, and the final answer named it.
- Headless and unattended runs are unchanged in character: the tool
  degrades to "nobody answered" instead of hanging or auto-approving.
- Viewers receive a form-renderable schema, so consoles can show a real
  question card rather than parsing prose.
- Known follow-ups, deliberately not blocking: other viewers' cards do
  not learn when someone else answered (the tee could broadcast a
  resolution frame); questions share the permission timeout and probably
  want their own, longer default.
- Adding an agent runtime now has a concrete acceptance checklist
  (Decision 3) instead of a rediscovery exercise.
