# ADR 0004: Verified client contract and layered transports for AgentRun UIs

- **Status:** proposed
- **Date:** 2026-07-06
- **Relates to:** ADR 0002/0003 (extension-first client, Hub-later; see README),
  [konveyor/enhancements#295](https://github.com/konveyor/enhancements/pull/295),
  konveyor/agentic-controller PR #4

## Context

Enhancement konveyor/enhancements#295 defines a multi-UI agent platform for
Konveyor: the "UI creates AgentRun via Hub passthrough proxy", and the
"external interface [is] identical regardless of runtime". Multiple UIs
(editor-extensions, tackle2-ui, RHDH) are expected to drive the same AgentRun
lifecycle and the same ACP chat surface.

The Hub passthrough proxy is a **later phase** — it does not exist yet. What
does exist, as of 2026-07-06, is the real konveyor/agentic-controller
reconciler (PR #4) running live on minikube with Agent Sandbox v0.5.0, and
two working clients (the VSCode extension transport and this repo's POC
client) exercised end-to-end against it: create AgentRun → wait for Running →
resolve pod + secret → tunnel → ACP session with streaming updates and
permission round-trips.

That exercise turned assumptions into **verified facts**. This ADR freezes
those facts as the client contract, and decides how client code is layered so
the same core serves IDE (node) clients today and browser UIs (through the
future Hub proxy) without rework.

## Decision

### (a) The verified client contract (normative)

Every client of the agentic-controller MUST conform to the following, all
verified against the real controller (PR #4) on a live cluster:

- **AgentRun.status** carries: `phase` (`Pending` | `Running` | `Succeeded` |
  `Failed`), `sandboxName`, `secretKeyRef.name`, `startTime`,
  `completionTime`, `duration`, and `conditions`.
- **Pod resolution is by name, never by string-munging.** The sandbox pod
  name equals `status.sandboxName` EXACTLY. The real controller sets
  `sandboxName == run name` (no suffix); the retired dev simulator used
  `<run>-sandbox`. Clients MUST read `status.sandboxName` and MUST NOT derive
  pod names from the run name.
- **ACP key Secret:** named `<sandboxName>-acp-key`, reached via
  `status.secretKeyRef.name`. The data key is `secret-key` (real controller)
  or `ACP_SECRET_KEY` (legacy simulator). Clients MUST try those keys in that
  order and MAY fall back to the sole entry if the Secret has exactly one key.
- **ACP server:** pod port `4000`, path `/acp`, speaking WebSocket and
  streamable HTTP, authenticated with the `X-Secret-Key` header. `/healthz`
  returns `ok` unauthenticated and is the liveness probe clients may use.
- **Pod labels:** the pod carries ONLY `agents.x-k8s.io/sandbox-name-hash` —
  there is NO `konveyor.io/agentrun` label on the pod. Label-based pod
  discovery is broken by design in the current controller; resolve by name
  (see Consequences for the prepared upstream patch).
- **Service:** the auto-created Service is HEADLESS (`clusterIP: None`, no
  ports). Clients MUST port-forward / dial the POD, not the Service.
- **Injected env** (contract between controller and harness images):
  `GOOSE_SERVER__SECRET_KEY`, `KONVEYOR_PARAM_<NAME>`, `KONVEYOR_PROMPT`,
  `KONVEYOR_INSTRUCTIONS`.
- **AgentRun spec is IMMUTABLE after create** (a whole-spec CEL rule).
  Clients MUST delete + recreate to change anything; PATCHing spec will be
  rejected by the apiserver.
- **Permission diff preview needs no protocol extension.** ACP already
  defines it: `session/request_permission`'s `toolCall` is a
  `ToolCallUpdate` whose `content[]` accepts `{type:"diff", path,
  oldText, newText}` (`oldText: null` = new file). Agents SHOULD attach
  diff blocks to file-modifying permission asks; clients SHOULD render
  them before the approve/reject choice. Verified end-to-end (mock
  harness → hub-shim WS proxy → browser ChatPanel, 2026-07-07).

### (b) Layered client: isomorphic core, pluggable transports

Client code is split so the protocol knowledge lives once, in browser-safe
code, and only the transport differs per environment:

- **Core (`@konveyor/agentic-client`)** — isomorphic (no node builtins, no
  `ws`, no `@kubernetes/client-node`): the contract types + helpers
  (`resolveSecretKeyFromData`, `waitForRunning`, `isTerminalPhase`) and the
  `AcpSession` class (initialize, session/new, session/load, prompt
  streaming, permission requests, cancel) over a plain WebSocket.
- **Direct-k8s transport** (node / IDE dev): talks to the apiserver with a
  kubeconfig, port-forwards to the pod, injects `X-Secret-Key` via a
  node WebSocket factory.
- **Hub-proxy transport** (browsers): the same `RunApi` interface over plain
  HTTP + a plain WebSocket — no headers, no kube credentials; the proxy owns
  endpoint resolution, tunneling, and secret injection server-side.

The local **hub-shim** implements the proxy side today, and its HTTP surface
— **SHIM HTTP API v1** — is the reference shape the future Konveyor Hub
passthrough proxy is expected to expose:

| Method | Route | Behavior |
|--------|-------|----------|
| GET | `/healthz` | 200 `ok` |
| GET | `/api/applications` | 200 `Application[]` — the platform's application inventory (mocked in the shim; Hub serves its real records). Source of truth for resolved params/credentials, see ADR 0005. |
| GET | `/api/agents` | 200 `AgentResource[]` (full CRs, metadata+spec), **filtered to `konveyor.io/managed=true`** |
| GET | `/api/agents/:name` | 200 `AgentResource` \| 404 (never label-filtered) |
| GET | `/api/llmproviders[/:name]` | 200 `LLMProvider[]` \| `LLMProvider` \| 404 |
| GET | `/api/skillcards[/:name]` | 200 `SkillCard[]` \| `SkillCard` \| 404 |
| GET | `/api/skillcollections[/:name]` | 200 `SkillCollection[]` \| `SkillCollection` \| 404 |
| GET | `/api/agentruns` | 200 `AgentRun[]` (full CRs) |
| POST | `/api/agentruns` (body `{agentRef, params?: Record<string,string>, instructions?, applicationRef?}`) | 201 `AgentRun` (generateName `ui-`, params mapped to `[{name,value}]`). When `applicationRef` is set, the platform resolves the Agent's declared param/credential sources from that application (ADR 0005): resolved params merge under caller-supplied ones, credentials become `spec.envFrom`. 400 on unknown `applicationRef`, or a required param with a recognized source the application cannot supply. |
| GET | `/api/agentruns/:name` | 200 `AgentRun` \| 404 |
| DELETE | `/api/agentruns/:name` | 204 |
| WS | `/api/agentruns/:name/acp` | Resolves the run's ACP endpoint (waitForAcpEndpoint semantics, 60s), opens a port-forward tunnel to the pod, dials `ws://127.0.0.1:<tunnel>/acp` upstream WITH `X-Secret-Key` (key read from the run's Secret), then pipes frames bidirectionally. Client close → close upstream + tunnel; upstream close/error → close client 1011 with reason. |

The shim itself is unauthenticated (localhost dev tool) and serves
`Access-Control-Allow-Origin: *` on `/api/*` (plus OPTIONS preflight). The
real Hub proxy adds its own authn/z in front of the same shape.

### (c) Spec immutability ⇒ delete + recreate semantics

Because the AgentRun spec is immutable, every client "edit"/"retry" affordance
is defined as **delete the run, create a new one** (owner references
garbage-collect the Sandbox/Secret/pod). UIs MUST NOT offer in-place spec
mutation; run identity is per-attempt, and history is preserved by listing
past runs, not by mutating one.

## Consequences

- **Deduplication:** editor-extensions, tackle2-ui, and RHDH can all consume
  the same core package; only the thin transport differs. Protocol fixes land
  once.
- **The shim doubles as the Hub-proxy spec:** when the Hub passthrough proxy
  is built, SHIM HTTP API v1 is its acceptance contract — browser UIs written
  against `ShimClient` should work against Hub by swapping the base URL.
- **Known cosmetic gap (harness):** the mock harness accepts the legacy
  `AGENT_PROMPT` env var as a fallback for `KONVEYOR_PROMPT`
  (`clients/harness-mock/server.mjs`). Harmless; remove once nothing legacy remains.
- **Known gap (pod labels):** because Agent Sandbox v0.5.0 copies only
  PodTemplate labels onto the pod, the pod is not selectable by
  `konveyor.io/agentrun`. A controller fix mirroring the labels into the
  Sandbox PodTemplate is proposed separately; until it merges, name-based
  resolution remains the only correct discovery mechanism — which is why
  (a) mandates it (and why it stays mandated: name-based resolution keeps
  working either way).
- **Risk:** the contract is verified against the controller at `main`
  (post-#4 merge), not a tagged release. If status fields or the secret
  data key are renamed before release, this ADR and
  `clients/packages/agentic-client/src/contract` are the two places to
  update.
