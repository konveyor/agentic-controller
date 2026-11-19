---
adr: "0024"
title: "Run Artifact Persistence: Recorded Transcripts and Logs"
description: "Defines who records an AgentRun's transcript and logs, the pluggable sink they are written to, and who owns reading and expiring them."
status: proposed
status_note: "Section 7's per-sink division of labour is the decision reviewers should scrutinise first; it is restated as open question 1."
date: "2026-11-18"
last_updated: null
authors:
  - "Dylan Murray"
last_reviewed: "2026-11-18"
implementation_status: deferred
review_note: "No implementation exists in this repository yet. The capability is proven in an external prototype built on a forked Hub and UI; this ADR decides the upstream shape before any of it lands."
relates_to:
  - "ADR 0002 (controller stays out of ACP; UI connects to pods for live streaming)"
  - "ADR 0006 (Hub addon pattern; Hub is the curated API and the authorization boundary)"
  - "ADR 0008 (harness owns the pod ACP port; the tee sees every run frame)"
  - "ADR 0011 (execution controls: mode, askUser)"
  - "ADR 0012 (verified client contract and layered transports)"
  - "ADR 0020 (run outcome model)"
  - "konveyor/agentic-controller#341"
provenance: "A working end-to-end prototype of transcript and log persistence, deployed on a live cluster against a forked Konveyor Hub and UI. The prototype hardcoded Hub as the only sink and put capture in Hub; this ADR keeps what it proved and moves the parts that cannot be upstreamed."
---

# ADR 0024: Run Artifact Persistence — Recorded Transcripts and Logs

## Context

An AgentRun leaves nothing behind. When the Sandbox goes away, so does
the ACP transcript — every message, tool call, plan update and permission
event — and so do the container logs. There is no way to audit what an
agent did, debug a failure after the fact, or review a long-running run
without having had a client attached for its entire duration.

### What the prototype established

A working prototype persists both streams and replays them in the UI. It
is deployed and demonstrated, and it settled the parts of the problem that
are genuinely hard:

- **The artifact contract.** One directory per run holding an append-only
  NDJSON transcript, plain container logs, and an atomically-written
  `meta.json` carrying the recording's state. Simple enough that any
  reader can consume it without a client library.
- **Gaps are explicit, not inferred.** When the recorder is detached
  mid-run, a gap marker is written into the transcript stream rather than
  the stream silently skipping. A recording is `complete` only when it has
  no gaps and no write failures; otherwise it is `incomplete`. The UI
  renders gaps as dividers, so a viewer can tell "nothing happened" from
  "nobody was watching."
- **Resumable reads.** Logs resume by byte offset, transcripts by line
  offset, both with a follow mode. This is what lets a browser survive an
  idle-timeout cut without re-fetching or losing its place, and it is the
  property that makes the rest of this design tolerant of restarts.
- **Replay fidelity.** A recorded transcript replayed through the same
  reducer that renders the live stream is indistinguishable from having
  watched live. Recording raw ACP frames, not a derived summary, is what
  buys this.
- **Runs that must not be recorded.** Runs in `approve` mode or with
  `askUser` set are gated out. Their semantics depend on a human being
  attached and answering; a recorder that participates in that exchange,
  or that changes its timing, changes the run.

### Why the prototype's shape cannot be upstreamed

In the prototype, Hub does the recording: it reconciles every AgentRun in
the namespace, dials each sandbox's ACP websocket as a silent second
client, and follows each pod's log through the Kubernetes API. That works,
and it is exactly the coupling this ADR exists to avoid.

- Hub must hold every run's ACP secret and pod-log RBAC, to do a job that
  has nothing to do with Hub's role as an inventory and analysis engine.
- The artifacts land in Hub's bucket, where only Hub can serve them. A
  Konveyor install is then unable to persist runs without Hub, and the
  data's lifetime is governed by Hub's bucket janitor rather than by the
  component that created it.
- The agentic platform would acquire a hard dependency on Hub for a
  capability that is not domain-specific.

### Three constraints the prototype did not have to face

