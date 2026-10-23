---
adr: "0021"
title: "Usage Telemetry on AgentRun and AgentWorkflowRun"
description: "Promotes a small usage object into the harness contract, gives AgentRun and AgentWorkflowRun typed per-stage and per-run usage, and defines how the console shows it."
status: proposed
status_note: "Amends ADR 0011 (usage reporting: opaque terminationData, standard-ACP-only usage) and ADR 0018 (terminationData unchanged from 0011). Tracks MTA-8049."
date: "2026-10-20"
last_updated: null
authors:
  - "Dylan Murray"
last_reviewed: "2026-10-20"
implementation_status: deferred
review_note: "Not implemented. Today usage exists only as opaque terminationData on each AgentRun (turns, final context, cost); nothing reaches AgentWorkflowRun."
relates_to:
  - "0002"
  - "0011"
  - "0018"
  - "0020"
---

# ADR 0021: Usage Telemetry on AgentRun and AgentWorkflowRun

## Context

An AgentWorkflowRun that drives a complex code migration can succeed or
fail for reasons that never show in its phase. Two of the most common are
**context rot** — a stage that fills its context window, gets compacted,
and loses track of the work — and **poor context handoff** between stages.
Diagnosing either needs to know how full each stage's context got and how
many tokens it spent. The same numbers let users trying different skills,
tools, or workflow shapes see which variant does the job with fewer tokens,
and give a rough cost for a run (MTA-8049).

### What exists today

- **The harness already reads usage.** It consumes goose's
  `_goose/unstable/session/update` stream and keeps the last `usage_update`
  snapshot of context `used`/`size` and cumulative `cost.amount`, plus a
  turn count (`harness/internal/acp/session.go`, `trackUsage`). It writes
  `usage{turnsUsed, contextUsed, contextSize, cost}` into the termination
  blob (`harness/cmd/migration-harness/outcome.go`).
- **The harness discards token counts.** The same goose frames carry
  `accumulatedInputTokens` and `accumulatedOutputTokens` — the console's
  live `UsageBadge` already reads them (tackle2-ui `ChatPanel.tsx`,
  `mergeUsage`) — but the harness ignores them. It also records only the
  *final* context occupancy, which compaction can make look healthy.
- **The controller treats terminationData as opaque.** ADR 0011 (*Usage
  reporting*) and ADR 0018 store the termination message as a
  `RawExtension` on `AgentRun.status.terminationData`, uninterpreted. The
  stated reason: the harness is replaceable, "a different harness writes
  different data," and only the platform UI that pairs with a harness knows
  its schema. Exit codes and `params.json` are the cross-harness contract;
  usage was deliberately left out of it.
- **ADR 0011 limits usage to standard ACP.** "The harness reports only what
  the ACP standard provides: context window `used`/`size` and cumulative
  `cost`." Per-turn token breakdowns are a draft ACP RFD.
- **Nothing reaches the workflow run.** `AgentWorkflowRunStageStatus`
  carries `name`, `phase`, `agentRunName` and the stage snapshot. To show
  usage for a run, the console would fetch every stage's AgentRun and parse
  each opaque blob.
- **Cost is goose's own estimate.** goose prices tokens from its built-in
  price list; cost is optional in `usage_update` and absent when goose does
  not know the model. The Gateway's model entry has `name` and
  `contextWindow`, no price. The harness enforces `maxCost` against this
  number.
- **The console shows usage one AgentRun at a time.** A live chip while
  connected (lost on reconnect) and a "turns / context used" line after
  finish. No per-stage, per-run, or cross-run view.

### Constraints carried forward

- The controller stays a stateless reconciler with no ACP connection
  (ADR 0002). Usage arrives at stage end through the termination message.
- The kubelet caps the termination message at 4 KiB (ADR 0011).
- The harness has no Kubernetes client and does not write status
  (ADR 0002 rejected that).
- goose is the only runtime today.

## Decision

### 1. A usage object joins the harness contract

The `usage` object in the termination blob becomes part of the
cross-harness contract, alongside exit codes and `params.json`. Every
field is optional. A harness that omits `usage`, or any field of it, is
conforming; the platform shows that data as not reported.

```json
"usage": {
  "inputTokens":     1290000,
  "outputTokens":    61000,
  "peakContextUsed": 194000,
  "compactions":     3,
  "contextUsed":     121000,
  "contextSize":     200000,
  "turnsUsed":       166,
  "cost":            4.59,
  "currency":        "USD",
  "model":           "claude-sonnet-4-5"
}
```

