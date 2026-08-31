---
adr: "0011"
title: "Execution Controls and Mode on CRDs"
description: "Defines runtime-agnostic execution limits and supervision mode on CRDs, with enforcement and translation in the harness."
status: proposed
date: "2026-08-05"
last_updated: "2026-08-31"
authors:
  - "David Zager"
last_reviewed: "2026-08-31"
implementation_status: amended
review_note: "Execution controls, cost monitoring, native turn limits, mode translation, and termination reporting are implemented. ADR 0018 supersedes its AgentRun field-placement and exit-2 mapping."
---

# ADR 0011: Execution Controls and Mode on CRDs

**Update (2026-08-31):** The controller and entry point now implement the
execution section of `params.json`, native turn limits, ACP cost monitoring,
mode translation, handoff, exit codes, and opaque termination data. ADR 0018
supersedes this ADR’s AgentRun field-placement table, workflow-stage
stamping model, and exit-2 status mapping; those original statements remain
as decision history.

## Context

Execution controls — how long an agent runs, how much it costs, whether
a human supervises it, and what kind of work it does — are currently
either absent from the CRDs or implemented in the wrong layer:

- **Turn limits** are enforced client-side by the harness counting
  `tool_call` ACP notifications, despite goose supporting
  `GOOSE_MAX_TURNS` natively. The harness never sends `session/cancel`
  when the limit is hit — it just stops reading, and goose keeps
  running.
- **Iteration caps** (`KONVEYOR_PARAM_MAX_FIX_ITERATIONS`) are encoded
  in the verify skill as natural-language instructions to the LLM —
  execution control in the wrong layer (ADR 0010).
- **Cost and token budgets** do not exist. The ACP protocol's
  `usage_update` notification (stabilised June 2026) provides
  real-time `used`, `size`, and optional `cost` data, but the harness
  ignores it.
- **Mode** (`auto` vs `approve` — whether tool calls require human
  approval) is not a CRD field. ADR 0008 established the tee topology
  that relays permission asks to attached viewers, but there is no
  way for an Agent author or run creator to declare supervision
  policy.
- **Session type** (whether the runtime should plan or execute) is not
  a CRD concept. The harness always sends one prompt and waits.
  Goose, OpenCode, Claude Code, and Codex CLI all have native
  planning modes that produce a plan before executing, but the
  harness cannot leverage them.

These are universal execution concerns that apply to any agent, not
domain-specific values. They belong as first-class CRD fields, not
as skill instructions or arbitrary parameters.

## Decision

### Three categories of execution controls

#### 1. Execution limits

Two optional fields, each independently settable. Whichever limit is
hit first triggers wind-down.

| Field | Type | Meaning |
|-------|------|---------|
| `maxTurns` | `int` | Maximum turns before wind-down (runtime-defined unit; e.g. goose counts turns without user input) |
| `maxCost` | `string` | Maximum cumulative cost (USD) before wind-down |

