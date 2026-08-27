# Agent Entry Point (`migration-harness`)

**Minimal single-stage entry point (bookends only)** that handles git plumbing, parameter delivery, and [goose](https://github.com/block/goose) runtime lifecycle for AI agent workloads. It is the reference implementation of the [Agent Entry Point Contract](../docs/entry-point.md).

The entry point manages workspace setup and teardown only — all migration intelligence, tool use, and git commits belong to the agent runtime and its [SkillCards](../CONTEXT.md).

---

## How It Works

```
┌──────────────────────────────────────────────────────────┐
│                  migration-harness run                   │
│                                                          │
│  Setup (Bookend 1):                                      │
│  1. Load config from env & /run/konveyor/params.json     │
│  2. Resolve app + git creds from Hub API (if configured) │
│  3. Clone repo, configure author, strip push creds       │
│  4. Write analysis insights to .konveyor/ (uncommitted)  │
│  5. Start goose serve (ACP loopback :4001)               │
│  6. Discover skills and assemble layered prompt          │
│  7. Start ACP tee (:4000) for live streaming & HITL      │
│  8. Start filesystem watcher (incremental push)          │
│                                                          │
│  Execution:                                              │
│  9. Send ACP prompt — agent executes and commits locally │
│                                                          │
│  Teardown (Bookend 2):                                   │
│ 10. Wind-down handoff prompt if budget limit reached     │
│ 11. Final push of agent-authored commits                 │
│ 12. Write usage & outcome to /dev/termination-log        │
│ 13. Revoke Hub token and exit (0=ok, 1=err, 2=limit)     │
└──────────────────────────────────────────────────────────┘
```

The entry point sends **one prompt** per stage. The AgentWorkflowRun controller handles stage sequencing — the entry point binary is identical in every stage image.

---

## Prerequisites

- **Go 1.21+** (to build)
- **[goose](https://github.com/block/goose)** (started by the entry point via `goose serve`)
- **git**

---

## Build

```bash
cd harness
go build -o migration-harness ./cmd/migration-harness/
```

---

## Configuration

Configuration comes from two sources: environment variables (required and optional) and the controller-mounted `/run/konveyor/params.json` file (execution controls and workflow/agent parameter values — see the Parameters section below).

### Required

| Variable | Description |
|----------|-------------|
| `KONVEYOR_LLM_MODEL` | LLM model name (fallback: `KONVEYOR_MODEL_PRIMARY_MODEL`) |
| `HUB_BASE_URL` | Konveyor Hub API base URL |
| `APP_ID` | Application ID in Hub |
| `KONVEYOR_ACP_SECRET_KEY` | Secret key for ACP WebSocket auth |
| `TARGET_BRANCH` | Git branch to push results to (must differ from source) |

### Optional

| Variable | Default | Description |
|----------|---------|-------------|
| `KONVEYOR_LLM_PROVIDER` | — | LLM provider, e.g. `anthropic`, `openai` (fallback: `KONVEYOR_MODEL_PRIMARY_PROVIDER`) |
| `KONVEYOR_LLM_ENDPOINT` | — | Custom LLM endpoint URL (fallback: `KONVEYOR_MODEL_PRIMARY_ENDPOINT`) |
| `KONVEYOR_LLM_API_KEY` | — | LLM API key (fallback: `KONVEYOR_MODEL_PRIMARY_API_KEY`) |
| `HUB_TOKEN` | — | Hub authentication token |
| `HARNESS_WORK_DIR` | `/workspace/repo` | Clone directory |
| `HARNESS_SKILLS_DIR` | `/opt/skills` | Skills mount directory |
| `HARNESS_PARAMS_FILE` | `/run/konveyor/params.json` | Path to the controller-written parameter file (ADR 0009) |
| `HARNESS_TERMINATION_LOG_PATH` | `/dev/termination-log` | Path for the termination log JSON blob |
| `KONVEYOR_PROMPT` | — | Agent-level standing instructions |
| `KONVEYOR_WORKFLOW_GUIDE` | — | Workflow guide context |
| `KONVEYOR_INSTRUCTIONS` | — | Stage-specific task instructions |
| `HARNESS_ACP_TEE` | `on` | `off` disables the ACP tee; goose then owns :4000 directly |
| `HARNESS_HITL_STEER` | `on` | `off` makes the run stream watch-only: viewer steer/cancel frames for the run session are refused instead of relayed |
| `HARNESS_HITL_TIMEOUT_SECONDS` | `180` | How long a permission ask or an `ask_user` question waits for an attached viewer; values above 600 are clamped to 600 |
| `HARNESS_HITL_ASK` | `on` | `off` leaves the `ask_user` tool out of the session (the agent then has no way to block on a human answer) |

### Parameters (`/run/konveyor/params.json`)

Execution limits and mode, plus workflow/agent parameter values, are delivered via a three-section JSON file mounted by the controller (ADR 0009):

```json
{
  "workflow": { "application_name": "coolstore" },
  "agent": { "source_url": "https://github.com/example/app", "dry_run": true },
  "execution": { "mode": "auto", "maxTurns": 200, "maxCost": "10.00" }
}
```

- `execution.mode` sets runtime supervision (`auto` or `approve`).
- `execution.maxTurns` configures runtime turn limits.
- `execution.maxCost` sets cumulative spend budget monitored via ACP.
- Workflow and agent values are appended to the agent's prompt under `## Parameters`.

---

## Git Lifecycle & Commit Authorship

1. **Clone** — entry point clones using Hub-provided credentials
2. **Configure author** — configures git commit identity (`user.name`, `user.email`)
3. **Strip credentials** — strips push credentials from the remote URL before launching the runtime
4. **Clear env** — Hub token is cleared from the process environment
5. **Checkout branch** — checks out `TARGET_BRANCH`
6. **Agent commits** — the agent authors all commits locally with descriptive messages. The entry point **never creates commits of its own**.
7. **Watcher** — background fsnotify watcher pushes agent commits after a 30s quiet period
8. **Final push** — pushes agent commits on stage completion. If no new commits were created, push is skipped to avoid empty remote branches.

---

## Exit Code Contract (ADR 0011)

| Exit Code | Meaning | Controller Status |
|-----------|---------|-------------------|
| `0` | Succeeded — agent completed work | `Phase: Succeeded` |
| `1` | Failed — execution error or crash | `Phase: Failed` |
| `2` | Limit reached — budget exhausted, handoff committed | `Phase: Succeeded`, `type=LimitReached` |

---

## Skill Discovery

The entry point globs `/opt/skills/*/SKILL.md` at startup. Skills are mounted into agent pods by the controller via SkillCard init containers. The entry point concatenates all discovered skills and always-loaded rules into the prompt alongside environment-provided context layers.

The entry point requires **no specific skills** — it discovers and loads whatever is mounted. The plan/execute/verify stage-skill bundle is a convention for workflow runs, not a requirement; a single standalone operation skill works the same way (see `docs/entry-point.md`).

---

## Architecture

```
cmd/migration-harness/
├── main.go        CLI entry point (cobra, single "run" command)
└── outcome.go     Outcome classification & termination log
internal/
├── config/        Env-var & params.json configuration
├── acp/           ACP WebSocket client (session, prompt, cost monitoring)
├── goose/         goose serve lifecycle (start, health, stop)
├── tee/           Pod-facing ACP endpoint: pipe, live tee, HITL relay
├── askuser/       ask_user stdio MCP server for in-turn human elicitation
├── hub/           Konveyor Hub API client (app, creds, analysis)
├── git/           Credential-isolated git operations (go-git)
├── watcher/       Debounced filesystem watcher (fsnotify)
└── logging/       Colored terminal output
```

### The ACP tee: live run status and human redirection

goose gives every WebSocket connection a private agent with no
cross-connection fan-out, so a client dialing the pod could never see the
run's live session. The entry point therefore owns the pod ACP port and
fronts goose:

```text
viewer ──(hub WS proxy)──▶ pod:4000 = entry point tee ──▶ 127.0.0.1:4001 = goose serve
                                       ▲
                        entry point's own run connection (session, prompt)
```

**Watching the sandboxed run.** Attached viewers receive the run
session's stream in standard ACP vocabulary, unmodified:

- goose's own `session/update` notifications — `agent_message_chunk`,
  `agent_thought_chunk`, `tool_call` / `tool_call_update` (with file
  locations), `session_info_update` (which carries the active run id in
  `_meta.goose.activeRunId`) — plus its `_goose/unstable/session/update`
  channel (`usage_update` token/context spend, `status_message`
  notices), which the entry point enables by declaring the
  `customNotifications` client capability at initialize.
- Entry point lifecycle the goose stream cannot see, emitted as synthetic
  frames on the run's sessionId with the same vocabulary: a `plan`
  ladder (prepare workspace → agent works the stage → push results),
  `tool_call` / `tool_call_update` for watcher and final git pushes, and
  a closing `status_message` with the stage outcome. A small replay ring
  catches late-attaching viewers up on status (goose history is
  replayable via `session/load` as usual).
- Each attached client also gets a verbatim frame pipe to its own goose
  connection — interactive chat is unchanged.

**Redirecting the run (in-turn HITL).** goose scopes an active prompt to
the connection that started it, so the tee routes viewer frames naming
the run session onto the entry point's run connection instead of the
viewer's private pipe:

- `_goose/unstable/session/steer` — inject operator guidance into the
  active turn. goose queues it, drains it at the next loop iteration as
  a real user message (`user_message_chunk` with `_meta.goose.steer`),
  and a steer landing while the model is finishing keeps the turn alive.
  The viewer supplies `expectedRunId` from the teed
  `session_info_update`, and goose's response relays back under the
  viewer's own request id.
- `session/cancel` — stop the turn; treated as a deliberate human abort
  (stage fails, partial work still pushed).
- A viewer `session/prompt` on the run session is rejected while the run
  is active — two connections prompting one session would interleave its
  history — with goose's own guidance text pointing at steer. After the
  run it passes through (goose lazily activates the session for post-run
  chat).
- `HARNESS_HITL_STEER=off` refuses steer/cancel and keeps the stream
  watch-only.

**Permission asks.** `session/request_permission` asks from the run are
offered to attached viewers (`kperm-*` ids, first answer wins, relayed
verbatim). Everything else fails closed: nobody attached denies
immediately, and an ask no viewer answers within
`HARNESS_HITL_TIMEOUT_SECONDS` denies too.

**Questions from the agent (`ask_user`).** The session mounts a stdio
MCP server that is the entry point binary itself (`migration-harness
ask-user-mcp`), giving the agent one tool: `ask_user(question, options?)`.
A call becomes an MCP `elicitation/create` that goose relays over ACP to
the entry point, offering it to attached viewers (`kask-*` ids, first answer
wins). The tool call blocks the turn for the whole round trip.

---

## License

Apache-2.0
