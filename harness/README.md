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
| `HARNESS_HITL_ASK` | — | The `ask_user` tool is opt-in, normally via the run's `spec.execution.askUser` (params.json `execution.askUser`). `on` mounts it for a harness run outside the controller; `off` leaves it out even when the run asked for it |
| `HARNESS_GIT_WRITE_CHECK` | `on` | `off` skips the pre-flight that proves the resolved credential can push to the source repository before the stage runs; only for runs that deliberately never push |

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

## Maven Settings

Before launching the agent runtime, the harness prepares Maven user settings
at `$HOME/.m2/settings.xml` (`/home/harness/.m2/settings.xml` in the agent
images). An existing settings file, including one mounted from a Secret or
ConfigMap, takes precedence and is left unchanged. Otherwise, the harness
retrieves the application's assigned Maven credential from Hub, falling back
to the default Maven credential if none is assigned. A broken settings symlink
stops setup before any credential lookup and is left unchanged.

The harness creates the file with mode `0600` outside the repository. It logs
the file's location, never its contents. If neither a Maven credential nor an
enabled inherited proxy applies, Maven keeps its normal defaults.
Credential lookup and file setup failures stop the run
with an error; an assigned credential with empty settings is also an error.
If a write fails, the harness removes the incomplete settings file. Failure to
remove it is reported alongside the write or close error.

Before launching the runtime, the harness registers passwords and passphrases
from the effective settings file with its log redactor, including existing and
mounted settings. Both literal XML and decoded values are masked in agent text
logged by the harness. Handoff failure details are redacted before truncation
and before they reach logs, viewer notices, or termination status.
Unreadable settings or XML parsing errors stop setup;
parse errors omit XML details to avoid exposing credential contents.

This setup runs for standalone AgentRuns and separately in every workflow
stage's fresh Sandbox, independently of the selected skills. The settings
file is not carried between stages through Git. Hub API credentials remain
in the harness and are cleared from the agent runtime's environment.

---

## Hub Proxy Inheritance

Every standalone AgentRun and every fresh workflow stage resolves enabled Hub
HTTP/HTTPS proxies and decrypted identities before cloning or launching the
runtime. The Hub token needs read access to `/proxies` and referenced decrypted
identities. Disabled proxies are ignored. Lookup and configuration errors stop
startup; an empty proxy list preserves direct access unless the run supplies
its own Git proxy environment.

Set `HARNESS_HUB_PROXY=off` to skip automatic Hub proxy lookup and injection for
both Git and Maven. Supplied environment variables and Maven settings remain
effective; the existing Hub Maven-credential lookup still runs. Supply the
switch in either AgentRun or AgentWorkflowRun:

```yaml
spec:
  env:
    - name: HARNESS_HUB_PROXY
      value: "off"
```

Git resolves precedence independently for HTTP and HTTPS. `HTTP_PROXY` and
`HTTPS_PROXY` override the respective Hub proxy; lowercase spellings apply
when uppercase is absent. A supplied empty value selects direct access for
that scheme. `NO_PROXY` (or `no_proxy`) replaces inherited exclusions using
Go's native matching, including CIDRs and host/port entries. An explicitly
empty bypass list removes inherited exclusions. With an explicit proxy
environment variable, only explicit `NO_PROXY` applies. This covers
clone/fetch, push-access discovery, watcher pushes and the final push.
SSH, `git://` and local repositories retain their existing behavior.

Maven precedence uses settings files without translating Git proxy environment
variables or changing Maven launch arguments:

1. Existing `$HOME/.m2/settings.xml` files, including Secret or ConfigMap mounts,
   are complete overrides and remain unchanged, even without proxies.
2. Otherwise, the assigned/default Hub Maven credential is loaded. An explicit
   `<proxies>` section wins, even when empty or inactive.
3. Without that section, enabled Hub proxies are added while preserving the
   supplied XML. Without a Maven credential, proxy-only settings are created.

Generated settings use the normal Maven user-settings path outside Git, with
mode `0600`. Native settings selection (`-s`, wrapper or project configuration)
remains available. Maven retains its native fallback from HTTPS destinations
to an enabled HTTP proxy when no HTTPS proxy applies.

