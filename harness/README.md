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

The opt-in [Lightspeed runtime prototype](../docs/lightspeed-runtime-prototype.md)
uses the same harness and ACP tee with `HARNESS_RUNTIME=lightspeed`. It requires
the companion runtime changes described there. Goose remains the default.
The prototype reports token usage and supports same-session handoff; it rejects
`maxCost` because the runtime does not yet report monetary cost.

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
| `HARNESS_VERIFY` | `on` | `off` skips the harness's own verification pass (the commands in `docs/plan.md`'s `## Verification` section, re-run on the final tree) |
| `HARNESS_VERIFY_ENFORCE` | `off` | `on` fails the stage (exit `1`) when verification comes back `failed` or `incomplete`. Default off: the result is recorded either way |
| `HARNESS_VERIFY_TIMEOUT_SECONDS` | `600` | Per-command timeout for the verification pass; clamped to 30–3600 |
| `HARNESS_VERIFY_TOTAL_TIMEOUT_SECONDS` | `1800` | Budget for the whole verification pass, not one command; clamped to 60–14400. Commands it leaves unrun are recorded as a gap, so the verdict is `incomplete` |

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
identities. Disabled proxies are ignored. Lookup and shared proxy validation
errors stop startup; an empty proxy list preserves direct access unless the run supplies
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
`HTTPS_PROXY` override the respective Hub proxy; a nonempty lowercase value
applies when uppercase is absent or empty. If a variable is supplied but both
case variants are empty, that scheme uses direct access. `NO_PROXY` (or
`no_proxy`, with the same nonempty-value precedence) replaces inherited
exclusions using Go's native matching, including CIDRs and host/port entries. An explicitly
empty bypass list removes inherited exclusions. Git always bypasses localhost
and loopback addresses, matching Go's native HTTP proxy behavior. With an
explicit proxy environment variable, only explicit `NO_PROXY` applies. This covers
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
surrounding whitespace is trimmed. A bare domain matches itself and its
subdomains; a leading-dot domain matches only subdomains. Matches bypass all
inherited proxies for both tools, including Maven's HTTP fallback. Git also supports
inherited CIDRs and port-specific exclusions. Maven cannot represent those forms;
automatic Maven proxy conversion then emits a warning and skips the entire
inherited Maven proxy block. Supplied Maven settings remain unchanged; without
a Maven credential, no proxy-only settings file is created.
Existing user settings, Secret mounts, and an explicit Hub Maven `<proxies>` section
remain authoritative and bypass Maven exclusion validation. URLs and malformed
exclusions still fail shared validation. Those errors name the inheritance
opt-out. Unsupported exclusions are never dropped from a partial Maven proxy
configuration: all automatic Maven proxy entries are skipped together.

Maven settings preparation runs for every invocation, including images without
Maven installed. Conversion failures do not stop the run or change Git proxy
selection. Maven uses its supplied/default configuration, which may attempt
direct access and fail later if a proxy is required. Supply explicit Maven proxy
settings when automatic conversion is unsupported. No toolchain detection is
required; wrappers and tools installed during execution use the same settings.
The warning appears in pod logs and as a console notice once the ACP tee is
available. It is also retained as `mavenProxyWarning` in
`AgentRun.status.terminationData`, including when setup fails before the console
connection is established.

Maven interpolates credential values containing `${` before authentication.
Automatic proxy conversion skips all inherited Maven proxy entries with a
sanitized warning for those identities, preserving supplied settings and
continuing the run. Supply an explicit Maven proxy section with encrypted
credentials if needed. Literal brace passwords are preserved;
the harness does not insert backslashes that would change their value.
Maven may attempt to decrypt literal `{...}` values and report a decryption
problem; Maven 3 retains the original value when decryption fails. Real builds
with Maven 3.9.11 cover both `{s3cret}` and `p{a}ss` passwords.

Hub proxy values are private to go-git's HTTP transport and generated Maven
settings; they are not exported to the runtime environment or written to Git
config. Maven can read its proxy credentials from its private settings file.
Proxy passwords, username-only tokens and encoded URL/XML forms are registered
with the harness redactor. Short usernames paired with a password are masked
inside complete URLs and userinfo, while their bare values retain the existing
eight-character minimum to preserve ordinary diagnostics. Proxy lookup errors
omit response bodies. Git push credentials stay
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

## Independent Verification

