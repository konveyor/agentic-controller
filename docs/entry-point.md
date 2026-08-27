# Agent Entry Point Contract

An **entry point** is the container entrypoint binary in an Agent image. It acts as a minimal, domain-agnostic wrapper managing **bookends only**: setup before the agent runtime runs, and teardown after it exits. All intelligence, tool operations, and git commits belong to the agent runtime and its skills.

The controller is agnostic to the entry point implementation. Any third-party entry point that satisfies this contract can be used.

### Workflow vs. Standalone Runs

The entry point is agnostic to how many prompts it will ever send — it sends exactly one per invocation. Two usage patterns follow from that:

- **Workflow run** — Driven by an `AgentWorkflowRun`, one stage at a time (e.g. plan, execute, verify). Each stage is a separate entry point invocation with its own prompt; state carries across stages via git commits.
- **Standalone run** — A single `AgentRun` with no workflow: one skill, one prompt, one operation (fetch application details → run the operation → commit/push). This is the same code path as a single workflow stage; nothing in the entry point special-cases it. §5 references credential revocation "on standalone run" — this is what that means.

Neither mode requires a specific bundle of skills (see `harness/README.md` § Skill Discovery); the plan/execute/verify convention is a skill-authoring choice, not an entry point requirement.

---

## 1. Parameter Delivery (`/run/konveyor/params.json`)

The controller delivers resolved parameters to `/run/konveyor/params.json` (ADR 0009):

```json
{
  "workflow": {
    "application_name": "coolstore"
  },
  "agent": {
    "source_url": "https://github.com/example/app",
    "dry_run": true
  },
  "execution": {
    "mode": "auto",
    "maxTurns": 200,
    "maxCost": "10.00"
  }
}
```

- **`workflow`** — Workflow-level parameters (`AgentWorkflowRun`).
- **`agent`** — Agent-level parameters (`AgentRun`), type-coerced (strings, numbers, booleans).
- **`execution`** — First-class execution controls (ADR 0011): `mode` (`"auto"` | `"approve"`), `maxTurns` (integer), `maxCost` (string USD).

---

## 2. Execution Semantics

- **Supervision Mode** — `auto` (default) permits tool calls automatically; `approve` requires human approval via the ACP tee.
- **Limit Enforcement** — `maxTurns` is enforced natively by the runtime; `maxCost` is monitored via ACP `usage_update.cost.amount`.
- **Wind-down & Handoff** — The entry point reserves 15–20% of the turn/cost budget. When the primary limit is reached, it cancels the active turn and sends a final prompt instructing the agent to commit current work and write a handoff to `.konveyor/handoff.md`.

---

## 3. Git Lifecycle & Credential Isolation

- **Clone & Author Identity** — Clones the repository and configures commit author (`user.name`, `user.email`).
- **Credential Isolation** — Push credentials remain in the entry point and are stripped from the working tree remote before launching the runtime.
- **Agent-Authored Commits** — The agent creates all commits locally. The entry point **never creates commits of its own**.
- **Push on Exit** — A debounced watcher pushes agent commits incrementally; a final push runs on stage exit. If no new commits were authored (`HEAD == baseSHA`), push is skipped to avoid creating empty branches.

---

## 4. Observability & Supervision (ACP Tee)

- **Port Ownership** — The entry point binds the external ACP port (`:4000`) and proxies to the runtime on loopback (`:4001`) (ADR 0008).
- **Live Stream** — Relays session updates (`agent_message_chunk`, `tool_call`, `usage_update`) to attached viewers.
- **Interactivity** — Forwards viewer steer/cancel requests, human tool approvals (`mode: approve`), and `ask_user` tool questions (ADR 0017).

---

## 5. Exit Codes & Termination Log Contract

### Exit Codes (ADR 0011)

| Exit Code | Meaning | Controller Phase / Condition |
|-----------|---------|------------------------------|
| `0` | Succeeded — agent completed work | `Phase: Succeeded` |
| `1` | Failed — execution error or fatal crash | `Phase: Failed` |
| `2` | Limit reached — budget exhausted, handoff committed | `Phase: Succeeded`, `type=LimitReached` |

### Termination Log (`/dev/termination-log`)

On exit, the entry point writes a JSON blob to `/dev/termination-log` (copied to `AgentRunStatus.terminationData`):

```json
{
  "exitCode": 0,
  "outcome": "succeeded",
  "limitReached": "",
  "stopReason": "end_turn",
  "usage": {
    "turns": 42,
    "contextUsed": 53000,
    "contextSize": 200000,
    "cost": { "amount": 0.045, "currency": "USD" }
  }
}
```

Token credentials (e.g. Hub API token) are revoked on last stage, standalone run, or failure.