| Field | Meaning |
| --- | --- |
| `inputTokens`, `outputTokens` | Cumulative tokens for the whole stage session, including the limit-handoff prompt. |
| `compactions` | Number of times the runtime compacted the context during the stage. **The primary context-rot signal.** |
| `peakContextUsed` | Highest context occupancy seen at any point in the stage. Supporting detail: runtimes that auto-compact (goose does, at a fixed fraction of the window by default) cap this near their compaction threshold, so on its own it mostly measures how long a stage ran. |
| `contextUsed` | Occupancy at the end of the stage (the existing field). |
| `contextSize` | Context window size the runtime reported. |
| `turnsUsed` | Existing field, unchanged. |
| `cost`, `currency` | Cumulative cost **as reported by the runtime**, and its currency. Not computed by the platform. |
| `model` | Model the stage ran against. |

The rest of the termination blob (`outcome`, `limitReached`,
`handoffStatus`, `stopReason`) stays harness-private and opaque.

The object evolves additively: new optional fields may be added; existing
fields are never renamed or re-typed. Readers ignore unknown fields. There
is no version field.

The object is under 400 bytes, well within the 4 KiB cap. It is never
dropped by `writeTerminationLog`'s overflow trimming before `stopReason`
is cut; the minimal fallback blob keeps `usage` alongside `exitCode`,
`outcome` and `limitReached`.

### 2. The harness reports goose token counts as a stopgap

This amends ADR 0011's "standard ACP only" rule for token counts. The
harness reads `accumulatedInputTokens` and `accumulatedOutputTokens` from
goose's `usage_update` frames, and `cost.currency` alongside
`cost.amount`. These are goose-specific. When ACP standardizes per-turn
usage (the end-turn token-usage RFD), the harness switches to the standard
source and the contract fields stay the same.

The harness also:

- tracks `peakContextUsed` as the maximum `used` across every
  `usage_update` frame, across both the primary and limit-handoff prompts;
- counts `compactions`. If goose sends an explicit compaction notice, the
  harness counts those. Otherwise it counts a compaction whenever `used`
  falls below half of the previous frame's `used`. Occupancy only shrinks
  that much when context is discarded. The 50% figure is a starting point
  and should be tuned against real runs. Counts from the primary and
  limit-handoff prompts are added together;
- combines the two prompts' token counts like cost — goose's counters are
  session-cumulative, so the later non-zero value wins and nothing is
  summed;
- fills `model` from its resolved model configuration
  (`KONVEYOR_LLM_MODEL`);
- writes `currency: "USD"` when goose reports `cost` without a currency.
  This makes explicit an assumption the platform already holds: `maxCost`
  is USD-only (ADR 0018), and the console's live chip defaults to USD.
  The controller can therefore assume `cost` always comes with a
  `currency`.

### 3. The controller reads only `usage`, into typed status

This amends ADR 0011 and ADR 0018: the controller now interprets **one
documented part** of the termination message, the `usage` object, and
nothing else.

**AgentRun.** `AgentRunStatus` gains `usage *RunUsage`, parsed from
`terminationData.usage` at the same point `terminationData` is copied
today. `terminationData` itself is still stored verbatim. A missing or
malformed `usage` leaves `status.usage` nil; it never fails the run or
changes its outcome.

**AgentWorkflowRun stage.** `AgentWorkflowRunStageStatus` gains:

- `usage *RunUsage`, copied from the child AgentRun's `status.usage` when
  the stage reaches a terminal phase;
- `startTime` and `completionTime`, copied from the child AgentRun, so the
  console can show stage duration without fetching the child.

**AgentWorkflowRun total.** `AgentWorkflowRunStatus` gains
`usage *RunUsage`, recomputed whenever a stage's usage is copied:

| Field | Rollup |
| --- | --- |
| `inputTokens`, `outputTokens`, `turnsUsed`, `compactions` | Sum over stages that reported them. |
| `peakContextUsed` | Max over stages. |
| `cost` | Sum over stages that reported cost, **only when all reporting stages share one currency**; otherwise unset. |
| `currency` | The shared currency, when `cost` is set. |
| `contextUsed`, `contextSize`, `model` | Unset — not meaningful across stages. |

The total covers finished stages only, so it grows as a run progresses.

`RunUsage` is one Go type shared by all three, with every field optional:
`*int64` for counts, and `cost` as a decimal string like
`ExecutionSpec.maxCost` (CRDs avoid floats).

### 4. The console shows per-stage usage on the run detail page

On the **workflow run detail page** (tackle2-ui
`workflow-run-detail-page.tsx`):

- **Stage stepper.** Each `ProgressStep` gains a small peak-context bar:
  `peakContextUsed / contextSize`, and a "Compacted N×" label when
  `compactions` > 0. The compaction label is the attention signal: it is
  warning-colored at 1 or more. The bar itself stays neutral, because
  auto-compaction caps peak occupancy and a color threshold on it would
  mostly flag long stages. A stage without usage shows no bar.
- **Usage section**, new, between the stepper and the chat panel:
  - a totals row: total tokens (input + output), reported cost, turns,
    compactions, duration;
  - a per-stage table: stage, phase, input tokens, output tokens, peak
    context as `194k / 200k`, compactions, turns, reported cost, duration.
