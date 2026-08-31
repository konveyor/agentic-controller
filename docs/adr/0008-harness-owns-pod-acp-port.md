---
adr: "0008"
title: "Harness Owns the Pod ACP Port (Tee Topology)"
description: "Defines the harness tee as the pod-facing ACP endpoint that fans out the run stream and relays human interaction."
status: proposed
status_note: "Revises the pod-layout and human-in-the-loop sections of ADR 0002; ADR 0002's decisions otherwise stand."
date: "2026-08-04"
last_updated: null
authors:
  - "Ian Bolton"
last_reviewed: "2026-08-31"
implementation_status: in-sync
review_note: "The tee topology is implemented and enabled by default in the entry point. Its status remains proposed pending the project's formal acceptance process."
---

# ADR 0008: Harness Owns the Pod ACP Port (Tee Topology)

## Context

ADR 0002 settled three things: the controller stays out of ACP, the UI
connects to agent pods directly for streaming, and the pod runs
`goose serve --port 4000` as its ACP endpoint. The first two hold. The
third turned out to make the second unachievable.

Verified against the pinned goose fork (`aaif-goose/goose` v1.39.0):
every WebSocket upgrade calls `registry.create_connection()`, which
constructs a **fresh `GooseAcpAgent` per connection**. The outbound
streams (`connection_stream`, `session_streams`, `all_outbound`) are
fields of that per-connection struct, and nothing fans out across
connections. So when a viewer dials `pod:4000/acp` it lands on a
different agent instance than the harness's run connection: the run's
session is not merely unsubscribed, it is **structurally unobservable**.

Two consequences of ADR 0002 were therefore never reachable as written:

- *"UI connects to agent pods directly for streaming"* — the UI could
  connect, but never see the run. Its `session/new` created a private
  session on a private agent, a parallel universe to the run.
- *"Human-in-the-loop via ACP"* — 0002 describes goose sending
  `request_permission` to the connected UI. It cannot: the ask travels
  down the run connection, whose only client is the harness. With no
  relay the harness's only options are to answer or to hang the turn
  forever (goose parks on the reply with no timeout of its own).

Adding notifications to the existing topology cannot fix this. The ACP
schema marks `SessionNotification` `x-side: client`, and the harness is
the client on that link; there is no spec-legal way for it to emit
progress on the connection it consumes.

## Decision

**The harness becomes the pod's ACP endpoint on `:4000`; goose serves
loopback-only on `127.0.0.1:4001` behind it.**

```text
before:  viewer ──(hub WS proxy)──▶ pod:4000 = goose   ◀── harness (ACP client)
after:   viewer ──(hub WS proxy)──▶ pod:4000 = harness ──▶ 127.0.0.1:4001 = goose
                                                 ▲
                                   harness's own run connection
```

### Verbatim pipe per viewer

Each attached client gets its own upstream connection to goose and its
frames are piped byte-for-byte in both directions. A client driving its
own session behaves exactly as when goose owned the port — every present
and future goose capability passes through untouched.

### The run's stream is teed

The run connection's inbound `session/update` notifications — and
goose's `_goose/unstable/session/update` channel (`usage_update`,
`status_message`), enabled by declaring the `customNotifications` client
capability — are copied to every attached viewer **unmodified**, keeping
the run's real `sessionId`. They are notifications, so they cannot
collide with a viewer's own request/response pairs, and an unmodified
ACP client renders the live run.

### The harness reports its own lifecycle in the same vocabulary

Work the goose stream cannot see — workspace preparation, watcher and
final git pushes, stage outcome — is emitted as synthetic
`session/update` frames on the run's session using standard ACP kinds
(`plan`, `tool_call`, `tool_call_update`) plus goose's `status_message`.
A small replay ring catches late-attaching viewers up. No third
vocabulary is invented, and durable stage status stays in
`.konveyor/result.json` per the issue-22 contract.

### Permission asks are relayed; unattended runs fail closed

An ask on the run connection is offered to attached viewers under a
harness-allocated `kperm-<n>` **string** id, disjoint from the pipe's
verbatim numeric ids. First answer wins and is relayed verbatim. Every
other path denies: nobody attached, no answer within
`HARNESS_HITL_TIMEOUT_SECONDS`, or a viewer already shown to be
unresponsive. This replaces 0002's reliance on `GOOSE_MODE=auto` to
avoid asks — an ask that self-approves on a timer is not an ask.

