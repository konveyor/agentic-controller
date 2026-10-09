---
adr: "0020"
title: "Run Outcome Model"
description: "Consolidates the terminal-outcome taxonomy that is currently accreting across ADRs 0011 and 0018 and the AgentRunPhase enum."
status: proposed
status_note: "Opened with its Decision section unresolved. Six questions (Q1-Q6) need answers before this ADR can be accepted; the Context and Consequences are settled."
date: "2026-10-09"
last_updated: null
authors:
  - "Ian Bolton"
last_reviewed: "2026-10-09"
implementation_status: deferred
review_note: "No implementation is proposed by this ADR. It names the outcome set that ADRs 0011 and 0018 are each extending piecemeal, and blocks on Q1-Q6 before any consolidation lands."
---

# ADR 0020: Run Outcome Model

## Context

A run's terminal outcome is specified in three places, none of which was
designed to hold a taxonomy:

- **ADR 0011** — the harness exit-code contract (the `outcome` enum and
  `exitCode()` in `harness/cmd/migration-harness/outcome.go`)
- **ADR 0018** — execution fields on AgentRun and the `Succeeded` terminal
  condition
- **`api/v1alpha1`** — the `AgentRunPhase` enum

Outcomes are arriving in those three places from unrelated threads, one PR
at a time:

| Outcome | Exit | Condition | Source | State |
| --- | --- | --- | --- | --- |
| `succeeded` | 0 | `Succeeded=True` | original | shipped |
| `failed` | 1 | `Succeeded=False` | original | shipped |
| `limitReached` | 2 | see Q4 | ADR 0011 | shipped |
| `refused` | 3 | `Succeeded=False{Refused}` | #241 (PR #249) | in review |
| `noChanges` | ? | proposed `Succeeded=False` | #129 | planned |
| `cancelled` | n/a | phase `Cancelled` | #65, ADR 0006 | planned |
| lost work | ? | ? | #166 | unscoped |

Each addition is individually reasonable and individually documented. The
problem is that no document owns the set, which has three consequences.

**1. Per-condition TTL cannot be specified.** ADR 0006 and #66 define
`ttlSecondsAfterSucceeded`, `ttlSecondsAfterFailed`, and
`ttlSecondsAfterCancelled`. That presumes the terminal set is closed. ADR 0006
named three conditions; the harness already distinguishes four outcomes and is
heading toward six. Retention policy cannot be written against a moving enum,
so #66 is blocked on a question that lives nowhere.

**2. Consumers cannot render exhaustively.** tackle2-ui's `runOutcome.ts`,
`RunConditionSummary.tsx`, and the broken-runs filter each need updating per
outcome, per PR, in a different repository, with no contract telling them when
the set is complete. A missed outcome degrades silently to an unlabelled run.

**3. Workflow halting is a side effect rather than a rule.** The workflow
controller stops on any `Succeeded=False`. Whether a given outcome halts a
workflow is therefore decided by which condition an implementer picked when
adding it, not by a stated policy.

## Decision

**This section is deliberately unresolved.** The questions below are the
decision; this ADR cannot be accepted until they are answered. They are opened
here rather than settled privately because each one has a consumer outside the
controller.

### Q1 — Is `noChanges` a success or a failure?

PR #249 proposes `Succeeded=False`. The counter-argument is that a stage which
correctly determined there was nothing to do performed its function. Under
`Succeeded=False` it halts the workflow and inherits `ttlSecondsAfterFailed`
retention, both of which look wrong for a correct no-op.

Options: (a) `Succeeded=False{NoChanges}`; (b) `Succeeded=True{NoChanges}`;
(c) `Succeeded=True` plus a separate `Changed` condition carrying the fact.

### Q2 — Does `cancelled` imply `Succeeded=False`?

#65 adds a `Cancelled` phase. Open: whether a corresponding `Succeeded`
condition is set, and whether cancelling one stage halts its workflow or marks
the workflow cancelled as well.

### Q3 — Where does lost work (#166) sit?

Abnormal termination holding uncommitted work is not the agent's own verdict
(`refused`) and is not cleanly machinery failure (`failed`) — the
distinguishing fact is that output was destroyed rather than that a step
broke. Own outcome, or `failed` with a reserved reason?

### Q4 — Is `limitReached` success or failure today, and is that right?

Shipped behaviour predating this ADR. ADR 0018 already revisited ADR 0011 on
this point. State the current answer explicitly rather than inheriting it.

### Q5 — Exit-code allocation

`refused` took exit 3 ad hoc. Reserve a range, state what an unrecognised exit
code maps to, and record whether exit codes are part of the contract or an
implementation detail of the harness/controller pair.

### Q6 — Which outcomes are resumable?

Bears on #128 (continuation runs). `limitReached` is presumably resumable and
`refused` presumably is not, but continuation will infer a rule from whatever
ships unless one is stated here.

## Consequences

- Closes the terminal-outcome set, unblocking #66's per-condition TTL.
- Gives tackle2-ui and any other client a single enumerable contract in place
  of per-PR updates discovered after the fact.
- Makes workflow halting a stated rule rather than a consequence of which
  condition an outcome happened to be given.
- Scopes ADRs 0011 and 0018 back to their own concerns — exit-code mechanics
  and execution-field placement respectively — with the taxonomy itself
  referenced here. This is a partial supersession of specific clauses, not a
  reversal of either decision; both remain authoritative elsewhere.
- Future outcomes become a visible amendment to one document rather than a row
  added to whichever ADR the author was already editing.

## References

- ADR 0006 — Hub addon pattern for agent resources (per-condition TTL)
- ADR 0011 — Execution controls and mode on CRDs (exit-code contract)
- ADR 0018 — Execution fields on AgentRun, and a `Succeeded` terminal condition
- #129 (refusal and no-op outcomes), #166 (lost work), #241 (handoff status),
  #128 (continuation runs)
- #264 — run lifecycle controls epic (#65 cancel, #66 per-condition TTL,
  #67 concurrency)
- konveyor/tackle2-ui — `runOutcome.ts`, `RunConditionSummary.tsx`
