# clients/ — reference client stack

Working client-side implementation of this repository's AgentRun/ACP
contract, developed against the live controller on minikube and verified
end-to-end with both a deterministic mock agent and a real goose+Bedrock
agent. It exists so the Hub, UI, and base-image streams can build against
running code instead of a whiteboard:

- the **hub-shim** HTTP surface is the proposed contract for the Hub
  passthrough proxy (stream 2, issue #21) — see ADR 0004
- the **UI** and **client core** are the reference for the tackle2-ui
  client layer (stream 3, issues #22–#24)
- the **goose harness** is a working reference for the base-image harness
  (stream 4, issues #25/#26)

## Layout

| Path | What |
|------|------|
| `packages/agentic-client/` | Isomorphic (browser + node) client core: CRD contract types + helpers, `AcpSession` over WebSocket (JSON-RPC 2.0), hub-shim HTTP transport. Zero runtime dependencies. |
| `packages/agentrun-client/` | Node-side client: create/watch AgentRuns against the apiserver, resolve the ACP endpoint (`status.sandboxName` → pod, `status.secretKeyRef` → key), port-forward tunnel. Shaped for konveyor/editor-extensions. |
| `packages/hub-shim/` | Localhost gateway serving the SHIM HTTP API v1 (REST + WS `/acp` proxy with server-side `X-Secret-Key` injection). Stands in for the future Hub passthrough proxy. |
| `ui/` | Browser SPA (PatternFly): run list, create form with platform-resolved params (ADR 0005), streaming chat with permission (HITL) round-trips. |
| `harness-mock/` | Deterministic ACP agent (no LLM): real protocol via the ACP SDK's server side, scripted behaviors (`TEST_PERMISSION`, `TEST_CANCEL`, `TEST_DROP`). Test fixture and demo agent. |
| `harness-goose/` | Real agent base: goose `serve` on `:4000` behind an entrypoint that adapts the controller's `KONVEYOR_*` env contract (clones the `repository` param, maps model env, writes prompt hints). |
| `deploy/` | In-cluster deployment of gateway + UI (interim, pre-Hub). |
| `manifests/` | Sample Agent/LLMProvider CRs: `samples.yaml` (mock) and `goose-bedrock.yaml` (real goose on AWS Bedrock). |
| `hack/` | `demo-up.sh` / `demo-check.sh` / `demo-down.sh` — converge the cluster, smoke the full ACP round-trip, tear down. |
| `docs/DEMO.md` | Narrated end-to-end demo runbook (browser + IDE + real agent). |

Architecture write-ups live with the repo's ADRs: `docs/adr/0004`
(verified client contract and layered transports) and `docs/adr/0005`
(platform-resolved params).

## Quickstart

Prereqs: minikube running, Agent Sandbox v0.5.0 installed, the controller
deployed from the repo root (`make docker-build deploy IMG=agentic-controller:dev`),
Node ≥ 22.9.

```sh
cd clients
hack/demo-up.sh     # converge cluster + start shim & UI, prints URLs
hack/demo-check.sh  # smoke: full ACP round-trip on a throwaway mock run
hack/demo-down.sh   # stop local processes; cluster untouched
```

Then follow `docs/DEMO.md`.

## Status: interim by design

The controller and CRDs are the real ones from this repository. The rest
is scaffolding with a named replacement:

- `hub-shim` → the real Hub passthrough proxy (its route table is the
  handover spec)
- `ui` → absorbed into tackle2-ui
- `harness-goose` → the stream-4 base image
- `harness-mock` → stays, as a deterministic test fixture

Every piece encodes contract facts verified against the live controller
(pod name == `status.sandboxName`, ACP Secret data key `secret-key`,
headless portless Service, whole-spec immutability) — see ADR 0004 for
the full list and rationale.
