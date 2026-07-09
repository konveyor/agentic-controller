# ADR 0005: Platform-resolved agent params (param sources)

- **Status:** proposed (prototype verified in this repo)
- **Date:** 2026-07-08
- **Relates to:** ADR 0004 (client contract), konveyor/agentic-controller#22/#24,
  the APB `plans`/parameter-metadata precedent

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

### (b) Param sources: generic field, namespaced values, no enum

A param may declare a **source identifier** — a free-form, namespaced
string (`konveyor.io/application-repository-url`), following the
`storageClassName`/`ingressClassName` pattern: the mechanism is
platform-neutral, the vocabulary belongs to whoever resolves it and lives
in documentation, not CRD validation.

Semantics (normative):

- A param with a source **the consumer recognizes** is resolved by the
  platform (Hub) at run creation, from the caller-selected application. The
  UI does not render a field for it; it shows what will be resolved.
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
  injected — the sandbox must never receive a `KONVEYOR_PARAM_*` for a
  param its Agent never declared.

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

**Open question surfaced by wiring this to real Hub.** Repo URL and branch
are plain fields on a Hub `Application` — read them and you're done. A
credential is *not*: Hub stores it as an `Identity` in its own encrypted
vault, and the REST API exposes the identity's *name*, never the secret.
So `application-identity` resolves cleanly to "this app uses Hub identity
`coolstore-git`", but turning that into something the sandbox can use
requires the platform to **decrypt the vault identity and materialize it
into the pod** (as a mounted Secret or injected env). Production Hub, which
owns the vault, does this itself. The shim can't — it only sees the name —
so it *bridges* known identity names to a pre-created k8s Secret
(`IDENTITY_SECRET_BRIDGE`) and the UI shows both: `Hub identity:
coolstore-git → git-credentials-coolstore`. That bridge is the one honest
stub left in the flow, and materialization is the concrete thing Hub must
own.

### (d) API surface

SHIM API v1 (and therefore the future Hub proxy) gains:

- `GET /api/applications` → the platform's application inventory. The shim
  reads **real Konveyor Hub** over `HUB_URL` (`/applications` + `/identities`,
  mapped to `{id, name, repository, identity, identitySecret}`) and falls
  back to a built-in stub only when Hub is unreachable. Repo URL/branch and
  the identity name are genuine Hub data; only the identity→Secret bridge is
  stubbed (see (c)). Production is Hub reading its own Application table.
- `POST /api/agentruns` accepts `applicationRef`; the platform resolves
  sourced params/credentials from that application per the semantics
  above.

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
