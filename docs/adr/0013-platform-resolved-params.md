---
adr: "0013"
title: "Platform-resolved agent params (param sources)"
description: "Defines a platform-resolved parameter vocabulary while leaving parameter sources and values opaque to the controller."
status: proposed
status_note: "Prototype verified in this repository."
date: "2026-07-08"
last_updated: "2026-08-31"
authors: []
last_reviewed: "2026-08-31"
implementation_status: deferred
review_note: "Remains a proposed platform-resolved-parameter contract based on the external prototype; the current controller remains parameter-source agnostic."
relates_to:
  - "ADR 0012 (client contract)"
  - "konveyor/agentic-controller#22/#24"
  - "the APB plans/parameter-metadata precedent"
provenance: "Written and verified in the hub-shim prototype (ibolton336/agentcontroller-client). Contributed here because the vocabulary it defines is the platform's, and #22 cites it."
---

# ADR 0013: Platform-resolved agent params (param sources)

**Update (2026-08-31):** The platform remains source-agnostic in this
repository: the controller passes caller-supplied values through and does
not resolve application sources. Where this ADR’s prototype discussion says
“env vars today,” the current parameter carrier is `params.json` (ADR 0009).

## Context

An Agent declares typed params (`name`, `type`, `description`, `required`,
`default`) — already enough for a UI to render a form, and our create modal
does. What nothing declares is **where a value should come from**: when a
user runs `migration-analyzer` against an application Konveyor already
knows, the repo URL, branch, and git credentials should come from the
application record, not from the user retyping them. Hub needs to know
which params it must fill, the UI needs to know which fields not to show,
and neither should hard-code per-agent knowledge.

A proposed upstream shape adds a `source` field to `AgentParam` validated
by a kubebuilder enum (`application.repository.url`, …). The layering is
right; the enum is the problem: it bakes one consumer's (Hub's) domain
vocabulary into the generic platform CRD, its own controller ignores the
field, and every new source value becomes a CRD schema upgrade — with the
skew failing closed (an older CRD rejects newer Agent manifests at
admission).

## Decision

### (a) Managed-agent label

Agents that Konveyor UIs know how to drive carry
`konveyor.io/managed: "true"`. Platform agent lists filter on it
(`GET /api/agents` in SHIM API v1 does); unlabeled Agents remain usable by
other consumers and invisible to Konveyor UIs.

**Runs carry a second platform label: `konveyor.io/application: "<hub id>"`**,
written at create time on both AgentRun and AgentWorkflowRun whenever the
caller supplies an application, so per-application run views are a label
selector instead of a scan. Same reasoning as the managed label — a
namespaced label the platform owns, no CRD schema involvement.

This is **additive to `APP_ID` in `spec.env`, not a replacement**. The two
serve different consumers and neither can do the other's job:

| | carrier | consumer | why it can't be the other |
|---|---|---|---|
| `APP_ID` | `spec.env` | the **pod** — the harness resolves the application from Hub at runtime | env vars are not indexable; answering "runs for app 42" means fetching every run and parsing `spec.env` |
| `konveyor.io/application` | `metadata.labels` | the **API** — `?application=<id>` becomes a `client.List()` label selector | a label is not visible inside the container |

Consequences, all verified in the shim prototype:

- Hub application ids must parse as a uint64 (`hub.ParseAppID` requires
  it), capping them at 20 digits — inside the apiserver's 63-character
  label-value limit, in a label-safe alphabet. One uint64-bounded check
  covers both concerns: a run the harness would reject at startup, and a
  create the apiserver would reject outright. A plain digits regex is not
  that check — a 21-digit id passes it and overflows `ParseUint` anyway.
- Runs created before the label are invisible to the selector. A filtered
  list is "runs we can prove belong to 42", not "every run that ever
  touched 42". Callers needing the old runs keep a `spec.env` fallback for
  one release.
- **Workflow stage runs do not inherit it.** The controller builds each
  stage AgentRun's labels from scratch, so filtering `agentruns` finds
  single runs only. Propagating parent labels to stage runs is an upstream
  controller change, tracked separately.

ADR 0006 as merged specifies only the env var — "the application ID goes
directly on the CR as an env var", "other resource types are listed
unfiltered". PR #105 amends it to match this section. If that amendment is
rejected, this subsection goes with it and per-application views stay a
client-side scan; the rest of this ADR is independent of the outcome.

### (b) Param sources: generic field, namespaced values, no enum

A param may declare a **source identifier** — a free-form, namespaced
string (`konveyor.io/application-repository-url`), following the
`storageClassName`/`ingressClassName` pattern: the mechanism is
platform-neutral, the vocabulary belongs to whoever resolves it and lives
in documentation, not CRD validation.

Sources declare **what a value means, not who fills it in.** Two
consumers, two moments:

- The **UI** consumes sources at form time: a recognized source means
  "this comes from the selected application" — no input field, a resolved
  preview row instead. This is the contract that collapses the create
  form, and it holds regardless of the platform behind the form.
