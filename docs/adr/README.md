# Architecture Decision Records

ADRs in this directory are living documents. They preserve the original
rationale while allowing the repository to record what changed as the
implementation evolves.

## Amendment convention

- Keep the original context and decision legible; do not silently rewrite
  accepted history.
- Add an entry near the header for each material implementation or context
  change:

  `**Update (YYYY-MM-DD):** What changed, and which part of the original
  decision or implementation it affects.`

- New ADRs and materially amended ADRs must include
  `**Last updated:** YYYY-MM-DD`, set to the date of the newest amendment.
  An unchanged legacy ADR does not need a metadata-only edit; record a
  no-drift review in the reconciliation index instead.
- Use a new ADR with an explicit `Superseded by ADR NNNN` relationship only
  for a full reversal or an incompatible replacement. A later ADR may still
  supersede one section or clause; identify that scope explicitly in both
  documents.
- Keep `Status` meaningful: `proposed` means the decision is not yet
  accepted, `accepted` means it is the current decision, and `superseded`
  means the named replacement is authoritative for the superseded scope.

## Reconciliation checklist

When implementation changes, compare each affected ADR with the CRD types,
controllers, entry point/harness contract, configuration, and user-facing
documentation. Record material drift as an amendment in the affected ADR;
record the review and any remaining gaps in
[RECONCILIATION.md](RECONCILIATION.md). Review the full ADR set periodically,
including proposed and superseded ADRs, because they remain useful context
for understanding current boundaries and historical decisions.

## Template

```markdown
# ADR NNNN: Title

**Status:** proposed | accepted | superseded by ADR NNNN
**Date:** YYYY-MM-DD
**Last updated:** YYYY-MM-DD
**Authors:** Name

**Update (YYYY-MM-DD):** Optional dated amendment. Explain the implementation
or context change and preserve the original decision below.

## Context

## Decision

## Consequences
```

## Reconciliation

See [RECONCILIATION.md](RECONCILIATION.md) for the current review of every
ADR in this directory.