**S3 has no append.** The prototype writes to Hub's bucket, which is a
filesystem on a PersistentVolume, where append-only files and byte-offset
resume are free. A single growing S3 object means re-PUTting the whole
thing on every flush — wasteful, and racy under concurrent readers — while
buffering until the run ends sacrifices both liveness and crash-safety,
which are the two reasons this feature exists.

**The recorder has no Kubernetes client.** Moving capture into the harness
(§1) puts the only component that knows the recording's state in a process
with no API access: `harness/go.mod` depends on neither `client-go` nor
controller-runtime, deliberately. Whatever reaches `status.recording` has
to travel a channel that already exists.

**The harness's Hub token dies with the run.** It is minted per run and
self-revoked on exit (ADR 0006; `harness/cmd/migration-harness/main.go`).
Any design that expects a component to touch the Hub bucket *after* the
run ends needs a different credential, and the controller holding one
would contradict principle 1 — see §7.

### What the controller is, and is not

Three settled decisions bear directly on where recording can live:

- The controller is a stateless reconciler that does not connect to agent
  pods over ACP and holds no in-memory streaming state (ADR 0002).
- The harness owns the pod's ACP port and tees the run's frames to every
  attached viewer (ADR 0008). Every `session/update` on the run session
  already passes through `harness/internal/tee`, which also keeps a small
  replay ring for late-attaching viewers.
- Hub is the curated API and the authorization boundary for agent
  resources (ADR 0006). The controller is domain-agnostic: it does not
  call Hub or any inventory system, it does not know what an application
  is, and authorization questions scoped to one are explicitly not its
  concern (AGENTS.md, principle 1).

## Decision

### 1. The harness records

The recorder is a subscriber on the harness tee. Every frame it needs
already flows through that process, so recording adds no connection, no
second ACP client, and nothing new on the wire. The harness already lives
exactly as long as the run, already holds the run's credentials, and is
already the only component inside the pod's trust boundary.

This keeps per-run streaming state out of the controller, where ADR 0002
says it does not belong, and it dissolves the questions that
controller-side capture would raise — no ACP secret outside the pod, no
leader-election or sharding scheme for per-run capture goroutines, and no
capacity coupling between the number of concurrent runs and one process's
socket budget.

The harness records three streams:

- **The transcript**, from the tee. This includes the synthetic
  `session/update` frames the harness emits for work the agent runtime
  cannot see — workspace preparation, pushes, stage outcome (ADR 0008).
  Those frames are part of the run's narrative and are recorded like any
  other.
- **Its own container log**, teed to the sink as it is written to stdout.
- **The `skill-loader` init container's log.** The init container runs and
  exits before the harness starts, so the harness cannot observe it live.
  Instead `skill-loader` tees its own output to a shared `emptyDir`, and
  the harness uploads the completed file at startup. This keeps
  init-container capture on the same path as everything else — no
  Kubernetes log API, no `pods/log` RBAC, and identical behaviour on every
  sink.

**Recording must never fail a run.** An unreachable sink degrades the run
to an `incomplete` recording; it does not degrade the run.

### 2. The controller backstops runs that die without finalizing

A harness that is OOM-killed, evicted, or hard-terminated loses whatever
it had not flushed and never writes its final state. When a run reaches a
terminal phase without a `complete` recording, the controller marks the
recording `incomplete` and records a gap covering the unrecorded interval.

Where the controller can also *write* to the sink (§7), it additionally
makes one best-effort salvage pass against the pod log through the
Kubernetes API while the Sandbox still exists, appending what the harness
missed. Where it cannot, the status is marked and the tail is lost.

Salvage is best-effort by construction. A recording that cannot be
completed is reported as `incomplete`, never silently presented as whole.

### 3. The gate is enforced before the recorder exists

A run with `mode: approve` or `execution.askUser: true` gets no recorder
at all — not a recorder that attaches and discards. The decision is made
when the tee is wired, so there is no code path on which recording can
affect the timing or the outcome of a human-in-the-loop exchange.

The run's recording state is `skipped` with a machine-readable reason.
"Not recorded" is always distinguishable from "recorded and empty."

### 4. Recording is on by default, with a per-run opt-out