### Viewers may redirect the run

goose scopes an active run to the connection that started it, so a
viewer frame naming the run session is routed onto the **run**
connection rather than the viewer's private pipe:
`_goose/unstable/session/steer` injects operator guidance into the live
turn, and `session/cancel` stops it (a cancelled stop reason fails the
stage — a human abort is not a success). A viewer `session/prompt`
against the run session is refused while the run is active, since two
connections prompting one session interleave its history.
`HARNESS_HITL_STEER=off` makes the stream watch-only.

### The tee can never fail the run

Bounded per-viewer queues (a slow viewer is dropped, it can reconnect),
per-connection panic recovery, keepalive so half-open viewers release
their goose connection, and a listener failure downgraded to a warning.
`HARNESS_ACP_TEE=off` restores goose owning `:4000` directly.

## What ADR 0002 keeps

This is a change of implementation, not of decision:

- **Controller stays out of ACP** — unchanged.
- **UI connects to agent pods directly** — unchanged, and now actually
  delivers the run. Precisely: the *endpoint* contract is unchanged —
  same `pod:4000/acp` path, same `X-Secret-Key` / `?token=` carriers,
  same headless Service DNS — so nothing outside the pod is reconfigured
  and an existing client still connects and drives its own session as
  before. What a client *observes and may do* is deliberately extended,
  not preserved: it additionally receives the run session's teed frames
  and the harness's synthetic lifecycle updates, it may be offered
  `kperm-*` permission asks, and it may steer or cancel the run session
  (while a `session/prompt` against an active run session is now
  refused). Those extensions are the point of this ADR; the compatibility
  claim is about the endpoint, not about the frames on it.
- **Users without the UI** — `kubectl port-forward <pod> 4000:4000`
  still reaches an ACP endpoint, now the tee.
- **The harness bridge for non-ACP runtimes** (0002's multi-runtime
  section) is *extended* by this, not superseded: the harness already
  fronting `:4000` is exactly the seam that future bridge needs.

## What this revises in ADR 0002

- **Pod architecture** — goose no longer binds `:4000`; it binds
  `127.0.0.1:4001` and the harness owns the pod's ACP surface. The pod's
  ACP behaviour is therefore versioned by the harness image, not by the
  goose image.
- **Human-in-the-loop via ACP** — asks do not reach a UI directly; the
  tee relays them, and an unattended run fails closed instead of
  suppressing asks with `GOOSE_MODE=auto`.

## Alternatives Considered

### Fan-out in the Hub (issue-22 R2)

Placing multi-viewer fan-out in the Hub was the agreed contract before
this. It does not solve the problem: whatever the Hub connects to still
lands on a private goose agent that cannot see the run. Pod-side teeing
arguably shrinks R2 to a dumb pipe with a credential swap, which is a
gift rather than a loss — but the placement is the Hub maintainer's
call, and this ADR should not be read as taking it unilaterally.

### Patch goose for cross-connection fan-out

Correct in principle, and the right long-term home. Rejected for now: it
forks the pinned image, moves at upstream's pace, and every deployment
would need the patched build. The tee needs no goose change at all.

### An ACP multiplexer service

ADR 0002 already considered and rejected a separate multiplexer service.
The tee is not that: it is in-pod, per-run, and dies with the run — no
new deployment, no new credential boundary, no cross-run state.

### Observer sessions / a second harness ACP session

Would require the harness to reimplement the agent role and re-emit a
synthesized transcript, inventing protocol and drifting from goose's
real stream. The tee copies frames it does not interpret.

## Consequences

**Positive.** The run is observable while it executes, by any ACP
client, with no client changes. In-turn HITL becomes possible at all —
both permission asks and, more usefully, mid-turn redirection. The pod's
external ACP contract is unchanged, so the UI, the Hub proxy and
`port-forward` all keep working.

**Negative.** The harness now runs a network-facing listener inside the
run pod: more code on the critical path and one more failure surface.
Mitigated by construction — the tee cannot fail the run, and a kill
switch restores the old topology. Anything depending on goose itself
answering on `:4000` (nothing known outside the pod) would break.

**Operational.** The pod's ACP surface now ships with the harness image,
so an ACP-level fix no longer waits on a goose bump — and conversely, a
harness rollback changes the pod's ACP behaviour.