- **Resolution** happens per the platform's architecture. On the Konveyor
  Hub path there is NO create-time resolution: Hub injects connectivity
  (`HUB_BASE_URL`, `APP_ID`, scoped token) and the harness pulls
  application data from Hub at runtime (ADR 0006, enhancement #295 —
  which rejected the smart-endpoint alternative). Create-time resolution
  remains the mechanism for hosts WITHOUT a runtime data plane — the IDE
  extension and the standalone prototype shim — which fill `spec.params`
  from the application record when the run is created.

Semantics (normative):

- A param with a source **the consumer recognizes** is platform-supplied —
  by the harness at runtime (Hub path) or by the creating host (standalone
  path). The UI does not render a field for it; it shows what will be
  resolved.
- A param **without** a source is caller-supplied (form field).
- **Fail open, and it outranks every other rule here:** a consumer that
  does not recognize a source value MUST treat the param exactly as if it
  had no source — render the form field, accept the caller's value. This is
  what keeps an older UI/Hub working when newer agents appear: skew
  degrades to "user types it", never to "field vanishes" or "manifest
  rejected". A UI that hides unrecognized-source params is **non-conformant**
  (it strands the user with an unfillable required param).
- An explicit caller-supplied value always wins over resolution.
- A `required` param with a **recognized** source that the selected
  application cannot supply, and which the caller did not supply, is a
  **clear pre-create error** (HTTP 400), never a silently empty value. This
  guarantee is deliberately scoped to recognized sources with an
  application selected; outside that scope the param is caller-supplied and
  ordinary required-ness rules apply.
- An annotation entry naming a param the Agent does not declare in
  `spec.params` (a stale annotation after a rename) MUST be ignored, not
  injected — the sandbox must never receive a value for a param its Agent
  never declared, whatever the delivery carrier (`KONVEYOR_PARAM_*` env
  vars today; the params.json file if ADR 0009 lands — this ADR is about
  where values come FROM, that one about how they reach the pod).

**Carrier:** prototyped as an Agent annotation so no CRD change is needed:

```yaml
metadata:
  annotations:
    konveyor.io/param-sources: |
      {"repository": "konveyor.io/application-repository-url",
       "branch": "konveyor.io/application-repository-branch"}
```

Graduation path: an optional free-form `source` field on `AgentParam`
(no enum, no controller interpretation) once the pattern is agreed
upstream. Annotation → field is a mechanical migration.

### (c) Credentials: same pattern, but the hard part is materialization

Credentials must not be an `envFrom` punt (that couples every caller to
per-agent Secret knowledge — same flaw the SigV4 feedback flagged).
An agent declares credential needs identically:

```yaml
konveyor.io/credential-sources: |
  {"git": "konveyor.io/application-identity"}
```

The platform resolves `konveyor.io/application-identity` to the selected
application's credential and mounts it via `AgentRun.spec.envFrom`.
Applications without an identity (public repos) resolve to nothing and the
run proceeds without credentials.

**Decision: on the Hub path, credentials resolve at runtime, not create
time.** Hub stores credentials as `Identity` records in its encrypted
vault; the REST API exposes the identity's name, never the secret. Rather
than Hub decrypting and materializing secrets into the pod at create time,
the harness fetches the decrypted identity from Hub at runtime using its
scoped token (enhancement #295 §945–953) — the same pattern as every other
piece of application data. No secret ever transits the create path.

Create-time `envFrom` mounting remains the standalone-host mechanism. The
prototype shim, which has no runtime data plane, bridges known identity
names to pre-created k8s Secrets (`IDENTITY_SECRET_BRIDGE`) — an explicit
stub of the runtime resolution Hub provides for real.

### (d) API surface

SHIM API v1 (and therefore the future Hub proxy) gains:

- `GET /api/applications` → the platform's application inventory. The shim
  reads **real Konveyor Hub** over `HUB_URL` (`/applications` + `/identities`,
  mapped to `{id, name, repository, identity, identitySecret}`) and falls
  back to a built-in stub only when Hub is unreachable. Repo URL/branch and
  the identity name are genuine Hub data; only the identity→Secret bridge is
  stubbed (see (c)). Production is Hub reading its own Application table.
- `POST /api/agentruns` accepts `applicationRef`; what it implies depends
  on the host per the semantics above — Hub coordinates + label on the
  Konveyor path, create-time fill of sourced params on standalone hosts.

## Consequences

- The create-run form for a fully sourced agent collapses to: application
  picker + instructions. Verified in the prototype UI: `repository` and
  `branch` disappear as fields and render as "resolved from application"
  rows with live values; the git credential shows the Secret it mounts.
- The generic CRD stays Konveyor-agnostic; RHDH or any other platform can
  define its own source vocabulary without touching the schema.
- Known vocabulary (initial): `konveyor.io/application-repository-url`,
  `konveyor.io/application-repository-branch`,
  `konveyor.io/application-identity` (Secret-valued).
- Open upstream questions: where the well-known vocabulary doc lives, and
  whether `source` graduates to the CRD field or stays annotation-based
  until Hub's application model settles.