Recording is active when a sink is configured and the run is not gated.
An install that configures no sink records nothing and behaves exactly as
Konveyor does today — the feature is invisible until an administrator
turns it on, and no cluster acquires a durable store of agent output by
upgrading.

Once a sink is configured, runs record by default. Audit coverage that
depends on remembering to ask for it is not audit coverage. A run that
should not be recorded sets `spec.recording.enabled: false`, which lands
in status as `skipped` with an explicit reason, exactly like the gate.
`spec.recording` is also settable on AgentWorkflowRun, where the
controller propagates it to every stage AgentRun it creates.

The sink itself is configured on the controller — a flag naming the sink
type and a reference to a Secret holding its endpoint and credentials —
and is cluster-wide. The controller projects what the harness needs into
the Sandbox spec, the same way it already delivers other runtime
configuration. Per-namespace sinks are plausible and deliberately not in
this decision; adding them later means changing where the configuration is
read from, not how recording works.

### 5. The sink is an append-chunk interface, not a filesystem

The sink abstraction is deliberately narrower than a file API. It appends
chunks to named streams and finalizes them:

```go
// Sink is the pluggable persistence boundary. Implementations are
// responsible for durability; callers are responsible for ordering.
type Sink interface {
    // Open returns a writer for one named stream of one run. Calling
    // Open again for a stream that already has content resumes it.
    Open(ctx context.Context, run RunRef, stream string) (StreamWriter, error)
    // Finalize marks every stream of a run closed and may compact.
    Finalize(ctx context.Context, run RunRef, meta Meta) error
    // Delete removes every artifact belonging to a run.
    Delete(ctx context.Context, run RunRef) error
}

type StreamWriter interface {
    // Append durably adds one chunk and returns the logical offset
    // immediately after it: bytes for logs, lines for the transcript.
    Append(ctx context.Context, chunk []byte) (offset int64, err error)
    Close() error
}
```

The two implementations differ entirely below that line:

- **Hub.** Appends to one file per stream under the Hub bucket, which is
  what the prototype does and what the filesystem makes natural.
- **S3-compatible.** Materializes each flush as its own key under a
  zero-padded sequence (`.../transcript/000001.jsonl`), and compacts the
  sequence into a single object at `Finalize`. Readers concatenate in
  sequence order.

The logical contract a reader sees is identical on both: NDJSON transcript
lines, plain log bytes, resumable by offset. The physical layout is the
sink's business. This is the one place the prototype's design had to
change, and confining the change to the sink implementation is the point
of the abstraction.

**Hub is the primary integration.** It is the sink Konveyor deployments
are expected to use and the one whose behaviour is normative when the two
differ. S3 exists so that the capability does not require Hub, and so that
the interface stays honest — an abstraction with one implementation is not
an abstraction.

The harness learns its own identity for `RunRef` through the downward API;
no such environment variable exists today.

### 6. The artifact contract

```
<prefix>/agentruns/<namespace>/<run-name>-<uid>/
├── meta.json          # recording state, gaps, timestamps, schema version
├── transcript.jsonl   # one JSON object per line
├── agent.log          # the harness container's log
└── skill-loader.log   # init container log, relayed by the harness
```

Including the run's UID in the directory name keeps a recreated run with a
recycled name from colliding with its predecessor's artifacts, and keeps
artifacts identifiable after the CR is gone (§7).

Transcript lines are one of two shapes:

```json
{"t":"2026-11-18T14:03:22.481Z","frame":{ /* raw ACP session/update */ }}
{"gap":{"from":"2026-11-18T14:05:01.002Z","to":"2026-11-18T14:05:44.119Z"}}
```

Frames are recorded raw and stamped with receive time. Nothing is
summarized, reshaped, or interpreted on the write path — that is what
makes replay faithful and what keeps the recorder from needing to
understand ACP beyond "this is a notification on the run's session."

`meta.json` is written atomically and is the bucket's self-describing
copy of what `status.recording` reports. The two deliberately carry the
same facts: the bucket must be interpretable without the cluster, and the
cluster must be queryable without the bucket. Because recordings outlive
their CRs (§8), `meta.json` is the authority once the CR is gone.

### 7. Who may touch the sink, and who serves reads

The controller publishes one normative read contract:

