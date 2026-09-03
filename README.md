# Agentic Controller

Kubernetes controller for managing AI agent workloads. Defines CRDs
under the `konveyor.io` API group and controllers for composing and
executing agent workloads via [Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox).

## Overview

The controller follows the Tekton Task/TaskRun pattern: an **Agent**
declares what is available (skills, gateways (provider/model combinations), container image,
prompt, typed parameters) and an **AgentRun** supplies concrete values
(gateway selection, parameter values, instructions) to trigger execution.

The controller is domain-agnostic. It does not call Hub, Backstage, or
any inventory system. Parameter values are opaque — the controller
validates and passes them through. The creator of the AgentRun (UI, CLI,
CI pipeline) resolves application metadata before creating the CR.

### CRDs

| CRD | Purpose |
|-----|---------|
| **SkillCard** | One skill or rule, from an OCI image, a git repository, or inline content. Assembled into `/opt/skills/{name}/` at pod init, where `{name}` is the skill's own frontmatter name. |
| **SkillCollection** | Group of skills. Points at an OCI image holding several, in which case the controller writes a SkillCard per skill it finds, or references existing SkillCards. |
| **Gateway** | LLM service endpoint serving one provider/model combination. |
| **Agent** | Template declaring available skills, gateways, container image, prompt, and typed parameters. |
| **AgentRun** | Execute a single Agent with specific values. Creates an Agent Sandbox. |
| **AgentWorkflow** | Ordered sequence of stages, each referencing an Agent. |
| **AgentWorkflowRun** | Execute a workflow. Creates AgentRuns sequentially per stage. |

### Key design decisions

- **Agent Sandbox** is a hard dependency for workload execution
- **Git credentials** stay in the entry point — the agent does not receive
  push credentials
- **Skills** are Agent Skills directories, delivered as ordinary OCI images
  mounted via ImageVolumes (K8s 1.33+), git clones, or inline content
- **Workspaces** are ephemeral — git is the persistence layer
- **ACP over HTTP** (via `goose serve`) provides real-time observability
  and human-in-the-loop interaction
- **Hub** provides curated REST endpoints for the UI

See `docs/adr/` for the full set of architecture decision records.

## Authoring images and skills

- [Create an agentic base image](docs/agent-base-images.md) — extend
  `agent-base`, build language images, publish them, and understand the
  harness contract.
- [Create a SkillCard image](docs/skill-card-images.md) — author, validate,
  package, publish, and reference skills and rules.

## Getting started

See [docs/getting-started.md](docs/getting-started.md) for a
step-by-step guide to deploying the controller, configuring a Gateway
with LLM credentials, and creating your first AgentRun.

## Project structure

```
agentic-controller/
  api/v1alpha1/           CRD type definitions (Go structs)
  api/skill/              Agent Skills frontmatter parsing and validation
  internal/controller/    Controller implementations
  internal/skills/        Skill assembly, and SkillCard materialization
  pkg/kai/                The kai CLI command tree (reusable cobra package)
  cmd/kubectl-kai/        The kubectl-kai plugin binary (see docs/kubectl-kai.md)
  cmd/skill-loader/       The init container and enumeration Job binary
  docs/                   Documentation (getting started, entry point contract, API specs)
  docs/adr/               Architecture Decision Records
  harness/                In-pod entry point: git lifecycle, parameter delivery, ACP tee
  skills/                 Agent Skills directories:
    plan, execute,          shipped in the bundle image built by
    verify,                 skills/Containerfile; a SkillCard selects
    javaee-to-quarkus         one of them via subPath
    grill-me,               contributor skills for developing ON this
    grill-with-docs,        repo — deliberately not in the bundle image
    submit-pr
    examples/               SkillCard fixtures (not shipped)
    Containerfile           builds the skill bundle image
  CONTEXT.md              Domain glossary
  AGENTS.md               Agent-facing instructions
```

## Platform requirements

- Kubernetes 1.33+ (ImageVolume GA)
- OpenShift 4.20+
- Agent Sandbox v0.5.x

## Related projects

| Project | Role |
|---------|------|
| [konveyor/enhancements](https://github.com/konveyor/enhancements) | Enhancement proposals |
| [konveyor/tackle2-hub](https://github.com/konveyor/tackle2-hub) | Application inventory, curated REST API for agent resources |
| [konveyor/tackle2-ui](https://github.com/konveyor/tackle2-ui) | Web UI |
| [kubernetes-sigs/agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) | Sandbox CRDs for agent workloads |
| [agentskills.io](https://agentskills.io) | The skill format our skills are written in |
| [NVIDIA/OpenShell](https://github.com/NVIDIA/OpenShell) | Secure runtime for autonomous agents |

## Contributing

Read `AGENTS.md` for project conventions. Use the skills in `skills/`
for design workflows — `grill-with-docs` for stress-testing designs
against the domain model.

## Code of Conduct

Refer to Konveyor's Code of Conduct [here](https://github.com/konveyor/community/blob/main/CODE_OF_CONDUCT.md).
