---
adr: "0006"
title: "Hub follows the addon pattern for agent resources"
description: "Defines Hub as a fire-and-forget CRUD and runtime data service that follows the existing addon integration boundary."
status: proposed
status_note: "Supersedes ADR 0003; Hub-specific integration remains external to this repository."
date: "2026-07-23"
last_updated: "2026-08-31"
authors:
  - "David Zager"
last_reviewed: "2026-08-31"
implementation_status: amended
review_note: "Remains proposed/external to this repository. The Hub addon pattern is still the intended integration boundary; the entry point now receives parameters through params.json, as recorded in the ADR amendment."
---

# ADR 0006: Hub follows the addon pattern for agent resources

Hub's integration with the agent platform follows the established addon
pattern rather than introducing smart resolution endpoints. Hub creates
AgentRun/AgentWorkflowRun CRs with `HUB_BASE_URL`, `APP_ID`, and a
scoped API token injected as env/envFrom — then walks away
(fire-and-forget). The harness resolves application metadata from Hub at
runtime, the same way the addon adapter does for addon tasks today.

**Update (2026-08-31):** The entry point no longer has a standalone
`KONVEYOR_PARAM_*` parameter-delivery path. Resolved workflow and Agent
parameters are delivered through `/run/konveyor/params.json` under the
`workflow` and `agent` sections (ADR 0009); environment variables remain the
carrier for runtime credentials and execution metadata. Hub integration is
outside this repository, so the Hub-specific portions of this ADR remain
proposed.

## Context

ADR 0003 proposed a smart AgentRun create endpoint where Hub eagerly
resolves application metadata (git URLs, branches, credential Secrets)
before building the full CR. Review with the Hub team revealed this
breaks the established pattern: in the addon/task flow, Hub is a store
and runtime data service — the thing-in-the-pod resolves what it needs
at runtime. Introducing eager resolution for agent resources creates an
inconsistent pattern.

Additionally, the Task envelope (creating a Hub Task row to carry the
application reference) was considered and rejected — the Task concept
represents addon execution requests with an addon image, a pod, and a
Hub-managed lifecycle. Agent runs have no addon, and the controller (not
Hub) creates the pod. The application ID goes directly on the CR as an
env var.

## Decision

### Hub creates CRs with connectivity info, nothing more

When Hub receives a create request for an AgentRun or AgentWorkflowRun:

1. Mints a scoped API token with `AddonScopes`
2. Stores the token and its database ID in a Kubernetes Secret
   (`HUB_TOKEN`, `HUB_TOKEN_ID`)
3. Adds `HUB_BASE_URL`, `APP_ID`, and the token Secret to the CR's
   `spec.env` and `spec.envFrom`
4. Stamps the application id as a label
   (`konveyor.io/application: "<app id>"`) when the request carries one
5. Creates the CR via `client.Create()`

The label duplicates `APP_ID` deliberately: the env var is for the
harness inside the pod, the label is what makes runs queryable by
application through the API. Application ids are uint64
(`hub.ParseAppID`), so they are always valid label values.

Hub does not resolve application metadata, git URLs, or credentials at
create time. Hub is fire-and-forget.

### CRUD endpoints under `/hub/agent/`

Hub exposes REST endpoints for all agent CRDs using controller-runtime
client, following the `AddonHandler`/`ConfigMapHandler` pattern. CRDs in
etcd are the sole source of truth — no database copies. All reads are
request-driven (`client.Get()` on demand), no informers.

Agents and AgentWorkflows are filtered by `konveyor.io/managed=true`.
AgentRun and AgentWorkflowRun lists accept `?application=<app id>`,
served as a label selector on `konveyor.io/application`. Runs created
before the label existed are not matched. An invalid or unsupported
`application` filter is an error, never a silent unfiltered list. Stage
AgentRuns do not carry the label yet (#107). Other resource types are
listed unfiltered.

### Harness resolves at runtime

The harness acts as a Hub client (analogous to the addon adapter). In
managed mode (`HUB_BASE_URL` + `APP_ID` set), it calls Hub's existing
REST API to resolve the application's git URL, branch, and decrypted
credentials. In standalone mode, it reads from `KONVEYOR_PARAM_*` env
vars and mounted Secrets.

### Cancel, not delete

The UI cancels runs (never deletes). Hub revokes the token and sets
`spec.cancel: true` on the CR. The controller handles cancellation
based on the current phase:

- **Running**: delete the Sandbox, set phase to `Cancelled`
- **Pending** (queued behind `maxConcurrentRuns`): no Sandbox exists;
  set phase to `Cancelled` immediately, preventing Sandbox creation

Token and Secret cleanup is idempotent in both cases.

### Stage-aware token revocation

For standalone AgentRuns, the harness self-revokes its Hub API token
on exit (success or failure). For AgentWorkflowRun stages, all stages
share a single Hub token. The controller injects stage metadata
(`KONVEYOR_WORKFLOW_STAGE`, `KONVEYOR_WORKFLOW_STAGE_COUNT`) into each
child AgentRun. The harness revokes the token only on the last stage;
earlier stages skip revocation. Token TTL expiry is the safety net
for crashes or mid-workflow failures.

### ACP secret key stays separate

The controller generates a random ACP secret key per run (stored in a
Secret, referenced by `status.secretKeyRef`). Hub reads it when proxying
WebSocket connections. The Hub API token and ACP key protect different
trust boundaries and remain distinct credentials.

### Run pruning

The controller prunes completed runs using per-terminal-condition TTLs
(`ttlSecondsAfterSucceeded`, `ttlSecondsAfterFailed`,
`ttlSecondsAfterCancelled`). Owner references cascade-delete Secrets and
Sandboxes.

### Scaling protection

`ResourceQuota` for hard protection. Configurable `maxConcurrentRuns` on
the controller — excess runs stay `Pending` until a slot opens.

### Reusable controller interfaces

The controller exposes validation and conversion logic as importable Go
packages so Hub can reuse them (param validation, model selection
validation, CRD type converters).

## Considered Options

**Smart Hub endpoint (ADR 0003):** Hub eagerly resolves application
metadata at create time. Rejected — breaks the established addon
pattern.

**Task envelope:** Hub creates a Task row to carry the application
reference. Rejected — the Task concept doesn't fit (no addon, no
Hub-managed pod lifecycle). The application ID goes directly on the CR.

**Hub adapter in the controller:** The controller calls Hub to resolve
application metadata. Rejected — couples the controller to Hub's API,
violating domain-agnostic design.

## Consequences

- Hub's agent handlers are thin CRUD — no resolution logic beyond token
  minting.
- The harness must be a Hub API client, increasing its complexity. The
  addon adapter (`shared/addon/adapter`) is a reference implementation.
- Sandbox pods in managed mode require network egress to Hub (configured
  via SandboxTemplate network policy and/or OpenShell).
- Non-Konveyor use cases work without Hub — the controller and harness
  are self-contained when `HUB_BASE_URL` is absent.
- ADR 0003's CRUD handler structure survives (Hub still exposes REST
  endpoints backed by controller-runtime), but the smart AgentRun create
  endpoint is removed.