```
GET /agentruns/{namespace}/{name}/transcript?offset=<line>&follow=
GET /agentruns/{namespace}/{name}/logs?container=<name>&offset=<byte>&follow=
GET /agentruns/{namespace}/{name}/recording
```

Responses carry `X-Log-Source: live|saved`, `X-Transcript-Status`, and
`X-Transcript-Skip-Reason`. The UI always talks to Hub, which
authenticates the user and makes the application-scoped authorization
decision it already makes for every other route under `/hub/agentic/`.

**Who implements that contract depends on the sink**, because the
controller cannot reach the Hub bucket. The harness's Hub token is
per-run and self-revoked on exit, so any post-run Hub access would require
the controller to hold a standing Hub credential — which principle 1
forbids and ADR 0006 rejected. Rather than amend that principle, the work
divides:

- **S3 sink.** The controller serves the contract from an in-process
  listener alongside the metrics and health servers the manager already
  runs, protected by the same
  `filters.WithAuthenticationAndAuthorization` the metrics endpoint uses,
  and reachable in-cluster only — no Route, no Ingress, no browser-facing
  surface. Hub proxies to it. Hub needs no S3 credentials and no knowledge
  of the key layout.
- **Hub sink.** Hub already holds the bytes; routing them out to the
  controller and back would be absurd. Hub serves the same contract
  directly from its own bucket, and reaps it, following the layout in §6
  and the expiry the controller stamps on the CR (§8), which Hub already
  has RBAC to read (`config/hub-rbac/`).

The controller therefore never holds a Hub credential, and Hub never holds
an S3 credential. What each component can reach is what it already had.

**This is the decision most in need of review** — see open question 1. It
buys principle 1 at the price of issue #341's criterion that the
controller, and only the controller, deletes the data: for the Hub sink,
the controller defines retention and Hub enforces it. It also means the §2
salvage pass works only for the S3 sink; a hard-killed harness on the Hub
sink yields an `incomplete` recording with no salvaged tail.

**In-process, for now.** A separate artifact-server Deployment would buy
independent scaling and restart isolation at the cost of another
deployment, RBAC binding, and bundle entry. At `replicas: 1`, with Hub as
the only client and a read contract resumable by offset, that is paying
operational cost for a load problem that does not exist yet. The contract
is transport-identical either way, so carving the server out later is a
deployment change, not a redesign.

**Live reads.** While the pod exists the live container log is preferred
and the saved copy is tailed once it is gone. The two are *not* byte-
identical — redaction (§9) applies to the recorded copy, and the
Kubernetes log API has no byte-offset parameter — so the offset space is
per-source and `X-Log-Source` tells a client which one it is reading. A
client crossing that boundary re-arms at the new source's offset, which
the prototype's stream client already does on every reconnect.

A live transcript follow is served by polling the sink as the harness
appends, since the controller may not open ACP (ADR 0002). Follow latency
is therefore bounded by flush cadence, not by frame arrival. Clients
wanting true real-time attach to the pod's ACP endpoint as they do today;
the read API is for history and for runs nobody is watching.

### 8. Recording state, expiry, and deletion

**Status.** The controller maintains `AgentRun.status.recording`:

```yaml
status:
  recording:
    state: recording | complete | incomplete | skipped
    skipReason: approveMode | askUser | disabled | noSink
    gaps:
      - from: "2026-11-18T14:05:01Z"
        to: "2026-11-18T14:05:44Z"
    location:
      sink: hub
      prefix: agentruns/konveyor-tackle/cve-remediator-abc123-7f3a.../
    expiresAt: "2026-12-18T14:09:55Z"
```

The controller derives this without reading the sink. `recording` follows
from the run being active with recording enabled; `skipped` from the gate
or the opt-out; `complete`, along with the harness's gap list, arrives on
the channel the harness already uses — the termination message the
controller copies to `status.terminationData` (CONTEXT.md). A terminal run
whose termination message never arrived, or does not report `complete`, is
`incomplete` by §2. The harness needs no Kubernetes client.

