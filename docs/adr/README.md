# Architecture Decision Records

ADRs in this directory are living documents. They preserve the original
rationale while allowing the repository to record what changed as the
implementation evolves.

## ADR index

The index is generated from each ADR's YAML front matter. Do not edit the
generated block by hand.

<!-- BEGIN ADR INDEX -->

| ADR | Title | Status | Implementation | Last reviewed |
| --- | --- | --- | --- | --- |
| [0001](./0001-agentic-platform-crd-architecture.md) | Agentic Platform CRD Architecture | accepted | amended | 2026-08-31 |
| [0002](./0002-acp-transport-and-observability.md) | ACP Transport and Agent Observability | accepted | amended | 2026-08-31 |
| [0003](./0003-hub-curated-api-for-agent-resources.md) | Hub Curated API for Agent Resources | superseded | superseded | 2026-08-31 |
| [0004](./0004-openshell-as-execution-interface.md) | OpenShell as Execution Interface | accepted | deferred | 2026-08-31 |
| [0006](./0006-hub-addon-pattern-for-agent-resources.md) | Hub follows the addon pattern for agent resources | proposed | amended | 2026-08-31 |
| [0007](./0007-harness-thin-runner-and-skillcard-skills.md) | Harness as Thin Single-Stage Runner with SkillCard-Based Skills | accepted | amended | 2026-08-31 |
| [0008](./0008-harness-owns-pod-acp-port.md) | Harness Owns the Pod ACP Port (Tee Topology) | proposed | in-sync | 2026-08-31 |
| [0009](./0009-parameter-delivery-via-params-json.md) | Parameter Delivery via params.json | proposed | amended | 2026-08-31 |
| [0010](./0010-skill-content-boundary.md) | Skill Content Boundary — Knowledge vs Execution Control | proposed | deferred | 2026-08-31 |
| [0011](./0011-execution-controls-mode-and-session-type.md) | Execution Controls and Mode on CRDs | proposed | amended | 2026-08-31 |
| [0012](./0012-client-contract-and-transports.md) | Verified client contract and layered transports for AgentRun UIs | proposed | in-sync | 2026-08-31 |
| [0013](./0013-platform-resolved-params.md) | Platform-resolved agent params (param sources) | proposed | deferred | 2026-08-31 |
| [0014](./0014-skill-loading-and-prompt-assembly.md) | Skill Loading and Prompt Assembly | proposed | amended | 2026-08-31 |
| [0015](./0015-skill-packaging-and-delivery.md) | Skill Packaging and Delivery | proposed | amended | 2026-08-31 |
| [0016](./0016-gateway-crd-as-interim-execution-interface.md) | Gateway CRD as Interim Execution Interface | accepted | in-sync | 2026-08-31 |
| [0017](./0017-ask-user-tool-and-elicitation.md) | In-turn human questions via the ask_user tool and ACP elicitation | proposed | in-sync | 2026-08-31 |
| [0018](./0018-execution-fields-on-agentrun-and-succeeded-condition.md) | Execution Fields on AgentRun, and a `Succeeded` Terminal Condition | proposed | in-sync | 2026-08-31 |
| [0019](./0019-skill-catalog-repository-layout.md) | Skill Catalog Repository Layout | proposed | in-sync | 2026-08-31 |
<!-- END ADR INDEX -->

## Metadata

Every ADR starts with YAML front matter. The metadata is the machine-readable
summary and reconciliation record; the narrative `Update (YYYY-MM-DD):`
amendments remain in the body so the decision history stays legible.

Required fields are `adr`, `title`, `description`, `status`, `date`,
`last_updated`, `authors`, `last_reviewed`, `implementation_status`, and
`review_note`. Use `last_updated: null` when an ADR has never received a
material amendment. Valid implementation statuses are `in-sync`, `amended`,
`superseded`, and `deferred`.

```yaml
---
adr: "NNNN"
title: "Decision title"
description: "One-line summary for progressive disclosure."
status: proposed
date: "YYYY-MM-DD"
last_updated: null
authors:
  - "Name"
last_reviewed: "YYYY-MM-DD"
implementation_status: in-sync
review_note: "Short result of the latest implementation review."
---
```

Optional fields such as `status_note`, `relates_to`, `superseded_by`, and
`provenance` also belong in the front matter when they describe the ADR
rather than its narrative rationale.

## Amendment convention

- Keep the original context and decision legible; do not silently rewrite
  accepted history.
- Add an entry near the header for each material implementation or context
  change:

  `**Update (YYYY-MM-DD):** What changed, and which part of the original
  decision or implementation it affects.`

- Update `last_updated` to the date of the newest amendment. A metadata-only
  migration or review does not count as an amendment.
- Use a new ADR with an explicit supersession relationship only for a full
  reversal or an incompatible replacement. A later ADR may still supersede
  one section or clause; identify that scope explicitly in both documents.
- Keep `status` meaningful: `proposed` means the decision is not yet
  accepted, `accepted` means it is the current decision, and `superseded`
  means the named replacement is authoritative for the superseded scope.

## Reconciliation checklist

When implementation changes, compare each affected ADR with the CRD types,
controllers, entry point/harness contract, configuration, and user-facing
documentation. Record material drift as an amendment in the affected ADR;
record the review and any remaining gaps in its front matter. The generated
[RECONCILIATION.md](RECONCILIATION.md) is the current view of that metadata.
Review the full ADR set periodically, including proposed and superseded ADRs,
because they remain useful context for understanding current boundaries and
historical decisions.

## Generating the indexes

Run `make generate-adr-index` after changing ADR metadata. CI or a local check
can run `make verify-adr-index` to ensure the README index and reconciliation
table are up to date.