`maxTokens` is intentionally excluded. The ACP `usage_update`
notification reports context-window occupancy (`used`/`size`), not
cumulative token consumption — occupancy drops on compaction, making
it unsuitable as a budget metric. The ACP end-turn-token-usage RFD
(which would provide per-turn input/output breakdowns) is still in
draft with divergent implementations (issue #1860). `maxCost` is
cumulative per the ACP spec and is the correct cost-control
mechanism. `maxTokens` can be added when the ACP token-usage standard
stabilises.

When unset, the field falls back to the next level in the resolution
hierarchy. When unset at every level, the runtime's own default
applies (goose defaults to 1000 turns; cost is unbounded).

#### 2. Mode

Controls supervision policy for tool execution.

| Value | Meaning |
|-------|---------|
| `auto` | All tool calls approved automatically. No permission asks. Headless-safe. |
| `approve` | Tool calls require explicit approval. The tee (ADR 0008) relays asks to attached viewers. If no viewer is attached, the tee's fail-closed policy denies all asks — the agent cannot proceed. |

`smart_approve` is intentionally excluded. For the migration use case
the agent's core work is writing files and running shell commands —
`smart_approve` would trigger a permission ask on every meaningful
action, making it functionally identical to `approve`. It can be
added later if a use case emerges.

Mode defaults to `auto`. It is set on AgentRun for standalone runs
and on individual AgentWorkflow stages for workflow runs. The Agent
does not declare mode — it is an execution-time concern, not a
template concern.

Who can set mode (and to what values) is an authorization concern
owned by the UI/API layer (e.g. Hub), not the controller. The
controller validates that the value is one of `auto` or `approve`
but does not enforce policy about who may set it.

#### 3. Session type (deferred)

Session type (`plan` | `execute`) is reserved for future use. The
ACP protocol supports session mode selection via
`session/set_config_option`, and Codex ACP implements a `plan`
collaboration mode today. However, support across runtimes is
immature: goose's plan mode is CLI-only (not available via ACP),
Claude Code's `--permission-mode plan` is a server-side launch
config (not session-level), and OpenCode's plan agent is not
documented as ACP-accessible.

For dev preview, all stages use the runtime's default execution
mode. Stage-specific behavior comes from skills (the plan skill
tells the agent to produce a plan), not from session type
configuration. The harness may leverage runtime-specific plan
capabilities as they become available via ACP.

Stage-transition gating (requiring human acceptance of a stage's
output before the next stage proceeds) is a separate concern from
within-stage tool approval (`mode`). See issues #55 and #56 for
the HITL stage-gate design. This ADR does not address stage
transitions.

### CRD field placement

| Field | Agent | AgentRun | AgentWorkflow stage | AgentWorkflowRun |
|-------|-------|----------|---------------------|------------------|
| `maxTurns` | yes (default) | no | yes (override) | no |
| `maxCost` | yes (default) | no | yes (override) | no |
| `mode` | no | yes | yes | no |

**Execution limits** (`maxTurns`, `maxCost`) are set on the Agent
as defaults, overrideable per workflow stage. Runs do not set limits
— the Agent author controls the budget. A future enhancement may
allow runs to lower (but not raise) limits.

**Mode** is set on the AgentRun for standalone runs and on individual
workflow stages. Defaults to `auto`. There is no mode on the Agent
or AgentWorkflowRun — mode is a per-execution concern. Who can set
mode is an authorization concern owned by the UI/API layer (e.g.
Hub), not the controller.

Example:

```yaml
apiVersion: konveyor.io/v1alpha1
kind: Agent
metadata:
  name: migration-agent
spec:
  maxTurns: 200
  maxCost: "10.00"
  # no mode — that's a run/stage concern
  # ...
---
apiVersion: konveyor.io/v1alpha1
kind: AgentWorkflow
metadata:
  name: migration-workflow
spec:
  stages:
    - name: plan
      agentRef: migration-agent
      mode: approve              # this stage needs supervision
      maxTurns: 100              # planning needs fewer turns
    - name: execute
      agentRef: migration-agent
      # mode defaults to auto, inherits agent maxTurns: 200
    - name: verify
      agentRef: migration-agent
      maxTurns: 50               # verification is shorter
---
apiVersion: konveyor.io/v1alpha1
kind: AgentRun
metadata:
  name: standalone-run
spec:
  agentRef: migration-agent
  mode: approve                  # run creator wants supervision
```

### Harness translation

The controller resolves the effective values and writes them to the
`execution` section of `params.json` (ADR 0009):

```json
{
  "execution": {
    "maxTurns": 100,
    "maxCost": "10.00",
    "mode": "auto"
  }
}
```

The harness reads `execution` and translates to the runtime:

| Control | Goose | Notes |
|---------|-------|-------|
| `maxTurns` | `GOOSE_MAX_TURNS` env var (set to ~85% of configured value) | Runtime enforces natively; harness reacts to outcome |
| `maxCost` | Harness monitors ACP `usage_update.cost.amount` | No native runtime equivalent; harness enforces via cancel |
| `mode: auto` | `GOOSE_MODE=auto` env var | Set before launching runtime |
| `mode: approve` | `GOOSE_MODE=approve` env var | Set before launching runtime |

The CRDs are runtime-agnostic. The harness is the translation layer.
Each harness implementation maps CRD values to its runtime's native
mechanisms. The harness sets mode via environment variables before
launching the runtime, not via ACP protocol methods (which vary
across protocol versions — v1's `session/set_mode` is replaced by
v2's `config_option`).

### Enforcement

The harness enforces execution limits by monitoring the ACP
`usage_update` notifications that goose already sends (and that the
tee from ADR 0008 already sees):

```json
{
  "sessionUpdate": "usage_update",
  "used": 53000,
  "size": 200000,
  "cost": { "amount": 0.045, "currency": "USD" }
}
```

The harness tracks **cost only** — via `cost.amount` from ACP
`usage_update` notifications (cumulative per the ACP spec). Turn
limits are enforced by the runtime natively (e.g. `GOOSE_MAX_TURNS`);
the harness does not count turns itself. Only standard ACP
`usage_update` data is used — no runtime-specific custom
notifications.

#### Reserved budget and wind-down

The harness reserves 15–20% of each active limit for wind-down. When
the primary budget is exhausted, the harness sends a final handoff
prompt with the remaining budget. No mid-turn injection is required —
only standard ACP primitives (prompt, cancel, prompt).

For `maxTurns`, the harness sets the runtime's native turn limit
(e.g. `GOOSE_MAX_TURNS`) to ~80–85% of the configured `maxTurns`.
When the runtime stops (having hit its native limit), the harness
checks the `stopReason` from the prompt response. If it indicates a
turn limit (not natural completion), the harness sends a second
`session/prompt`:

> You have reached your turn limit. Commit your current work and
> write a handoff to `.konveyor/handoff.md` documenting what you
> completed and what remains.

For `maxCost`, the harness monitors `cost.amount` from `usage_update`
notifications. When cumulative cost reaches ~80–85% of `maxCost`, the
harness sends `session/cancel` to stop the current prompt, then sends
the handoff prompt with the remaining budget.

This approach uses standard ACP primitives only (prompt, cancel,
prompt) and works on any ACP-compliant runtime. No dependency on
the goose-specific `_goose/unstable/session/steer` extension.

Note: for `maxTurns`, the handoff prompt starts a fresh native turn
cycle (the runtime resets its counter on a new prompt). The 15–20%
reserve is advisory — the harness does not enforce a hard cap on
the handoff prompt's turn usage. In practice the handoff prompt is
small (write a file, commit) and completes well within the reserve.

Note: the kubelet enforces a 4,096-byte limit on termination
messages. Harness authors must keep the JSON blob compact — usage
stats fit comfortably but free-text summaries will be truncated.

#### Exit codes

The harness exit code is part of the harness contract:

| Exit code | Meaning | Controller maps to |
|---|---|---|
| 0 | Succeeded — agent completed naturally | Phase: `Succeeded` |
| 1 | Failed — error during execution | Phase: `Failed` |
| 2 | Limit reached — budget exhausted, handoff committed | Phase: `Succeeded`, Condition: `type=LimitReached` with reason indicating which limit |

Additional exit codes may be defined in the future. Any non-zero exit
code stops a workflow — the next stage does not proceed. Configurable
continue-on-limit-reached behaviour is deferred.

#### Workflow sequencing on limit-reached

A stage that exits 2 (limit reached) stops the workflow. The handoff
is committed to the branch — a human can inspect it and decide
whether to re-run or continue manually. The default is conservative:
don't feed incomplete work to the next stage.

A future enhancement may add per-stage configuration to allow
continuing on limit-reached (e.g. `onLimitReached: continue`).

### Usage reporting

The harness writes a JSON blob to `/dev/termination-log` on exit.
The controller reads the pod's termination message and stores it as
opaque data on the AgentRun status:

```go
// TerminationData is the raw JSON from the pod's termination
// message. The controller does not interpret this — it is opaque
// data written by the harness for consumption by platform-specific
// UIs. The termination message schema is part of the harness
// contract.
// +optional
TerminationData *runtime.RawExtension `json:"terminationData,omitempty"`
```

The controller copies the bytes without interpretation. The Konveyor
harness writes usage data (turns, context used/size, cost); a
different harness writes different data. The Konveyor UI knows how
to read the Konveyor harness's schema. The controller is agnostic.

This requires the controller to read the Pod directly on completion —
Agent Sandbox does not surface `terminationMessage` in its CR status.
The controller gains Pod read RBAC for this purpose.

Note: the standard ACP `usage_update` does not provide per-turn
input/output token breakdowns (that RFD is still in draft).
The harness reports only what the ACP standard provides: context
window `used`/`size` and cumulative `cost`.

### Relationship to mode and the tee (ADR 0008)

Mode controls tool approval. The tee (ADR 0008) controls two
additional interactivity features that are independent of mode:

| Feature | Controlled by |
|---------|---------------|
| Tool approval (ask before running a tool) | `mode` field |
| Steer/cancel (human redirects or stops the run) | `HARNESS_HITL_STEER` env var on the tee |
| Elicitation (agent asks a free-form question) | Agent-initiated in any mode; tee relays or harness denies |

Setting `mode: approve` with no viewer attached means every tool call
is denied (tee fail-closed policy). This is correct — "this needs
supervision" with nobody watching should fail, not proceed
unsupervised.

## Alternatives considered

### Execution controls as arbitrary params

Put `max_turns`, `max_cost`, etc. in `Agent.Spec.Params` as regular
parameters. The harness would scan for well-known param names.

Rejected because: execution controls are universal, not domain-
specific. Every agent has turn limits. Encoding them as arbitrary
params means the controller can't validate them, the CRD can't
express their types, and harness implementors have to know magic param
names instead of reading a typed field.

### Skill-level iteration caps

Keep `MAX_FIX_ITERATIONS` in the verify skill (current state).

Rejected because: LLMs are unreliable at counting iterations. A Go
loop in the harness or a native runtime limit (`GOOSE_MAX_TURNS`) is
deterministic. See ADR 0010.

### Goose-specific mode values on the CRD

Use goose's mode names (`auto`, `approve`, `smart_approve`, `chat`)
directly on the CRD.

Rejected because: ties the CRD to a single runtime. `auto` and
`approve` are universal concepts (supervised vs unsupervised).
`smart_approve` is goose-specific and functionally identical to
`approve` for agents that primarily write files and run commands.
`chat` (no tool use) is not universally supported across runtimes.

## Consequences

- Agent gains `maxTurns` and `maxCost` fields. AgentRun gains `mode`.
  AgentWorkflow stages gain `maxTurns`, `maxCost`, and `mode`.
  `sessionType` is reserved for future use.
- The controller resolves effective values (stage overrides Agent
  defaults for limits) and writes them to the `execution` section
  of `params.json`.
- The harness gains ACP `usage_update` parsing for cost enforcement.
- The harness gains runtime translation for mode.
- The harness exit code (0, 1, 2) is part of the harness contract.
  The controller maps exit code 2 to `Succeeded` with a
  `LimitReached` condition.
- AgentRun status gains an opaque `terminationData` field populated
  from the pod's termination message. The controller does not
  interpret it — platform-specific UIs read it.
- The controller gains Pod read RBAC to read termination messages
  (Agent Sandbox does not surface them in its CR status).
- Any non-zero exit code stops a workflow. Configurable continue-on-
  limit-reached is deferred.
- The UI (Konveyor) decides which of these knobs to expose to which
  persona. An Architect/PM may set defaults on the Agent; a Developer
  may or may not be allowed to override them on a run. This is a UI
  concern, not a CRD concern.
- User-facing documentation must describe these fields, their
  resolution order, the exit code contract, and the termination
  message schema for third-party harness implementors.
