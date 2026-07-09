# Enhancement: Migration Harness on the Konveyor Agentic Platform

## Problem

migration-harness runs as a CLI tool on a single machine. It uses one container, one model, and one config for all migration steps. Skills and references are baked into the image. There is no Kubernetes-native lifecycle management, no per-step model selection, and no portable skill distribution.

## Proposal

Map migration-harness onto the Konveyor Agentic Platform CRDs so that migration steps become portable SkillCards, the pipeline becomes an AgentPlan, and execution happens in isolated Sandboxes managed by a Controller.

## CRD Mapping

### SkillCard

Each migration step becomes a SkillCard — an OCI artifact mounted into containers via ImageVolumes at runtime.

| SkillCard | Type | Purpose |
|---|---|---|
| `detect` | skill | Build code graph, detect project structure |
| `plan` | skill | Generate migration plan from graph context |
| `execute` | skill | Transform files in dependency order |
| `verify` | skill | Build, test, report errors |
| `fix` | skill | Fix compilation errors, capture lessons |
| `javaee-quarkus` | rule | Reference catalog, always loaded into LLM context |
| `orchestrate` | skill | Meta-skill that sequences phases inside a Sandbox |

**Source:** migration-harness `skill-bundle/` and `recipes/` directories.

### SkillCollection

Groups related skills. Sources skills from OCI images or git repositories.

```yaml
SkillCollection: konveyor-quarkus-skills
  skills: [detect, plan, execute, verify, fix, javaee-quarkus, orchestrate]
```

**Source:** migration-harness `skill-bundle/goose-migration/` directory.

### LLMProvider

Declares LLM endpoint, credentials (via Secret reference), and model catalog with context window sizes.

```yaml
LLMProvider: gcp-vertex
  endpoint: vertex-ai
  secretRef: gcp-credentials
  models:
    - name: gemini-2.5-pro
      contextWindow: 1000000
```

The context window field enables upfront validation that skills + references fit within the model's limits before starting a run.

**Source:** migration-harness `~/.migration-harness/config` and `custom_providers/*.json`.

### Agent

A blueprint that binds a container image, an LLM provider + model, and a set of skills. The Agent CR does not run anything — the Controller reads it and builds a Sandbox from it.

```yaml
Agent: planning-worker
  image: migration-harness:latest
  llmProviderRef: gcp-vertex
  model: gemini-2.5-pro
  skills: [detect, plan]
  skillCollections: [konveyor-quarkus-skills]
```

Different Agents can use different models for the same image. This enables cost-aware model selection: powerful models for planning, cheap models for mechanical transforms.

### AgentPlan

A reusable pipeline definition. An architect writes it once. Developers run it against their applications.

```yaml
AgentPlan: java-ee-to-quarkus
  guide: "Migrate from Java EE to Quarkus 3"
  stages:
    - name: analysis
      agentRef: planning-worker
      phases:
        - name: detect
        - name: plan
    - name: execution
      agentRef: execution-worker
      phases:
        - name: execute
    - name: verification
      agentRef: verification-worker
      phases:
        - name: verify
        - name: fix
```

**Stages** get separate Sandboxes (fresh container, fresh LLM context, separate resource limits). **Phases** within a stage share the same Sandbox (same container, full LLM context continuity).

## Orchestration

Two components coordinate execution at different levels.

### Controller (Kubernetes operator — outside the container)

For each stage in the AgentPlan:

1. Reads the stage's `agentRef` and looks up the Agent CR
2. Looks up the LLMProvider CR for credentials and context window
3. Resolves SkillCards to OCI artifacts for ImageVolume mounts
4. Generates `phases.json` from the stage's phases (maps phase names to skill file paths and expected outputs)
5. Writes `phases.json` to the workspace PVC
6. Creates a Sandbox (Pod) with the Agent's image, mounted skills, injected credentials, and workspace PVC
7. Runs: `goose run --skill /meta-skill/orchestrate.md --input /workspace/phases.json`
8. Watches Pod status
9. On Pod completion: reads `stage-status.json` from PVC, records result in AgentPlanExecution CR, deletes Pod
10. Proceeds to next stage or marks execution complete

The Controller does not know how goose works. It sends one command per stage and reads one status file.

### Meta-skill (goose skill — inside the container)

The meta-skill (`orchestrate.md`) is a SkillCard mounted via ImageVolume. It:

1. Reads `phases.json` from the workspace
2. For each phase in order: loads the named skill from `/skills/`, executes it, verifies expected output files exist
3. Stops on first phase failure
4. Writes `stage-status.json` to the workspace

The meta-skill does not know about Kubernetes. It sequences phases and reports results. Rules (SkillCards with `type: rule`) are loaded into context automatically and provide reference knowledge.

### Communication

`phases.json` and `stage-status.json` on the workspace PVC are transient communication files between the Controller and the meta-skill. The Controller reads `stage-status.json` after each stage and records the result in the AgentPlanExecution CR. Both files get overwritten per stage. The CR is the permanent record.

```
Controller writes → phases.json (PVC)
Meta-skill reads  → phases.json
Meta-skill writes → stage-status.json (PVC)
Controller reads  → stage-status.json
Controller records → AgentPlanExecution CR (permanent)
```

## What Exists vs What Gets Built

| Component | Status | Location |
|---|---|---|
| Migration skills (detect, plan, execute, verify, fix) | Exists | migration-harness `skill-bundle/` |
| Reference catalogs | Exists | migration-harness `recipes/` |
| Migration pipeline logic | Exists | migration-harness `bin/` and `lib/` |
| Agent runtime (goose) | Exists | Block's goose |
| AgentPlan CRD format | Defined | ADR in dymurray/tackle2-ui |
| Sandbox management | Upstream | kubernetes-sigs/Agent Sandbox |
| Skill packaging/mounting | Upstream | skillimage.dev + ImageVolumes |
| Controller | Not built | Needs implementation |
| Meta-skill (orchestrate) | Draft | migration-harness `meta-skill/` |
| AgentPlanExecution CR | Not defined | Needs CRD spec |

## References

- [Konveyor Agentic Platform Deck](https://github.com/dymurray/tackle2-ui/blob/agent/docs/konveyor-agentic-platform-deck.md) — CRD architecture
- [ADR: Agentic Platform CRD Architecture](https://github.com/dymurray/tackle2-ui/blob/agent/docs/adr/0001-agentic-platform-crd-architecture.md)
- [skillimage.dev](https://skillimage.dev) — OCI skill packaging
- [kubernetes-sigs/Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox) — Sandbox CRDs