- A stage that has not finished shows dashes. While it runs, its live
  numbers stay where they are today, in the chat panel's usage chip.
- A stage whose harness reported no usage shows "Not reported," never
  zeros.
- **Cost is labeled as runtime-reported.** The column header and totals
  tile say "Reported cost", with a tooltip explaining it is the runtime's
  estimate at list price and may be missing or differ from actual billing.
  Cost is formatted in the reported currency. A blank cost is blank, not
  `$0`. When some stages reported cost and others did not, the total
  carries a "partial" marker.
- Built from PatternFly `Card`/`Table`/`Progress` components; no Victory
  charts.

On the **workflow runs list** (`workflow-runs-page.tsx`): two sortable
columns, **Total tokens** and **Reported cost**, from
`status.usage`. This is the cross-run view; comparing two variants is
reading two rows.

On the **agent run detail page**, `TerminationUsage` reads
`status.usage` and adds tokens and cost.

**Older runs and older controllers.** tackle2-ui ships separately from the
controller. Where `status.usage` is absent, the console falls back to
`terminationData.usage`, now a documented schema. This keeps the turns and
context line on AgentRuns that finished before the upgrade. Workflow-run
views do not fall back: stages that finished before the upgrade show
"Not reported".

Hub needs no change: it already proxies full CRs to the console.

### Out of scope

- **A context curve within a stage**, including *when* each compaction
  happened (this ADR records only how many).
  This is a time series; it fits neither the 4 KiB termination message nor
  CR status, and needs its own storage decision. Named follow-up.
- **Cache tokens.** goose does not report them.
- **Platform-computed cost.** A per-model price on the Gateway was
  considered and deferred (see Alternatives).
- **Budgets, quotas, alerts.** Cost is shown, not enforced, beyond the
  existing `maxCost`.
- **A comparison view or cross-run dashboard.**
- **Per-stage outcome on AgentWorkflowRun.** The table shows stage phase.
  Richer outcome (`LimitReached`, `Refused`) on stage status waits on
  ADR 0020's outcome model.
- **Tool-call counts and per-tool breakdowns.**

## Consequences

- The harness contract grows a documented `usage` object. Any future
  harness that wants its runs to show usage must write it; one that does
  not still works.
- The controller is no longer strictly uninterpreting of terminationData.
  The boundary moves from "never read it" to "read only `usage`," and that
  boundary must hold: any further field the controller wants goes through
  the contract the same way, by ADR.
- The token fields depend on goose-specific notifications until ACP
  standardizes usage. A goose upgrade that renames them silently empties
  the token columns; the harness should log once when a `usage_update`
  arrives without token fields.
- Unless goose sends an explicit notice, the compaction count comes from a
  heuristic. A large drop in occupancy for any other reason would be
  counted as a compaction, and a compaction that frees little context
  would be missed.
- Cost is only as good as goose's price list: blank for unknown models and
  wrong for negotiated or self-hosted pricing. The UI labels it so.
- `AgentRunStatus`, `AgentWorkflowRunStageStatus` and
  `AgentWorkflowRunStatus` grow. Each `RunUsage` is a handful of numbers;
  status size stays small.
- tackle2-ui's `api/agentic/contract.ts` mirror gains `RunUsage` and the
  new status fields.
- CONTEXT.md gains a **Usage** entry (the contract object and its rollup),
  and its Harness and Execution Limits entries are updated to match.
- This ADR merges as proposed. The first implementation PR to change the
  contract or the controller (harness or controller, whichever lands
  first) also adds dated `Update` notes to ADRs 0011 and 0018 pointing
  here, updates CONTEXT.md, and moves this ADR's
  `implementation_status`.

## Alternatives considered

**Console aggregates from each stage's AgentRun.** No API change: the UI
fetches every stage's AgentRun and parses the opaque blob. Rejected: N
fetches per run, N×M for the list page, and the metric definitions live
only in one UI. Typed status gives one definition and makes the list
columns cheap.

**Harness writes usage straight to the CR.** Rejected by ADR 0002, and
the harness has no Kubernetes client.

**Controller reads usage from the pod over ACP.** Violates the stateless,
no-ACP controller principle (ADR 0002).

**Standard-ACP-only usage.** Keep ADR 0011's rule and ship context and
cost without tokens. Rejected: tokens are the number users compare
workflows by.

**Price per model on the Gateway.** Optional input/output price per
million tokens on the Gateway's model entry; the UI computes cost from
tokens. Accurate to what the admin enters and works for self-hosted
models. Deferred: it adds configuration someone has to maintain, and
runtime-reported cost is enough for a first cut. Natural follow-up if
reported cost proves too often blank or wrong.

**Metrics backend (Prometheus/OpenTelemetry).** Good for fleet dashboards
and time series; would carry the within-stage curve. Out of scope here,
and a candidate home for that follow-up.
