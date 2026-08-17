# Agent REST API — working spec

Living document — **not** an ADR. The endpoint shapes below evolve with the
Hub implementation (#72); the architectural decisions behind them are frozen
in ADR 0012 (layered transports) and ADR 0013 (param sources). Seeded from
SHIM HTTP API v1, the surface the prototype hub-shim serves today and the
reference shape the Hub passthrough endpoints replace: browser UIs written
against it are expected to keep working when Hub takes over, modulo the path
prefix and the operations noted below.

Path mapping: the shim serves `/api/<resource>`; Hub serves
`/hub/agent/<resource>` (route table in #72). Rows below use the shim paths.

| Method | Route | Behavior |
|--------|-------|----------|
| GET | `/healthz` | 200 `ok` |
| GET | `/api/applications` | 200 `Application[]` — the platform's application inventory. Production is Hub reading its own Application table; the shim reads Hub over REST and falls back to a stub offline. |
| GET | `/api/agents` | 200 `AgentResource[]` (full CRs, metadata+spec), **filtered to `konveyor.io/managed=true`** |
| GET | `/api/agents/:name` | 200 `AgentResource` \| 404 (never label-filtered) |
| GET | `/api/gateways[/:name]` | 200 `Gateway[]` \| `Gateway` \| 404 |
| GET | `/api/skillcards[/:name]` | 200 `SkillCard[]` \| `SkillCard` \| 404 |
| GET | `/api/skillcollections[/:name]` | 200 `SkillCollection[]` \| `SkillCollection` \| 404 |
| GET | `/api/agentruns[?application=<hub id>]` | 200 `AgentRun[]` (full CRs). `application` filters by the `konveyor.io/application` label (ADR 0006) — a `client.List()` label selector, never a fetch-and-scan. Runs predating the label are not selected. 400 on a non-numeric id or on any resource that cannot honour the filter — never a silent unfiltered list. |
| POST | `/api/agentruns` (body `{agentRef, params?: Record<string,string>, instructions?, applicationRef?, targetBranch?, gateway?}`) | 201 `AgentRun` (generateName `ui-`, params mapped to `[{name,value}]`). When `applicationRef` is set the run carries the Hub coordinates (`HUB_BASE_URL`, `APP_ID`, target branch) and the `konveyor.io/application` label; the harness resolves application data from Hub at runtime (ADR 0006). 400 on unknown `applicationRef`. |
| GET | `/api/agentruns/:name` | 200 `AgentRun` \| 404 |
| GET | `/api/agentworkflows[/:name]` | 200 `AgentWorkflow[]` \| `AgentWorkflow` \| 404 (list **filtered to `konveyor.io/managed=true`**) |
| GET | `/api/agentworkflowruns[?application=<hub id>]` | 200 `AgentWorkflowRun[]`. Same `application` semantics as `/api/agentruns`; composes with the managed filter as one selector. Stage runs do not carry the parent's application label yet (#107). |
| GET | `/api/agentworkflowruns/:name` | 200 `AgentWorkflowRun` \| 404 |
| POST | `/api/agentworkflowruns` (body `{workflowRef, params?, applicationRef?, targetBranch?, gateway?}`) | 201 `AgentWorkflowRun` (generateName `ui-`), labelled `konveyor.io/managed` plus `konveyor.io/application` when scoped. |
| WS | `/api/agentruns/:name/acp` | Resolves the run's ACP endpoint (`status.sandboxName` pod, key from `status.secretKeyRef`, 60s), dials the pod's `:4000/acp` WITH `X-Secret-Key`, then pipes frames bidirectionally. Client close → close upstream; upstream **normal close → forward the close code and reason**; upstream **error → close client 1011** with reason. |

## Run lifecycle: cancel, not delete

The platform surface cancels runs and never deletes them (enhancement §UI;
ADR 0006): cancel revokes the Hub token and sets `spec.cancel`; completed
runs age out via per-condition TTL pruning. Because the AgentRun spec is
immutable, "edit"/"retry" is always *create a new run* — the old run stays
listed until pruned, which is what keeps run history real.

The prototype shim predates this and still exposes
`DELETE /api/agentruns/:name` (204) as a dev convenience; Hub's surface is
`Cancel`, and the shim converges as #72 lands.

## Auth

The shim is unauthenticated (localhost dev tool) and serves
`Access-Control-Allow-Origin: *` on `/api/*` plus OPTIONS preflight. Hub
puts its own authn/z in front of the same shape; the ACP secret key never
reaches the browser in either topology.