Inherited exclusions form one shared bypass list from all enabled Hub proxies.
They support case-insensitive hostnames, IP addresses and `*` wildcards;
surrounding whitespace is trimmed. Matches bypass all inherited proxies for
both tools, including Maven's HTTP fallback. CIDRs, URLs and port-specific
exclusions in Hub configuration fail startup with an error naming the opt-out.
Explicit Git `NO_PROXY` retains its native syntax independently.

Hub proxy values are private to go-git's HTTP transport and generated Maven
settings; they are not exported to the runtime environment or written to Git
config. Maven can read its proxy credentials from its private settings file.
Proxy passwords and encoded URL/XML forms are registered with the harness
redactor. Proxy lookup errors omit response bodies. Git push credentials stay
in the harness. Hub connectivity, Gateway verification and skill loading
retain their existing proxy behavior.

### Verification

Run `go test -race ./...` from `harness/`. Regressions exercise real Git
clone/fetch and pushes through authenticated HTTP and HTTPS CONNECT proxies,
direct access, exclusions, and startup for standalone and workflow runs.
The optional real Maven compile uses a fresh repository, locally served cached
plugins, an authenticated proxy-only repository and excluded HTTP/HTTPS hosts:

```bash
cd harness
HARNESS_PROXY_E2E=on go test -race ./cmd/migration-harness -run '^TestMavenProxyBuild$' -v
```

It requires Maven, a JDK with `keytool`, and cached `maven-resources-plugin:3.3.1`
and `maven-compiler-plugin:3.13.0` with their dependencies. The fixture reads
`$HOME/.m2/repository` without modifying it; set
`HARNESS_MAVEN_FIXTURE_REPOSITORY` to use another cache. No external repository
is used. Downloads are serial to avoid the authentication-cache race in older
Maven Resolver versions
([upstream issue](https://github.com/apache/maven-resolver/issues/1768)); production
concurrency is unchanged. For that upstream bug, use
`-Dmaven.artifact.threads=1` with Maven or upgrade Maven.

Validation against the deployed MTA 8.3 Hub, its scoped run token and the shipped
agent image remains a release/QE check.

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
9. **Handoff verdict** — reads the `- Status:` line of the last section of `.konveyor/handoff.md` when the stage wrote or changed it. `failed` means the stage did not do its work and the run exits `3` (refused), whatever the agent's turn looked like. Nothing else in the handoff is read.

---

## Exit Code Contract (ADR 0011, ADR 0018)

| Exit Code | Meaning | Controller Status |
|-----------|---------|-------------------|
| `0` | Succeeded — agent completed work | `Phase: Succeeded` |
| `1` | Failed — execution error or crash | `Phase: Failed` |
| `2` | Limit reached — budget exhausted, handoff committed | `Phase: Failed`, `Succeeded=False`, `reason=LimitReached` |
| `3` | Refused — the stage's own handoff records `Status: failed` | `Phase: Failed`, `Succeeded=False`, `reason=Refused` |

Any non-zero exit stops a workflow.

---

## Skill Discovery

The controller stages sources under `/opt/skills-src` and the `skill-loader`
init container assembles the validated skill tree at `/opt/skills`. Before
starting the runtime, the entry point links that tree into
`~/.agents/skills`, which lets the runtime load on-demand skills natively.
The entry point injects only always-loaded rules and its execution context
into the prompt (ADR 0014).

The entry point requires **no specific skills** — it discovers and loads whatever is mounted. The plan/execute/verify stage-skill bundle is a convention for workflow runs, not a requirement; a single standalone operation skill works the same way (see `docs/entry-point.md`).

---

## Architecture

```
cmd/migration-harness/
├── main.go        CLI entry point (cobra, single "run" command)
├── handoff.go     The stage's own verdict from .konveyor/handoff.md
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

**Questions from the agent (`ask_user`).** Opt-in, because an unanswered
question fails the run: only when the run sets `spec.execution.askUser`
(or `HARNESS_HITL_ASK=on`), the session mounts a stdio
MCP server that is the entry point binary itself (`migration-harness
ask-user-mcp`), giving the agent one tool: `ask_user(question, options?)`.
A call becomes an MCP `elicitation/create` that goose relays over ACP to
the entry point, offering it to attached viewers (`kask-*` ids, first answer
wins). The tool call blocks the turn for the whole round trip.

---

## License

Apache-2.0
