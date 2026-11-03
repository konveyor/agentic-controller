---
adr: "0018"
title: "Execution Fields on AgentRun, and a `Succeeded` Terminal Condition"
description: "Defines execution-field placement, workflow-stage stamping, and the Succeeded terminal condition for AgentRuns."
status: proposed
status_note: "Amends ADRs 0011 and 0009 and supersedes their conflicting field-placement, exit-code, and guide-substitution details."
date: "2026-08-19"
last_updated: "2026-11-02"
authors:
  - "David Zager"
last_reviewed: "2026-11-02"
implementation_status: in-sync
review_note: "Execution fields on AgentRun, workflow-stage stamping, Succeeded terminal condition, guide scoping, the Refused outcome (exit 3), and the Verified condition (#302) are implemented. The ADR remains proposed pending formal acceptance."
---

# ADR 0018: Execution Fields on AgentRun, and a `Succeeded` Terminal Condition

**Update (2026-08-31):** The controller now implements the execution-field
placement, workflow-stage stamping, `Succeeded` terminal condition, phase
mirror, and workflow-guide scope described here. The ADR remains proposed
pending formal acceptance; its implementation status is tracked in the ADR
reconciliation index.

**Update (2026-09-24):** The first of the "future outcomes" is implemented
(#241): harness exit 3 → phase `Failed` + `Succeeded{False, Refused}`. The
harness reads the stage's own verdict — the `- Status:` line of the last
section of `.konveyor/handoff.md`, when the stage wrote it — after the
turn; `failed` is a refusal. The reason rides `terminationData.stopReason`,
as the limit kind does for exit 2. `NoChanges` remains future (#129).

**Update (2026-11-02):** AgentRun gains a third orthogonal condition,
`Verified` (#302), and with it a narrow, named exception to
`terminationData` opacity. This amends two clauses below: "An AgentRun
then carries exactly two orthogonal condition facts" (now three), and
"terminationData … uninterpreted" (now: uninterpreted but for the
`verification` verdict and the commit it describes). See *The `Verified`
condition* below. Neither the exit-code contract nor the `Succeeded`
reason set changes, and verification does not affect phase or workflow
sequencing.

## Context

Implementing ADRs 0009/0011 (#115, #116, #119) surfaced three places
where the merged text does not survive contact with the controller's
reality.

**1. Stage limits have nowhere to land.** ADR 0011's field-placement
table puts `maxTurns`/`maxCost` on the Agent (default) and on
AgentWorkflow stages (override), but **not** on AgentRun. A standalone
run is fine — the AgentRun controller reads `Agent` defaults. But a
workflow *stage* can override limits, and the stage's AgentRun has no
field to carry them, and the AgentRun controller cannot see the stage.
The value has nowhere to go.

**2. Exit 2 mapped to `Succeeded` contradicts workflow sequencing.**
ADR 0011 maps harness exit 2 (limit reached) to phase `Succeeded` + a
`LimitReached` condition, while also stating any non-zero exit stops the
workflow. A `Succeeded` stage that stops the workflow is a
contradiction — `Succeeded` everywhere else means "proceed." Separately,
the terminal-outcome model in 0011 was never reconciled with how a
run-to-completion object should express its outcome, and PR #160
(readiness gate) has since reworked `AgentRun` status in a Pod-shaped
direction (`phase` + a `Ready` condition + a new `ACPReady` condition).

**3. A workflow guide has no single agent scope.** ADR 0009 says "All
text fields render from the same namespaced data context (`workflow.*`
and `agent.*`), so a workflow guide can reference agent params." But a
guide is one string shared by every stage, and each stage binds a
*different* Agent with a different param set. `$(agent.source_url)` in a
guide is ambiguous — it would resolve to a different value in every
stage, silently coupling ambient context to stage ordering. There is no
single "agent" in scope at the workflow level.

## Decision

### Execution fields are uniform on `AgentRun.spec`

`mode`, `maxTurns`, and `maxCost` all live on `AgentRun.spec`. The
AgentRun controller resolves `effective = AgentRun field if set, else
Agent default` and writes the `execution` section of `params.json`. This
is the single place params.json is assembled.

For workflow runs, the AgentWorkflowRun controller resolves each stage's
values (stage override, else Agent default) and **stamps them onto the
stage's AgentRun** at creation — the same mechanism it already uses to
stamp `KONVEYOR_WORKFLOW_*` env and the workflow guide. The AgentRun
controller stays ignorant of workflows; by the time it reconciles, the
stage values are already on the run.

Because the AgentRun spec is immutable, stamping also captures the
stage's execution config at creation time, immune to later workflow
edits — the determinism property issue #87 wants, for free.

**"Runs do not set limits" becomes governance, not schema.** ADR 0011's
intent — the Agent author controls the budget, a Developer running a
workflow cannot raise it — is preserved by the UI/API layer (Hub)
deciding who may set these fields, exactly as ADR 0011 already delegated
"who may set `mode`" to Hub. The fields exist on AgentRun so the workflow
controller can stamp them and so non-Konveyor callers (CLI, GitOps),
who are their own authority, can set them. The Konveyor UX does not
expose limit-raising to a migrator.

This supersedes ADR 0011's field-placement table rows for `maxTurns`
and `maxCost` (which said "no" on AgentRun). `mode` placement is
unchanged (AgentRun + stages, default `auto`).

The asymmetry — limits on the Agent, mode only on AgentRun/stage — is
enforced structurally, not by validation. The Agent's `execution` field
is an `ExecutionLimits` (`maxTurns`/`maxCost` only); AgentRun and stages
use `ExecutionSpec` (`ExecutionLimits` + `mode`). The Agent therefore
cannot express a mode default, honouring ADR 0011's "the Agent does not
declare mode — it is an execution-time concern": a template-level
`approve` default would stall every headless run (no attached viewer →
fail-closed). `resolveExecution` merges an `ExecutionSpec` override over
the Agent's `ExecutionLimits` base; mode comes only from the override.

### Terminal outcome is a `Succeeded` condition, not a phase

AgentRun is a run-to-completion object. The Kubernetes primitive it
resembles is Job (`Complete`/`Failed` **conditions**), and the
ecosystem's run-to-completion APIs (Tekton, Knative) use a single
`Succeeded` condition with a `reason`, not a rich phase. `Ready` is a
Pod steady-state property ("serving traffic") and is the wrong shape for
"did this run finish, and if not why."

The controller sets a **`Succeeded`** condition as the terminal signal:

| exit | phase (mirror) | `Succeeded` condition |
|------|----------------|-----------------------|
| — (running) | `Running` | `Unknown`, reason `Running` |
| 0 | `Succeeded` | `True`, reason `Succeeded` |
| 1 | `Failed` | `False`, reason `Failed` |
| 2 | `Failed` | `False`, reason `LimitReached` |
| 3 | `Failed` | `False`, reason `Refused` |

Future outcomes (issue #129) extend the reason set — `Refused` (added
2026-09-24, #241), `NoChanges` — without new phases or condition types.
`Refused` is the agent's own verdict: it ended its turn normally, but the
handoff section it wrote records `Status: failed`. `Failed` stays the
machinery breaking — a crash, a provider error, a failed push.

This supersedes ADR 0011's "exit 2 → phase `Succeeded` + `LimitReached`
condition." Exit 2 is phase `Failed` (it did not cleanly complete) with
`Succeeded=False, reason=LimitReached` carrying the truth. **Workflow
sequencing continues only when a stage is `Succeeded=True`**; `Failed`
and `LimitReached` both stop it — no more "Succeeded-but-stops."

`which` limit (turns vs cost) is **not** on the condition — the exit
code says only "a limit was reached." The granular detail lives in the
opaque `terminationData` (below) for the UI to render.

### Relationship to `phase`, `Ready`, and `ACPReady`

controller-runtime supplies only the mechanics of conditions
(`meta.SetStatusCondition`, `lastTransitionTime`/`observedGeneration`
bookkeeping); it has no opinion on which conditions exist or what they
mean. The Kubernetes API conventions treat conditions as *orthogonal
observations*, not a state machine — and the core resources disagree in
practice (Pod has parallel `Ready`/`ContainersReady`; Job uses
`Complete`/`Failed` and omits them until they occur; Tekton/Knative use
one `Succeeded` that goes `Unknown`→`True`/`False`). We choose the
Tekton/Knative `Succeeded` shape. An AgentRun then carries exactly two
orthogonal condition facts, plus the coarse mirror:

- **`ACPReady` (PR #160) stays as-is.** The serving fact — whether the
  live ACP endpoint accepts connections during the execution window.
  `True` while the agent is running and dialable, `False` before it is up
  and once it has finished.
- **`Succeeded` is the terminal-outcome fact.** `Unknown` (reason
  `Running`/`PodNotRunning`/`AgentNotReady`/…) while the run is still in
  progress; `True`/`False` with a reason once it ends.
- **`phase` stays** as a coarse compatibility mirror. ADR 0012 froze it
  as the client contract (`waitForRunning`, `isTerminalPhase`). The
  controller keeps `phase` and `Succeeded` in lockstep.
- **`Ready` is removed from AgentRun.** It added no fact beyond
  `ACPReady` (serving) and `Succeeded` (outcome): in the prior code it
  sat `False` the entire time the agent was running and only flipped
  `True` *after* the run finished — backwards for a serving flag and a
  duplicate of the terminal signal. Every former `Ready=False` site
  becomes `Succeeded=Unknown` (still working) or `Succeeded=False`
  (terminal failure). Other CRDs (Agent, Gateway, SkillCard, …) keep
  their own steady-state `Ready`; only the run-to-completion AgentRun
  drops it.
- **Deprecation path:** once ADR 0012's clients and the controller state
  machines read `Succeeded` instead of `phase`, a future ADR supersedes
  0012's phase clause and drops `phase`.

### The `Verified` condition (added 2026-11-02, #302)

`Succeeded` answers *did the run complete*. It cannot answer *does what
the run produced hold up*, because its only evidence is the harness exit
code, and the harness exits 0 when the agent ends its turn normally —
whatever state the tree is in. The dogfooding case in #300: a verify
stage broke the build during a lint round, never re-ran the compiler,
wrote `Build: passed` into its handoff and pushed a branch failing `tsc`
with 45 errors. Exit 0, phase `Succeeded`, `Succeeded=True`. A second run
on the same branch was genuinely green and rendered identically.

#300 makes the harness re-run the plan's own `## Verification` commands
after the turn and before the push, recording each exit code under the
`verification` key of the termination blob. That is a measurement of the
tree, independent of the agent's account of it. This decides where it
surfaces.

**It is a condition, not a new field and not a phase.**

- *Not a phase, and not a `Succeeded` reason.* A failed verification and
  a failed run are different facts, and a run can honestly be both
  succeeded and unverified. Folding verification into `Succeeded` would
  make a stage that did its work and found real breakage indistinguishable
  from one that crashed, and would silently halt workflows on a signal
  that was previously advisory. Conditions exist for orthogonal
  observations; this is the third such axis, alongside `ACPReady`
  (serving) and `Succeeded` (outcome). The condition is advisory: workflow
  sequencing continues to key off `Succeeded=True` alone.
- *Not a typed `status.verification` struct.* Mirroring the harness's
  per-command records (label, command, exit code, duration, output tail)
  into the API would make the controller own a schema it does not
  produce and cannot validate, versioned independently of it — the
  harness image is pinned per Agent, so a controller struct silently
  drops fields a newer harness adds, where `RawExtension` passes them
  through. It would also put the same outcome in a fourth place, which
  is the drift ADR 0020 exists to stop. The verdict is small and bounded
  and belongs in the status vocabulary; the evidence is neither, and
  already has a home.
- *Not "leave it in `terminationData`".* The blob is the harness's own
  schema under a 4 KiB kubelet cap whose trim ladder sheds content under
  pressure; it is the right place for evidence and the wrong place for a
  verdict that `kubectl`, Hub and any non-UI consumer must be able to
  read without parsing an explicitly opaque field.

**The mapping**, from the blob's `verification.status` alone:

| `verification.status` | `Verified` | Reason |
| --- | --- | --- |
| `passed` | `True` | `VerificationPassed` |
| `failed` | `False` | `VerificationFailed` |
| `incomplete` | `False` | `VerificationIncomplete` |
| `notRun` | `False` | `VerificationNotRun` |
| absent, unreadable, or unrecognised | `Unknown` | `VerificationNotReported` |

`True` is reachable only from an explicit `passed`. Every other input —
including a harness predating #300, a trimmed blob, and a status value
this controller does not know — lands on `False` or `Unknown`. Silence
never renders as green, which is the property the whole change exists
for. `notRun` is `False` rather than `Unknown` because the tree was in
fact not verified; `Unknown` is reserved for the controller being unable
to say. The condition is set on every run whose agent container
terminated; runs that never started carry none, their `Succeeded`
condition already saying the agent never ran.

**The reason set is closed, and grew once.** The four reasons above were
three plus a catch-all until #300's review added a fourth harness status:
`incomplete`, a measurement that started and could not finish — the step
cap left commands unrun, the run was cancelled part way, or the working
tree was not the commit being pushed, so no result could honestly be
claimed for it. The catch-all handled it safely (`Unknown` +
`VerificationNotReported`, never a pass), which is what the catch-all is
for, but it said "the controller cannot tell" where the harness was
saying "I measured and could not finish". That is a fact worth keeping,
so `VerificationIncomplete` was added and the set is five.

The precedent this sets is deliberate: a closed set is not a frozen one.
A new harness status becomes a new reason by an explicit API change, in a
release note, not by a consumer discovering an unfamiliar string. In the
interval, the catch-all holds — which is why consumers switching over
the set must keep a default branch that claims nothing. The invariant
that does not move is the asymmetry: `VerificationPassed` is the only
reason that pairs with `Verified=True`, and no reason added later will
change that.

**The opacity exception is two keys.** The controller reads
`verification.status`, treats it as a closed enum, and copies the
harness's `verification.reason` into the condition message as text. It
also reads `verification.commit` — the branch tip the harness confirmed
the working tree matched before any command ran (#300) — and names it,
abbreviated, in the same message: a verdict that does not say which tree
it describes cannot be checked against anything, and "verified" over an
unnamed tree is a weaker version of the claim this change exists to
remove. The value is validated as a commit and dropped if it is not one;
it is never parsed back out of the message, which stays human-readable
with the full SHA in the blob. The commands, exit codes, durations and
output tails are not read, validated or re-shaped. `terminationData`
remains the harness's contract with UIs for everything else.

### The workflow guide renders from `workflow.*` only

The guide is workflow-ambient text; it substitutes `$(workflow.<name>)`
references only. A `$(agent.<name>)` reference in a guide is a terminal
error (unresolved reference), not a silent per-stage value. Agent params
still reach each stage through that stage's **instructions** and the
Agent **prompt**, both of which render from both scopes (`workflow.*` and
`agent.*`) because a stage is bound to exactly one Agent.

This narrows ADR 0009's "a workflow guide can reference agent params" to:
the guide renders from `workflow.*`; stage instructions and the agent
prompt render from both scopes.

### terminationData (unchanged from ADR 0011)

The harness writes an opaque JSON usage blob to `/dev/termination-log`;
the controller reads the pod's termination message (via the pod-read
plumbing PR #160 adds) and stores it on `AgentRun.status.terminationData`
(`RawExtension`), uninterpreted. The kubelet's 4 KiB cap applies.

Amended 2026-11-02 (#302): all of it stays uninterpreted but for the
`verification.status` key, from which the controller derives the
`Verified` condition above, and `verification.commit`, which that
condition's message names. Nothing else in the blob is read, and nothing
is re-shaped onto the API.

## Consequences

- `AgentRun.spec` gains `maxTurns`/`maxCost` (alongside `mode`). The
  AgentWorkflowRun controller stamps stage-resolved execution config
  onto stage AgentRuns.
- The controller sets a `Succeeded` condition (`Unknown` while running,
  `True`/`False` terminal); `phase` remains a coarse mirror; `Ready` is
  removed from AgentRun (other CRDs keep theirs); `ACPReady` is unchanged.
- Exit 2 → phase `Failed` + `Succeeded{False, LimitReached}`. Workflow
  sequencing keys off `Succeeded=True`.
- #119 builds on PR #160's pod-read (RBAC, restricted pod cache,
  resolve-by-name), extending it to read container `exitCode` and the
  termination message.
- The workflow guide renders from `workflow.*` only; an `$(agent.*)`
  reference in a guide fails the run. Stage instructions and the agent
  prompt still render from both scopes.
- A future ADR retires `phase` once clients migrate to `Succeeded`.
- (2026-11-02, #302) AgentRun carries a third condition, `Verified`,
  reporting the harness's independent measurement of the pushed tree.
  `True` only from an explicit `passed`; absence, `notRun`, `incomplete`
  and unrecognised values never read as a pass. It does not affect
  `phase`, `Succeeded`, or workflow sequencing. The controller reads two
  keys of `terminationData` to set it (`verification.status` and
  `verification.commit`); the per-command evidence stays in the blob.
  konveyor/tackle2-ui#3703 is the consumer.
- (2026-11-02, #302) The `Verified` reason set holds five values. It is
  closed to consumers — the controller never invents a reason — but it
  grows by API change when the harness's own status enum does, as it did
  for `VerificationIncomplete`. A consumer switching over it keeps a
  default branch that claims nothing; only `VerificationPassed` ever
  pairs with `Verified=True`.
