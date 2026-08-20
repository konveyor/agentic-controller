# migration-harness

**Thin single-stage runner** that handles git plumbing and [goose](https://github.com/block/goose) lifecycle for AI-powered code migrations. The harness does not know what stage it is running — migration intelligence lives in [SkillCards](../CONTEXT.md).

---

## How It Works

```
┌──────────────────────────────────────────────────────┐
│                  migration-harness run                │
│                                                      │
│  1. Load config from env                             │
│  2. Resolve app + git creds from Hub API             │
│  3. Clone repo, strip creds, checkout target branch  │
│  4. Write analysis insights to .konveyor/            │
│  5. Start goose serve (ACP)                          │
│  6. Discover skills from /opt/skills/*/SKILL.md      │
│  7. Build prompt from context layers                 │
│  8. Start filesystem watcher (incremental push)      │
│  9. Send single ACP prompt (blocks until completion) │
│ 10. Final push                                       │
└──────────────────────────────────────────────────────┘
```

The harness sends **one prompt** per stage. The AgentWorkflowRun controller handles stage sequencing — the harness is identical in every stage image.

---

## Prerequisites

- **Go 1.21+** (to build)
- **[goose](https://github.com/block/goose)** (started by the harness via `goose serve`)
- **git**

---

## Build

```bash
cd harness
go build -o migration-harness ./cmd/migration-harness/
```

---

## Configuration

All configuration is via environment variables — there is no config file or `init` command.

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
| `KONVEYOR_PARAM_MAX_TURNS` | `200` | Max tool-call turns before terminating |
| `HARNESS_WORK_DIR` | `/workspace/repo` | Clone directory |
| `HARNESS_SKILLS_DIR` | `/opt/skills` | Skills mount directory |
| `KONVEYOR_PROMPT` | — | Agent-level standing instructions |
| `KONVEYOR_WORKFLOW_GUIDE` | — | Workflow guide context |
| `KONVEYOR_INSTRUCTIONS` | — | Stage-specific task instructions |
| `HARNESS_ACP_TEE` | `on` | `off` disables the ACP tee; goose then owns :4000 directly |
| `HARNESS_HITL_STEER` | `on` | `off` makes the run stream watch-only: viewer steer/cancel frames for the run session are refused instead of relayed |
| `HARNESS_HITL_TIMEOUT_SECONDS` | `180` | How long a permission ask or an `ask_user` question waits for an attached viewer; values above 600 are clamped to 600 |
| `HARNESS_HITL_ASK` | `on` | `off` leaves the `ask_user` tool out of the session (the agent then has no way to block on a human answer) |

---

## Git Lifecycle

1. **Clone** — harness clones using Hub-provided credentials
2. **Strip credentials** — strips any embedded credentials from the remote URL (safety net — auth is passed via transport, not URL)
3. **Clear env** — Hub token is cleared from the process environment
4. **Checkout branch** — checks out `TARGET_BRANCH`
5. **Agent commits** — the agent commits locally with descriptive messages (per skill instructions)
6. **Watcher** — background fsnotify watcher pushes agent commits after a 30s quiet period
7. **Final push** — catches anything the watcher missed (runs even on failure)

The agent commits locally but never sees push credentials — only the harness binary pushes.

---

## Skill Discovery

The harness globs `/opt/skills/*/SKILL.md` at startup. Skills are mounted into agent pods by the controller via SkillCard init containers. The harness concatenates all discovered skills into the prompt alongside environment-provided context layers.

Two kinds of skills:

- **Stage skills** (plan, execute, verify) — define *process*: what to do
- **Domain skills** (e.g. javaee-to-quarkus) — define *knowledge*: how to do it

---

## Architecture

```
cmd/migration-harness/main.go    CLI entry point (cobra, single "run" command)
internal/
├── config/        Env-var configuration
├── acp/           ACP WebSocket client (session, prompt)
├── goose/         goose serve lifecycle (start, health, stop)
├── tee/           Pod-facing ACP endpoint: pipe, live tee, HITL relay
├── hub/           Konveyor Hub API client (app, creds, analysis)
├── git/           Credential-isolated git operations (go-git)
├── watcher/       Debounced filesystem watcher (fsnotify)
└── logging/       Colored terminal output
```

### The ACP tee: live run status and human redirection

goose gives every WebSocket connection a private agent with no
cross-connection fan-out, so a client dialing the pod could never see the
run's live session. The harness therefore owns the pod ACP port and
fronts goose:

```text
viewer ──(hub WS proxy)──▶ pod:4000 = harness tee ──▶ 127.0.0.1:4001 = goose serve
                                       ▲
                        harness's own run connection (session, prompt)
```

**Watching the sandboxed run.** Attached viewers receive the run
session's stream in standard ACP vocabulary, unmodified:

- goose's own `session/update` notifications — `agent_message_chunk`,
  `agent_thought_chunk`, `tool_call` / `tool_call_update` (with file
  locations), `session_info_update` (which carries the active run id in
  `_meta.goose.activeRunId`) — plus its `_goose/unstable/session/update`
  channel (`usage_update` token/context spend, `status_message`
  notices), which the harness enables by declaring the
  `customNotifications` client capability at initialize.
- harness lifecycle the goose stream cannot see, emitted as synthetic
  frames on the run's sessionId with the same vocabulary: a `plan`
  ladder (prepare workspace → agent works the stage → push results),
  `tool_call` / `tool_call_update` for watcher and final git pushes, and
  a closing `status_message` with the stage outcome. A small replay ring
  catches late-attaching viewers up on harness status (goose history is
  replayable via `session/load` as usual).
- Each attached client also gets a verbatim frame pipe to its own goose
  connection — interactive chat is unchanged.

**Redirecting the run (in-turn HITL).** goose scopes an active prompt to
the connection that started it, so the tee routes viewer frames naming
the run session onto the harness's run connection instead of the
viewer's private pipe:

- `_goose/unstable/session/steer` — inject operator guidance into the
  active turn. goose queues it, drains it at the next loop iteration as
  a real user message (`user_message_chunk` with `_meta.goose.steer`),
  and a steer landing while the model is finishing keeps the turn alive.
  The viewer supplies `expectedRunId` from the teed
  `session_info_update`, and goose's response relays back under the
  viewer's own request id.
- `session/cancel` — stop the turn; the harness treats a cancelled stop
  reason as a deliberate human abort (stage fails, partial work still
  pushed).
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
`HARNESS_HITL_TIMEOUT_SECONDS` denies too — an ask that self-approves on
a timer is no ask at all. After a timeout the viewers are considered
unresponsive and follow-up asks deny fast until a new attach or any
viewer interaction shows a human is back, which caps the deny/retry turn
burn.

**Questions from the agent (`ask_user`).** The session mounts a stdio
MCP server that is the harness binary itself (`migration-harness
ask-user-mcp`), giving the agent one tool: `ask_user(question, options?)`.
A call becomes an MCP `elicitation/create` that goose relays over ACP to
the harness (the harness advertises `clientCapabilities.elicitation.form`
at initialize), the harness offers it to attached viewers exactly like a
permission ask (`kask-*` ids, first answer wins, a viewer attaching
mid-question is shown the pending ask), and the `{action, content}` answer
travels back to the tool, which tells the model "The human answered: …".
The tool call blocks the turn for the whole round trip — this is the
in-turn "stop and confirm" that prose questions cannot express (an
assistant message ending on a question is just a finished turn). Fail
closed as everywhere else: nobody attached, or no answer within
`HARNESS_HITL_TIMEOUT_SECONDS`, cancels the question — the tool then tells
the model no human answered, and the prompt guidelines tell it to say what
it needed and stop rather than guess. `HARNESS_HITL_ASK=off` leaves the
tool out; elicitation from any other MCP server the session mounts takes
the same path. The design decisions behind this flow — and what a future
agent runtime must provide to compose with it — are recorded in
[ADR 0017](../docs/adr/0017-ask-user-tool-and-elicitation.md).

**Fault containment.** The tee can never fail the run: bounded
per-viewer queues (slow viewers are dropped), ping/pong keepalive so
half-open viewers release their goose connection, panics recovered per
connection, and a listener failure only costs live viewing — plus, since
the controller's pod readiness probe targets `:4000`, a run whose
listener never binds keeps its `ACPReady` condition False (reason
`NotListening`) for its whole life. `/healthz` stays unauthenticated. `HARNESS_ACP_TEE=off` restores goose owning
`:4000` directly.

### Key design decisions

- **Single `run` command** — no subcommands, no interactive mode. One prompt, one stage.
- **go-git** — all git operations use `github.com/go-git/go-git/v5`. No shell-out to git CLI.
- **Credential isolation** — Hub and git push credentials are used by the harness only, cleared before goose starts. The agent commits locally; the harness pushes.
- **ACP WebSocket** — connects to goose via JSON-RPC over WebSocket (ACP protocol).
- **Exit status from ACP** — clean `SendPrompt` return = exit 0. Any error or goose crash = exit 1.

---

## License

Apache-2.0
