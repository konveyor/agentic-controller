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
- **Handoff Verdict** — After the turn, the entry point reads the `- Status:` line of the last `## ` section of `.konveyor/handoff.md`, if the stage wrote or changed that file. Fenced code blocks are skipped, so a plan excerpt a stage quotes back cannot open a section or supply a verdict it never wrote. `failed` means the stage did not do its work: the run exits `3` (refused) even though the agent ended its turn normally. `completed` / `passed` are the values the catalog skills write for a stage that did; anything else is recorded (`handoffStatus`) but not acted on. Nothing else in the handoff is read — its body is knowledge for the next stage, not execution control.
- **Independent Verification** — The handoff verdict is a self-report. After the turn and before the final push, the entry point also runs the commands in the `## Verification` section of `docs/plan.md` itself (`- Build:` / `- Test:`, via `/bin/sh -c` in the clone directory with the entry point's own environment) and records each exit code under `verification` in the termination blob, independent of what the handoff claims (#300). The result names the commit it describes: the tip of `TARGET_BRANCH`, which is what `Push` sends whatever HEAD points at (#281), confirmed before any command runs to be what the working tree holds. `Blackbox` and other prose lines, unfilled template placeholders and explicit `none` values are recorded as `skipped` with a reason. `status` is `passed`, `failed`, `notRun` (there was nothing to measure: no plan, no or command-less `## Verification` section, a disabled pass, a stage that never claimed success) or `incomplete` (there was, and the harness could not finish: the run was cancelled part way, the step cap left commands unrun, or the tree was not the commit being pushed). Every non-`passed` status carries a reason, and the field is never absent — an unmeasured or partly measured run says so rather than reading as green. Each command is bounded by a per-command timeout (`HARNESS_VERIFY_TIMEOUT_SECONDS`, default 600s) and killed as a process group; captured output is tailed; at most 12 lines are run. By default the result is recorded without changing the stage's phase; `HARNESS_VERIFY_ENFORCE=on` makes `failed` or `incomplete` fail the stage (exit `1`), while `notRun` never does.

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
| `2` | Limit reached — budget exhausted, handoff committed | `Phase: Failed`, `Succeeded=False`, `reason=LimitReached` |
| `3` | Refused — the stage's own handoff records `Status: failed` | `Phase: Failed`, `Succeeded=False`, `reason=Refused` |

Any non-zero exit stops a workflow: the next stage does not run.

### Termination Log (`/dev/termination-log`)

On exit, the entry point writes a JSON blob to `/dev/termination-log` (copied to `AgentRunStatus.terminationData`):

```json
{
  "exitCode": 0,
  "outcome": "succeeded",
  "limitReached": "",
  "handoffStatus": "completed",
  "stopReason": "end_turn",
  "usage": {
    "turns": 42,
    "contextUsed": 53000,
    "contextSize": 200000,
    "cost": { "amount": 0.045, "currency": "USD" }
  },
  "verification": {
    "status": "failed",
    "reason": "Build failed: npm run tsc -w client",
    "commit": "4f2a1c9e7b30",
    "commands": [
      { "label": "Build", "command": "npm run tsc -w client", "status": "failed", "exitCode": 2, "seconds": 41.2, "output": "…45 errors" },
      { "label": "Test", "command": "npm run test -w client", "status": "passed", "exitCode": 0, "seconds": 93.8 },
      { "label": "Blackbox", "status": "skipped", "reason": "manual steps, not a command the harness can run or score" }
    ]
  }
}
```

`handoffStatus` is what the stage said about its work; `verification` is what the entry point measured. The two disagreeing — `"handoffStatus": "passed"` next to `"verification": {"status": "failed"}` — is the case the field exists for. `verification.status` is one of `passed`, `failed`, `notRun` or `incomplete`, and is never absent when the entry point reached the end of a turn: an unmeasured or partly measured run says so, with a reason. `verification.commit` is the commit the result describes.

Token credentials (e.g. Hub API token) are revoked on last stage, standalone run, or failure.