**Recordings outlive their AgentRun.** The CR is an index, not the record.
Run GC already exists (`spec.ttlSecondsAfterFinished`, `--agentrun-ttl`),
and tying artifacts to CR lifetime would mean any install that garbage-
collects runs after a few hours destroys the audit trail with them — the
exact failure this feature exists to prevent.

So artifacts are reaped on their own clock. Recording TTL defaults to 30
days from completion and is overridable per run by
`spec.recording.ttlSeconds`, mirroring the existing
`spec.ttlSecondsAfterFinished` rather than inventing an annotation. The
AgentRun finalizer does **not** delete artifacts; deleting a run removes
its index, not its history. The reaper is keyed on expiry and walks the
sink's prefixes, so it works equally on artifacts whose CR is long gone —
which is why §6 puts the run's identity in the prefix and the expiry in
`meta.json` as well as on the CR.

Deletion authority belongs to whoever serves the sink (§7): the controller
for S3, Hub for its own bucket. No backend lifecycle rule and no unrelated
janitor may remove these objects. For the Hub sink specifically, Hub's
existing bucket reaper must treat the `agentruns/` tree as not its own.
Expressing that as an explicit, documented prefix contract — rather than
the prototype's accident of writing somewhere Hub happens not to sweep —
is a condition of accepting this ADR.

### 9. Secrets

The harness holds a known-secret set — git credentials, the Hub token,
Maven settings, proxy credentials — and today redacts those values at
roughly twenty specific call sites in its own output
(`harness/cmd/migration-harness/redact.go`;
`harness/internal/redaction` holds only shared policy constants). There
is no streaming redactor over stdout and none over ACP frames.

Both are new work this ADR requires: the redactor is promoted out of
`main` into a reusable package and applied as a filter over the recorded
container log and over every recorded frame. This is the single largest
piece of harness work in the design, and it is on the hot path of every
recorded run, so it must be allocation-cheap and must never block the tee.

This is credential redaction, not data-loss prevention. General content
scanning of agent output is a non-goal: a transcript is a faithful record
of a run, and a run that reads a secret out of a file has put that secret
in its output. Installs for which that is unacceptable are the reason
`spec.recording.enabled: false` and sink-less operation exist.

**The controller's salvage pass is unredactable by construction.** It
reads raw pod logs through the Kubernetes API and has no access to the
run's secret set, which is assembled inside the pod from its own
environment. The alternative — giving the controller the run's
credentials — is worse, and contradicts principle 3. Salvaged tails are
therefore marked as such in the gap record, and an install that cannot
accept unredacted salvage should disable the backstop rather than the
redactor.

Sink credentials follow the rule push credentials already follow
(principle 3): they live in the harness's environment, never the agent's.
Where the backend supports prefix-scoped credentials they should be scoped
to the run, so that a compromised run cannot reach another run's
artifacts. The Hub sink gets this for free — the harness's Hub token is
already scoped and already minted per run — which is a further reason it
is the primary integration.

## Approaches considered

