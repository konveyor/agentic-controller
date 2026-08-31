---
adr: "0012"
title: "Verified client contract and layered transports for AgentRun UIs"
description: "Defines the client contract and layered transports that browser UIs use to drive and observe AgentRuns."
status: proposed
date: "2026-07-06"
last_updated: null
authors: []
last_reviewed: "2026-08-31"
implementation_status: in-sync
review_note: "Remains a proposed client contract based on the external prototype; no contradictory client implementation is maintained in this repository."
relates_to:
  - "ADR 0002 (ACP transport)"
  - "ADR 0006 (Hub addon pattern, supersedes 0003)"
  - "konveyor/enhancements#295"
  - "PR #4"
provenance: "Written and verified in the hub-shim prototype (ibolton336/agentcontroller-client), which stands in for the Hub passthrough proxy so browser UIs can drive the real controller today. Contributed here because the contract it freezes is the platform's, not the prototype's."
---

# ADR 0012: Verified client contract and layered transports for AgentRun UIs

## Context

Enhancement konveyor/enhancements#295 defines a multi-UI agent platform for
Konveyor: the "UI creates AgentRun via Hub passthrough proxy", and the
"external interface [is] identical regardless of runtime". Multiple UIs
(editor-extensions, tackle2-ui, RHDH) are expected to drive the same AgentRun
lifecycle and the same ACP chat surface.

The Hub passthrough proxy is a **later phase** — it does not exist yet. What
does exist, as of 2026-07-06, is the real konveyor/agentic-controller
reconciler (PR #4) running live on minikube with Agent Sandbox v0.5.0, and
two working clients (the VSCode extension transport and the prototype
repo's POC client) exercised end-to-end against it: create AgentRun → wait for Running →
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
- **Params ride the CR, not the pod.** Clients set `spec.params` (and
  `spec.instructions`); how the controller delivers them into the sandbox
  (`KONVEYOR_PARAM_*` env vars at verification time; `params.json` per
  ADR 0009) is invisible to clients and MUST NOT be depended on.
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

The local **hub-shim** implements the proxy side today. Its HTTP surface —
SHIM HTTP API v1 — is the reference shape the Hub passthrough endpoints
replace, and lives as a working spec in
[docs/agent-api-spec.md](../agent-api-spec.md), coordinated with the Hub
implementation (#72). The decision this ADR freezes is the layering above
and that the spec is verified against the live controller before Hub
reimplements it — not the endpoint shapes themselves, which evolve with
#72.

### (c) Spec immutability ⇒ new-run semantics (cancel, never delete)

Because the AgentRun spec is immutable, every client "edit"/"retry"
affordance creates a NEW run — UIs MUST NOT offer in-place spec mutation;
run identity is per-attempt. The platform surface cancels in-flight runs
(token revocation + `spec.cancel`, ADR 0006) and never deletes them:
completed runs age out via per-condition TTL pruning, which is what keeps
"history is the run list" true. The prototype shim's DELETE route predates
this and is a dev convenience, not part of the platform contract.

## Consequences

- **Deduplication:** editor-extensions, tackle2-ui, and RHDH can all consume
  the same core package; only the thin transport differs. Protocol fixes land
  once.
- **The shim doubles as the Hub-proxy spec:** when the Hub passthrough proxy
  is built, SHIM HTTP API v1 is its acceptance contract — browser UIs written
  against `ShimClient` should work against Hub by swapping the base URL.
- **Known cosmetic gap (harness):** the mock harness accepts the legacy
  `AGENT_PROMPT` env var as a fallback for `KONVEYOR_PROMPT`
  (`harness-mock/server.mjs`). Harmless; remove once nothing legacy remains.
- **Known gap (pod labels):** because Agent Sandbox v0.5.0 copies only
  PodTemplate labels onto the pod, the pod is not selectable by
  `konveyor.io/agentrun`. A prepared (NOT submitted — human decision) patch
  adds the labels to the Sandbox PodTemplate upstream:
  `hack/upstream-patches/0001-add-agentrun-labels-to-pod-template.patch`
  in the prototype repo.
  Until/unless it merges, name-based resolution remains the only correct
  discovery mechanism — which is why (a) mandates it.
- **Risk:** the contract is verified against PR #4, not a tagged release. If
  upstream renames status fields or the secret data key before release, this
  ADR and `packages/agentic-client/src/contract` are the two places to
  update.
