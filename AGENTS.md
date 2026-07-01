# Agentic Controller — Agent Instructions

All agent-facing guidance for this repository. Read this file first.

## What this project is

A Kubernetes controller for managing AI agent workloads. Defines CRDs
(Agent, AgentRun, AgentPlaybook, SkillCard, SkillCollection,
LLMProvider) and controllers for composing and executing agent
workloads via Agent Sandbox.

## Key documents

- `CONTEXT.md` — Domain glossary. Canonical definitions for all
  project-specific terms. Consult before using any domain term.
- `docs/adr/` — Architecture Decision Records. Read before proposing
  changes that contradict existing decisions.

## How to work in this repo

### Go code

This is a controller-runtime project. Follow standard controller-runtime
patterns:

- CRD types live in `api/v1alpha1/`
- Controllers live in `internal/controller/`
- Use `sigs.k8s.io/controller-runtime` for reconciliation
- Use kubebuilder markers for CRD generation
- Run `make` to regenerate code after modifying type files

### ADRs

ADRs are immutable once accepted. To change a decision, create a new
ADR that supersedes the old one — don't edit the original.

Use the `grill-with-docs` skill when stress-testing a design decision.
It will challenge the plan against the existing domain model in
`CONTEXT.md` and create ADRs as decisions crystallise.

### Commits

- Write concise commit messages that describe what changed and why
- One logical change per commit
- Reference issue numbers when applicable

### Domain language

Always use the terms defined in `CONTEXT.md`. If you're unsure about
a term, check the glossary first. If a term is missing, propose it.

Key terms to get right:
- **Agent** is a template (what's available), **AgentRun** is an
  invocation (concrete values, triggers execution)
- **SkillCard** is an individual skill or rule, **SkillCollection**
  is a group of skills
- The **harness** is the Go binary entrypoint in the base image that
  manages git lifecycle and launches the agent runtime
- The **controller** is the Kubernetes controller that reconciles CRDs
  — it does NOT connect to agent pods or interpret parameter values

### Key design principles

1. **Controller is domain-agnostic.** It does not call Hub, Backstage,
   or any inventory system. Parameter values are opaque.
2. **Agent Sandbox is a hard dependency.** No bare-Pod fallback.
3. **Git credentials stay in the harness.** The agent does not receive
   push credentials in its env vars or git config.
4. **Controller is a stateless reconciler.** No ACP connections, no
   in-memory streaming state. It watches CRs only.
5. **The UI connects to agent pods directly for ACP streaming.** The
   controller is not an intermediary for real-time agent interaction.
6. **Hub provides curated REST endpoints.** The UI talks to Hub, Hub
   talks to the k8s API via controller-runtime client.

## Skills

Skills are in `skills/` at the repo root. Use them for repeatable
workflows:

- `skills/grill-me/` — Stress-test a plan or design through
  relentless questioning
- `skills/grill-with-docs/` — Same as grill-me but updates
  CONTEXT.md and creates ADRs inline as decisions crystallise