**Controller-side capture (the shape issue #341 proposed).** The
controller dials each sandbox's ACP websocket and follows each pod's log,
porting the prototype's recorder from Hub to the controller. Its one real
advantage is that the recorder is not in the pod, so it survives a pod
that dies badly. Rejected because it makes the controller stateful in
exactly the way ADR 0002 and principle 4 rule out — a websocket and a
log-follow goroutine per concurrent run — and because everything
downstream of that is cost: leader election or sharding for capture
ownership, ACP secrets in the controller, and read capacity coupled to
reconcile capacity. The advantage is recovered more cheaply by §2, which
asks the controller to act only on runs that actually failed to record.

**Harness-side capture with no backstop.** Simplest, and wrong at the
edges: it loses the tail of any run whose harness dies hard, which
includes the OOM and eviction cases most worth having a record of.

**A recorder sidecar.** A second container would isolate recording from
the harness's lifecycle. Agent Sandbox is single-container by design
(`CONTEXT.md`), so this is not available without diverging from the
sandbox model.

**A standing Hub credential for the controller.** Would let the controller
own write, read, reap and delete uniformly across both sinks, honouring
issue #341's "only the controller deletes" criterion. Rejected because it
makes the controller a Hub client, which principle 1 forbids and ADR 0006
rejected on its own merits; adopting it means amending both.

**Mounting Hub's bucket PVC into the controller and the sandboxes.** Would
keep uniform controller ownership without any Hub API call, leaving
principle 1 untouched. Rejected because it requires an RWX volume mounted
into every agent pod — a shared writable surface across runs and a
significant deployment constraint — to avoid a coupling that §7 avoids
more cheaply.

**A separate artifact-server deployment.** Deferred, not rejected — §7.

## Consequences

**The controller gains a read listener and a reaper.** Both are new
surfaces in a component that had neither. This is the first time the
controller serves data rather than reconciling state, and that is a real
widening of its role, bounded deliberately: authenticated, in-cluster
only, no authorization decisions, no browser exposure.

**The controller gains `pods/log` RBAC**, which it does not have today,
for the salvage pass alone. Avoiding this permission was an argument
against controller-side capture; §1's `emptyDir` relay removes the
init-container need for it, but salvage still requires it.

**The harness gains a sink dependency and a streaming redactor.** The
redactor is new work on the hot path of every recorded run (§9), and the
sink must degrade to an `incomplete` recording rather than a failed run.

**Hub gains real work for the primary sink**: an append-capable bucket
API for the harness, the read contract in §7, the reserved `agentruns/`
prefix, and expiry enforcement. None of it exists today, and none of it
lives in this repository. The S3 sink is what is buildable and testable
here end to end; the Hub sink is a cross-repository dependency. Sequencing
should reflect that, even though Hub is the primary integration.

**Agent output becomes durable, and outlives the run.** This is the point,
and it is also the risk. Source code, tool output, and whatever the agent
read along the way persist for a month by default, past the deletion of
the CR, readable by anyone Hub authorizes. The gate, the opt-out, the TTL,
and sink-less operation are the controls; none is a substitute for an
administrator deciding this cluster should keep this data.

**The UI work is not in this ADR.** Porting the prototype's run panel —
Transcript/Logs/Details tabs, per-stage panels on AgentWorkflowRun, live
tail with follow pinning, replay through the shared reducer, gap dividers,
saved-versus-live badges — is tracked by issue #341 and depends only on
the read contract in §7.

## Open questions

1. **Is the per-sink division of labour in §7 right?** This is the
   decision the author is least sure of and the one reviewers should
   weigh first. Letting Hub serve and reap its own bucket keeps the
   controller free of a Hub credential and keeps principle 1 intact, but
   it gives up issue #341's "the controller, and only the controller,
   deletes the data," splits the read implementation across two
   codebases, and leaves the salvage pass working only on the
   non-primary sink. The two rejected alternatives — a standing Hub
   credential, or a shared bucket PVC — are written up under Approaches
   with what each costs. If reviewers judge uniform controller ownership
   worth amending principle 1 for, that is a coherent position and this
   section is where it should be argued.
2. **Flush cadence and chunk size.** The prototype fsyncs roughly once a
   second. For the S3 sink that cadence sets the object count, and an hour
   of a chatty run is 3,600 keys before compaction. Time-or-size,
   whichever comes first, is the likely default, but it has not been
   measured — and it also sets live-follow latency (§7).
3. **Compaction failure.** If `Finalize` compacts and compaction fails, a
   reader must still be able to read the sequence. Whether compaction is
   best-effort with the sequence retained as the source of truth, or
   atomic behind a marker object, is undecided.
4. **Quota and backpressure.** Nothing yet says what happens when the
   bucket fills. Metrics and an alert are the minimum; whether the
   controller should refuse to admit new runs, or let recordings degrade
   to `incomplete`, is a policy question that touches run admission.
5. **Orphan reaping across sinks.** The reaper walks prefixes rather than
   CRs (§8), which means listing a bucket that may hold a great many
   expired runs. Whether that listing is cheap enough at scale, or wants
   an index object, is unmeasured.
6. **Workflow-level aggregation.** Each stage AgentRun records
   independently. Whether `AgentWorkflowRun.status` should index its
   stages' recordings, or the UI should assemble that from the stage runs
   it already lists, is unresolved.
7. **Schema evolution.** `meta.json` carries a schema version; the policy
   for readers encountering a newer one is not yet written.