The handoff verdict is the stage's own account of its work. The harness also
measures it (#300): after the agent's turn and before the final push, it runs
the commands in the `## Verification` section of `docs/plan.md` itself and
records each exit code in the termination blob, independently of anything the
agent wrote.

**Which commit the result describes.** `git.Push` sends `refs/heads/<TARGET_BRANCH>`
whatever HEAD points at (#281), so the working tree is not necessarily the tree
that reaches the remote. Two checks, both made before any command runs, and a
pass records the verified SHA in `verification.commit`:

- **HEAD must be the branch tip.** An agent that commits onto the branch and
  then checks out a different commit is refused outright (`incomplete`, naming
  both commits): a different commit is not a partial measurement of this one.
- **The working tree must hold nothing the commit does not.** Tracked files
  edited but not committed, and untracked files git is not ignoring, are both
  counted. The untracked half is the one that is easy to miss: a command that
  passes in the working directory can be compiling a source file the agent
  created and never added, so a clean checkout of the pushed commit does not
  have it and does not build. Here the commands still run — a failure on a
  superset of the commit is a real failure, and the evidence is worth keeping —
  but a pass is downgraded to `incomplete` naming the uncommitted paths.

Ignored paths do not count, because git does not count them: go-git's `Status`
honours `.gitignore`, so a project's `node_modules`, `target` or `dist` does not
show up while a stray source file does. Two paths are exempt, by exact name,
because neither is an input to a build and so neither can change what a command
measures: `.konveyor/analysis.json`, which the harness writes into the workspace
itself, and `.konveyor/handoff.md`, which the agent appends to as its last act —
the verify skill makes that append mandatory, *after* the commit of the work,
and the handoff verdict is read straight from the worktree for the same reason.
Counting it would have every clean verify stage report `incomplete` over a file
no build compiles. Any other file under `.konveyor/` counts like anything else.

The plan skill specifies that section as `- Build: <command>`,
`- Test: <command>`, `- Blackbox: <steps>`. The harness parses those lines and
runs each **command** through `/bin/sh -c` in the clone directory, inheriting
the harness process environment — the same workspace, toolchain, `HOME` and
Maven settings the agent's own shell had, since the harness is the process that
started the runtime in this container. Hub credentials were already cleared from
that environment before the agent ran, so verification does not see them either.
Fenced code blocks are skipped before parsing, so a plan quoting the plan
template back cannot supply commands it never meant. That cuts both ways: a
real plan wrote its six commands *inside* a ```` ```bash ```` block with prose
around them, and the parser saw none of them (#336). The harness still does not
read fenced blocks — it cannot tell a plan's own commands from a quoted
template's `<build command>` placeholder — but a section that names no command
outside one is reported as `incomplete`, naming the block, rather than as
"nothing to measure". The commands have to be list items for the harness to run
them.

A fence **nested under a prose line** is the exception: the plan skill asks for
Blackbox as steps to run by hand, and a model that illustrates one with a
snippet has still written a manual-only section. The indentation says so,
exactly as it does for a nested item, so that block is not reported and such a
section stays `notRun` instead of failing a stage for containing a code block.

What is **not** run, recorded line by line with a reason rather than dropped:

- **Blackbox** (and `Manual`, `Notes`, `Steps`) — prose steps for a person,
  with no single exit code to record. The harness does not guess a command from
  them.
- Anything **nested under** one of those lines. A model asked for "steps to
  start the app and verify key business flows still work" often writes them as
  sub-bullets with labels of their own — `- Start the app: mvn quarkus:dev` —
  and read on its own that is a command the harness would start and wait out,
  for a plan whose every scorable command passed. The indentation is the only
  thing that says those lines belong to the prose above them. A sibling back at
  the parent's indentation closes the block, and so does any heading — a
  sub-bullet under a later `### Automated checks` is scored, not skipped.
- An unfilled template placeholder (`<build command, …>`), an explicit `none` /
  `n/a` / `-`, or a value too long to be a command.
- A value that **reads as English** rather than as a command: one whose first
  word is a skip word followed by the plan's reasoning for it, or one carrying a
  sentence boundary (a letter, a full stop, a capitalised next word or the end
  of the line). `**Runtime verification**: skipped — this migration changes only
  the query library API surface, not application behavior. The type system
  enforces correctness.` is 132 runes, well under the length guard, and before
  #336 it went to `/bin/sh -c`, which recorded `/bin/sh: skipped: command not
  found` as that tree's verification result. A command is a program and its
  arguments; it does not end a word with a full stop, which is why `go test
  ./...`, `find . -name x` and `. ./scripts/env.sh && npm test` are still run.

  The two ways this heuristic can be wrong are **not symmetric**, and it is
  tuned accordingly. Prose misread as a command runs, exits non-zero and
  verdicts the stage `failed` — bogus evidence, loudly. A command misread as
  prose is skipped, and a section whose other commands pass then verdicts
  `passed` over a check that never ran: unearned green, the failure mode this
  file exists to remove. So anything carrying a mark of shell is a command — a
  shell operator (`&&`, `||`, `$(`) anywhere in the value, and punctuation
  inside a quoted argument, which is a string being passed to a program rather
  than sentence structure. `test -f required-artifact && echo 'Build passed.'`
  is a real check and runs.

**A pass must be measured, never assumed.** `verification.status` is one of
four values, and the difference between the last two is the point:

| Status | Means |
|---|---|
| `passed` | Every command the plan named ran on the pushed commit and exited 0 |
| `failed` | A command exited non-zero, timed out, or could not be started |
| `notRun` | There was **nothing to measure**: no `docs/plan.md`, no `## Verification` section, a section the harness read that names nothing scorable (`Blackbox` only, an explicit `none`, an unfilled placeholder), `HARNESS_VERIFY=off`, or a stage that never claimed success |
| `incomplete` | There **was** something to measure and the harness could not finish it, could not read it, or could not attribute the result to the pushed commit: the section's commands were inside a fenced block, the run was cancelled part way, the step cap or the pass budget left commands unrun, HEAD was not the branch tip, or the working tree held paths the commit does not |

The line between the last two is what the harness *understood*, not what it
ran. A section that was read and had nothing scorable in it is `notRun`: the
plan skill specifies `Blackbox` as manual steps with no exit code, and a plan
that did as it was asked must not fail for it. A section whose content never
reached the parser — commands sitting in a dropped fence — is `incomplete`:
there was something to measure and the harness cannot say what it would have
measured.

Every non-`passed` status carries a `reason`, and the field is never simply
absent while the harness reached the end of a turn. `incomplete` is not a pass:
a command the harness never reached — because the run was cancelled under it,
or because the step cap dropped it — leaves a verdict that says so, rather than
a `passed` covering work nobody checked. A measured failure outranks a gap,
since it is the actionable finding.

Bounds: each command gets `HARNESS_VERIFY_TIMEOUT_SECONDS` (default 10 min) and
is killed as a process group, so a hung build does not survive its own timeout;
the pass as a whole gets `HARNESS_VERIFY_TOTAL_TIMEOUT_SECONDS` (default 30
min), because twelve commands at the per-command timeout would be two hours
spent after the agent's turn with the final push and the termination blob
behind them. Output is captured as a bounded 64 KiB tail, the end of it logged
to the pod log, and carried into the termination blob only for a command that
did not pass, as a short excerpt. At most 12 Verification lines are run.

Every one of those bounds the work, not the honesty about it: a command the
step cap dropped, or one the budget left unrun, is a gap, and a gap verdicts
`incomplete`. A pass budget that runs out under a command is recorded as that
gap too, never as the command timing out — the harness running out of its own
clock is not a measurement of the tree.

By default a non-zero exit **does not** change the stage's phase — it is
recorded, and surfacing it on `AgentRun.status` is #302.
`HARNESS_VERIFY_ENFORCE=on` makes `failed` *or* `incomplete` fail the stage
(exit `1`): under enforcement the stage must be verified, and a partial
measurement is not verification. `notRun` never fails the stage — a run with no
plan must not fail for having nothing to check.

Enforcement applies on the **last** stage of a workflow, and on a standalone
run. `docs/plan.md` describes the tree after the migration, so an intermediate
stage is not expected to satisfy it yet: the execute stage hands a
half-migrated tree on by design, and the verify skill's own first step is a
build fix loop. Failing an intermediate stage on the plan's verification would
stop the run (ADR 0018) before the stage whose job is to fix exactly that ever
started. The result is measured and recorded on every stage either way.

```json
"verification": {
  "status": "failed",
  "reason": "Build failed: npm run tsc -w client",
  "commit": "4f2a1c9e7b30",
  "commands": [
    { "label": "Build", "command": "npm run tsc -w client", "status": "failed", "exitCode": 2, "seconds": 41.2, "output": "…45 errors" },
    { "label": "Test",  "command": "npm run test -w client", "status": "passed", "exitCode": 0, "seconds": 93.8 },
    { "label": "Blackbox", "status": "skipped", "reason": "manual steps, not a command the harness can run or score" }
  ]
}
```

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
├── verify.go      The harness's own verdict: docs/plan.md's Verification commands, re-run
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
