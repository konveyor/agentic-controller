---
adr: "0010"
title: "Skill Content Boundary — Knowledge vs Execution Control"
description: "Defines the boundary between skill knowledge and deterministic execution controls owned by the harness."
status: proposed
date: "2026-08-05"
last_updated: "2026-09-02"
authors:
  - "David Zager"
last_reviewed: "2026-08-31"
implementation_status: deferred
review_note: "The boundary remains the intended rule, but the repository's shipped skills still contain some execution-control and container-layout instructions. The ADR remains proposed and this gap is explicit."
---

# ADR 0010: Skill Content Boundary — Knowledge vs Execution Control

**Update (2026-08-31):** The boundary remains the intended authoring rule,
but reconciliation found that the shipped `plan`, `execute`, and `verify`
skills still contain some execution-control, commit, and `/opt/skills`
filesystem instructions. This ADR remains proposed until those skills are
fully migrated; ADR 0014’s native loading and ADR 0015’s loader are already
implemented.

**Update (2026-09-02):** The Graphify references in this ADR describe the
former planning design, not a supported current workflow. Graphify was not
present in the shipped plan skill and has been removed from the base image.

## Context

ADR 0007 established two kinds of skills: **stage skills** (define
process — what to do) and **domain skills** (define knowledge — how to
do it). The harness is a thin single-stage runner that sends one
prompt and waits.

In practice, stage skills have accumulated execution control logic
that belongs in the harness or the agent runtime:

- The `verify` skill reads `KONVEYOR_PARAM_MAX_FIX_ITERATIONS` from
  the environment and tells the LLM to count its own fix-rebuild
  iterations. LLMs are unreliable at counting.
- The former `plan` skill told the LLM to run a deterministic analysis CLI — a
  task that requires no LLM judgment. The current plan skill performs its
  analysis by reading the repository and optional Hub results.
- All three stage skills (`plan`, `execute`, `verify`) tell the LLM
  to run `git add -A && git commit` — a harness concern per ADR 0007
  (the harness owns push; commit is a local operation the agent
  handles, but the instruction is redundant with agent behavior).
- The `javaee-to-quarkus` domain skill repeats "run `mvn compile`,
  check the exit code, stop if it fails" at the end of every module —
  an orchestration gate pattern rather than domain judgment about
  whether the code compiles.
- Three skills reference `/opt/skills/*/references/*.md` via `ls` —
  filesystem discovery the harness could resolve at startup.

Meanwhile, the domain knowledge content in these skills (reference
tables, transformation patterns, error-fix mappings, approach
guidance) is exactly right.

## Decision

**A skill contains knowledge and judgment criteria. It never contains
execution control.**

### Allowed in a skill

- Domain knowledge: reference tables, annotation maps, dependency
  maps, transformation patterns, error-fix mappings.
- Approach guidance: "work bottom-up through the dependency graph",
  "process one file at a time."
- Quality criteria: "every changed file must compile", "do not
  introduce new warnings."
- Output format: "write a PLAN.md with this structure."
- Judgment calls: "if you cannot make progress, write a handoff to
  `.konveyor/handoff.md` documenting what you tried and what remains."

### Not allowed in a skill

- Reading environment variables or parameter files for configuration
  values that the harness or controller should provide.
- Hard iteration caps enforced by reading env vars (e.g.
  `KONVEYOR_PARAM_MAX_FIX_ITERATIONS`). Soft guidance like "try two
  or three approaches before moving on" is fine — that is judgment,
  not execution control.
- Knowledge of harness or controller internals. A skill should not
  know how the harness manages git push, how the controller injects
  parameters, or how the ACP connection works.
- Running deterministic CLI gates as required pre-steps (`mvn compile` as a
  pass/fail gate between phases). Note:
  telling the agent to run a tool as part of its judgment is fine —
  "query the dependency graph to find upstream dependencies before
  migrating a module" is domain knowledge. The distinction is between
  a skill that uses a CLI as part of its reasoning (judgment) and one
  that uses it as a deterministic orchestration checkpoint (gate).
- Filesystem discovery that depends on container layout conventions
  (`ls /opt/skills/*/references/*.md`).

### The rule

When deciding whether something belongs in a skill or the harness, ask:

> **Does this require LLM judgment, or could a program do it
> deterministically?**

If a program could do it — loop counting, exit code checking as a
gate, file discovery based on container layout — it belongs in the
harness or the runtime. If it requires understanding context,
making trade-offs, or producing creative output — planning, code
generation, error diagnosis, deciding when you're stuck — it belongs
in the skill.

### Handoff as judgment

The instruction "if you are stuck, write a handoff" is agent judgment,
not execution control. The agent decides it is stuck based on its
assessment of progress. This is baked into the harness's base prompt,
not into individual skills. The harness also enforces execution limits
(ADR 0011) as a safety net — at ~85-90% of any limit, the harness
tells the agent to wind down.

### Immediate changes

- Remove `KONVEYOR_PARAM_MAX_FIX_ITERATIONS` from the `verify` skill.
  The execution guardrail is `GOOSE_MAX_TURNS` set by the harness
  (ADR 0011).
- Remove `ls /opt/skills/*/references/*.md` globs from skills in
  favour of relative paths. If ADR 0014 (native skill loading) lands,
  the runtime resolves relative paths from the skill directory
  natively — skills no longer need to know the container layout.

## Consequences

- Skill authors write domain knowledge and judgment criteria only.
  This is a simpler, more focused authoring experience.
- Execution control moves to the harness and CRD configuration
  (ADR 0011). Skill authors do not need to think about turn limits,
  cost budgets, or runtime mechanics.
- Existing stage skills (`plan`, `execute`, `verify`) need revision
  to remove execution control logic.
- The `javaee-to-quarkus` domain skill's orchestration gates (exit
  code checking, stop-on-failure) should be rewritten as domain
  judgment: "make sure this compiles" rather than "run `mvn compile`,
  check exit code, stop if non-zero." Build verification is domain
  knowledge — a Java skill knows what "verified" means. The gate
  pattern is the problem, not the verification itself.
- This sharpens the skill authoring contract: if you find yourself
  telling the LLM to read an env var or encoding knowledge of how
  the harness or controller works, you're in the wrong layer.
