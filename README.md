# Agentic Controller

Kubernetes controller for managing AI agent workloads. Defines CRDs
under the `konveyor.io` API group and controllers for composing and
executing agent workloads via [Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox).

## Overview

The controller follows the Tekton Task/TaskRun pattern: an **Agent**
declares what is available (skills, LLM providers, container image,
prompt, typed parameters) and an **AgentRun** supplies concrete values
(model selections, parameter values, instructions) to trigger execution.

The controller is domain-agnostic. It does not call Hub, Backstage, or
any inventory system. Parameter values are opaque — the controller
validates and passes them through. The creator of the AgentRun (UI, CLI,
CI pipeline) resolves application metadata before creating the CR.

### CRDs

| CRD | Purpose |
|-----|---------|
| **SkillCard** | Individual skill or rule. Resolves to an OCI artifact mounted at `/opt/skills/{name}/`. |
| **SkillCollection** | Group of skills. References skills by OCI image, git source, or SkillCard CR name. |
| **LLMProvider** | LLM service endpoint, credentials, and available models. |
| **Agent** | Template declaring available skills, providers, container image, prompt, and typed parameters. |
| **AgentRun** | Execute a single Agent with specific values. Creates an Agent Sandbox. |
| **AgentPlaybook** | Ordered sequence of stages, each referencing an Agent. |
| **AgentPlaybookRun** | Execute a playbook. Creates AgentRuns sequentially per stage. |

### Key design decisions

- **Agent Sandbox** is a hard dependency for workload execution
- **Git credentials** stay in the harness — the agent does not receive
  push credentials
- **Skills** are OCI artifacts mounted via ImageVolumes (K8s 1.33+)
- **Workspaces** are ephemeral — git is the persistence layer
- **ACP over HTTP** (via `goose serve`) provides real-time observability
  and human-in-the-loop interaction
- **Hub** provides curated REST endpoints for the UI

See `docs/adr/` for the full set of architecture decision records.

## Project structure

```
agentic-controller/
  api/v1alpha1/           CRD type definitions (Go structs)
  internal/controller/    Controller implementations
  internal/registry/      OCI registry client
  docs/adr/               Architecture Decision Records
  skills/                 Agent skills for contributors
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
| [redhat-et/skillimage](https://github.com/redhat-et/skillimage) | OCI skill packaging and distribution |
| [NVIDIA/OpenShell](https://github.com/NVIDIA/OpenShell) | Secure runtime for autonomous agents |

## Contributing

Read `AGENTS.md` for project conventions. Use the skills in `skills/`
for design workflows — `grill-with-docs` for stress-testing designs
against the domain model.

## Code of Conduct

Refer to Konveyor's Code of Conduct [here](https://github.com/konveyor/community/blob/main/CODE_OF_CONDUCT.md).
