# Migration Harness on the Konveyor Agentic Platform

How migration-harness maps to the Agentic Platform CRDs.

---

## Today: migration-harness as a CLI

```
migration-harness ~/coolstore "Migrate from Java EE to Quarkus"

  detect  →  plan  →  execute  →  verify  →  fix
     │          │         │          │         │
  graphify    goose     goose      goose     goose
```

Everything lives in one container, one config, one model. Works, but:
- One model for all steps (planning and file transforms get the same brain)
- Skills and references baked into the image
- No Kubernetes-native lifecycle management

---

## Tomorrow: migration-harness on Agentic Platform

### SkillCards — the migration steps

Each step becomes a portable, versioned OCI artifact:

| SkillCard | Type | What it does |
|---|---|---|
| `detect` | skill | Build code graph, detect project structure |
| `plan` | skill | Generate migration plan from graph context |
| `execute` | skill | Transform files in dependency order |
| `verify` | skill | Build, test, report errors |
| `fix` | skill | Fix compilation errors, capture lessons |
| `javaee-quarkus` | rule | Reference catalog — always loaded into context |

Skills are mounted into containers via **ImageVolumes** (K8s 1.33+ / OCP 4.20+) — not baked into the image. Read-only, cached by kubelet, no init container needed.

### SkillCollection — the reference catalog

```yaml
SkillCollection: konveyor-quarkus-skills
  sources:
    - oci: quay.io/konveyor/migration-skills:latest
    - git: https://github.com/konveyor/migration-recipes
```

Groups related skills and references. Versioned, distributable, reusable across teams.

### LLMProvider — the model config

```yaml
LLMProvider: litmaas
  endpoint: https://litellm-litemaas.apps.prod.rhoai.rh-aiservices-bu.com/v1
  secretRef: litmaas-api-key
  models:
    - name: Qwen3.6-35B-A3B
      contextWindow: 128000
    - name: gemini-2.5-pro
      contextWindow: 1000000
```

Replaces `~/.migration-harness/config` and `custom_providers/*.json`. The context window field lets the platform validate that skills + references fit within the model's limits before starting a run.

### Agent — the worker (blueprint)

The Agent CR is a **blueprint**, not a running process. It describes what a worker looks like. The Controller reads the blueprint and builds a Sandbox (Pod) from it when needed.

```yaml
Agent: migration-worker
  image: migration-harness:latest
  llmProviderRef: litmaas
  model: Qwen3.6-35B-A3B
  skills: [detect, plan, execute, verify, fix]
  skillCollections: [konveyor-quarkus-skills]
```

Binds image + brain + skills. Users bring their own image with whatever toolchains they need (Java, Go, .NET). Skills and references are mounted at runtime, not baked in.

Different workers can use different models — powerful models for planning and debugging, cheap models for mechanical transforms:

```yaml
Agent: planning-worker           # powerful model for planning
  image: migration-harness:latest
  llmProviderRef: gcp-vertex
  model: gemini-2.5-pro
  skills: [detect, plan]

Agent: execution-worker           # cheap model for transforms
  image: migration-harness:latest
  llmProviderRef: litmaas
  model: Qwen3.6-35B-A3B
  skills: [execute]

Agent: verification-worker        # powerful model for debugging
  image: migration-harness:latest
  llmProviderRef: gcp-vertex
  model: gemini-2.5-pro
  skills: [verify, fix]
```

### AgentPlan — the pipeline

An **architect** writes the AgentPlan once. Developers reuse it against their apps.

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

Each stage gets its own Sandbox (fresh container, fresh LLM context). Phases within a stage share the same Sandbox. All stages share the workspace PVC.

---

## Stages and Sandboxes

**Phases within a stage** = same container, same memory, full LLM context continuity. Like running two commands in the same terminal.

**Stages** = new container, fresh LLM context window. But they share the workspace PVC, so files written by Stage 1 are visible to Stage 2.

Why separate Sandboxes per stage:

| Reason | Example |
|---|---|
| **Context window reset** | Plan step fills context with graph data. Execution step starts fresh — loads only PLAN.md + current file |
| **Different models** | Planning needs gemini-2.5-pro (1M context). Execution works with Qwen (128K, cheap) |
| **Isolation** | Stage 2 crashes mid-execution — Stage 1 results are safe on PVC |
| **Resource limits** | Detect needs CPU for graphify. Execute needs memory for LLM context |

---

## Orchestration: Controller + Meta-Skill

Two components work together at different levels:

```
Controller  = Kubernetes-level orchestration (outside the container)
Meta-skill  = Goose-level orchestration (inside the container)
```

### Controller (Kubernetes operator)

Manages infrastructure. Doesn't know how goose works.

- Watches for AgentPlan execution requests
- Reads each stage's `agentRef` → looks up the Agent CR
- Creates a Sandbox (Pod) from the Agent blueprint
- Mounts skills via ImageVolumes
- Injects LLM credentials from Secrets
- Attaches workspace PVC
- Calls goose with the meta-skill inside the Sandbox
- Waits for stage completion
- Tears down Sandbox, moves to next stage
- Handles failures — restarts a failed stage

### Meta-Skill (goose skill — runs inside the Sandbox)

Drives LLM work. Doesn't know about Kubernetes.

The meta-skill is a SkillCard like any other — packaged as an OCI artifact, mounted via ImageVolume:

```yaml
SkillCard: orchestrate
  type: skill
  oci: quay.io/konveyor/meta-skills:latest
```

The Controller calls goose with the meta-skill and a phases definition:

```bash
goose run \
  --skill /meta-skill/orchestrate.md \
  --input phases.json
```

The meta-skill reads `phases.json`, loads each skill in order, executes it, checks outputs, and reports status:

```
phases.json:
  [{"name":"detect","skill":"detect.md"},
   {"name":"plan","skill":"plan.md"}]

Meta-skill:
  Phase 1: detect
    → loads /skills/detect.md
    → runs graphify
    → writes detect.json, graph.json

  Phase 2: plan
    → loads /skills/plan.md
    → reads graph.json
    → loads /skills/javaee-quarkus.md (rule — always loaded)
    → writes PLAN.md

  Done → writes stage-status.json
```

### Responsibility Split

```
Controller knows:                    Meta-Skill knows:
──────────────────                   ─────────────────
AgentPlan stages                     How to read phases.json
Which Agent per stage                How to load a skill file
When to create/destroy Sandboxes     How to call goose tools
What to mount (skills, secrets)      How to check outputs
Stage-level failure recovery         Phase-level sequencing
                                     How to report status
```

Controller speaks Kubernetes. Meta-skill speaks goose. Neither needs to understand the other's domain.

---

## End-to-End Flow: Detailed Walkthrough

A developer wants to migrate coolstore from Java EE to Quarkus. Here is every step, every CRD, every component involved.

### Step 0: CRDs Already in the Cluster

Before any migration runs, the architect has applied these CRDs:

**SkillCards** (5 migration steps + 1 reference rule):

```yaml
SkillCard: detect          # type: skill — build code graph
SkillCard: plan            # type: skill — generate migration plan
SkillCard: execute         # type: skill — transform files
SkillCard: verify          # type: skill — build and test
SkillCard: fix             # type: skill — fix compilation errors
SkillCard: javaee-quarkus  # type: rule  — always loaded reference catalog
SkillCard: orchestrate     # type: skill — meta-skill, sequences phases
```

Each SkillCard references an OCI artifact (e.g., `quay.io/konveyor/migration-skills:latest`).

**SkillCollection** (groups the references):

```yaml
SkillCollection: konveyor-quarkus-skills
  skills: [detect, plan, execute, verify, fix, javaee-quarkus, orchestrate]
```

**LLMProviders** (two providers, different tiers):

```yaml
LLMProvider: gcp-vertex
  endpoint: vertex-ai
  secretRef: gcp-credentials
  models:
    - name: gemini-2.5-pro
      contextWindow: 1000000

LLMProvider: litmaas
  endpoint: https://litellm-litemaas.apps.prod.rhoai.rh-aiservices-bu.com/v1
  secretRef: litmaas-api-key
  models:
    - name: Qwen3.6-35B-A3B
      contextWindow: 128000
```

**Agents** (three workers — different brains, same body):

```yaml
Agent: planning-worker
  image: migration-harness:latest
  llmProviderRef: gcp-vertex
  model: gemini-2.5-pro           # powerful brain for planning
  skills: [detect, plan]
  skillCollections: [konveyor-quarkus-skills]

Agent: execution-worker
  image: migration-harness:latest
  llmProviderRef: litmaas
  model: Qwen3.6-35B-A3B          # cheap brain for transforms
  skills: [execute]
  skillCollections: [konveyor-quarkus-skills]

Agent: verification-worker
  image: migration-harness:latest
  llmProviderRef: gcp-vertex
  model: gemini-2.5-pro           # powerful brain for debugging
  skills: [verify, fix]
  skillCollections: [konveyor-quarkus-skills]
```

**AgentPlan** (the reusable pipeline):

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

All of this lives in the cluster, ready to be used.

---

### Step 1: Developer Triggers the Migration

Developer clicks "Run" in the Konveyor UI (or applies a CR):

```
"Run AgentPlan java-ee-to-quarkus against coolstore"
```

The Controller sees a new AgentPlan execution request.

---

### Step 2: Controller Reads the AgentPlan

```
Controller:
  1. Reads AgentPlan: java-ee-to-quarkus
  2. Sees 3 stages: analysis, execution, verification
  3. Starts with Stage 1: analysis
  4. Reads agentRef: planning-worker
```

---

### Step 3: Controller Looks Up the Agent CR (planning-worker)

```
Controller reads Agent CR: planning-worker
  image:           migration-harness:latest
  llmProviderRef:  gcp-vertex
  model:           gemini-2.5-pro
  skills:          [detect, plan]
  skillCollections: [konveyor-quarkus-skills]
```

Controller now knows everything it needs to build the Sandbox.

---

### Step 4: Controller Looks Up the LLMProvider CR (gcp-vertex)

```
Controller reads LLMProvider CR: gcp-vertex
  endpoint:   vertex-ai
  secretRef:  gcp-credentials
  model:      gemini-2.5-pro
  contextWindow: 1000000
```

Controller validates: do the skills + rules fit within 1M context? Yes → proceed.

---

### Step 5: Controller Creates Sandbox A

Controller builds a Pod from the Agent blueprint:

```
Sandbox A (Pod):
  ┌────────────────────────────────────────────────────┐
  │                                                    │
  │  Container: migration-harness:latest               │
  │                                                    │
  │  ImageVolumes (mounted from OCI artifacts):        │
  │    /skills/detect.md                               │
  │    /skills/plan.md                                 │
  │    /skills/javaee-quarkus.md   (rule — always on)  │
  │    /meta-skill/orchestrate.md                      │
  │                                                    │
  │  Environment:                                      │
  │    GCP_CREDENTIALS=<from Secret gcp-credentials>   │
  │    MODEL=gemini-2.5-pro                            │
  │                                                    │
  │  Volumes:                                          │
  │    /workspace  ← PVC (shared across all stages)    │
  │                                                    │
  │  phases.json (generated by Controller):            │
  │    [{"name":"detect","skill":"detect.md"},         │
  │     {"name":"plan","skill":"plan.md"}]             │
  │                                                    │
  └────────────────────────────────────────────────────┘
```

---

### Step 6: Controller Runs Goose with the Meta-Skill

Controller executes inside the Sandbox:

```bash
goose run \
  --skill /meta-skill/orchestrate.md \
  --input phases.json
```

This is the ONLY command the Controller sends. It doesn't know what detect or plan do. It just says "run the orchestrate skill with these phases."

---

### Step 7: Meta-Skill Takes Over Inside the Sandbox

The meta-skill reads `phases.json` and drives goose through each phase:

```
Meta-skill (orchestrate.md):

  Reading phases.json: 2 phases to execute

  ┌─── Phase 1: detect ──────────────────────────────┐
  │                                                   │
  │  Meta-skill loads /skills/detect.md               │
  │  Goose reads the skill instructions               │
  │  Goose runs: graphify /workspace/coolstore        │
  │  Goose writes:                                    │
  │    /workspace/coolstore/detect.json               │
  │    /workspace/coolstore/graph.json    (4.4MB)     │
  │    /workspace/coolstore/GRAPH_REPORT.md           │
  │                                                   │
  │  Meta-skill checks: detect.json exists? YES       │
  │  Phase 1 complete ✓                               │
  └───────────────────────────────────────────────────┘
         │
         │ same Sandbox, same goose process
         │ full LLM context continuity
         │ (graph data stays in context for planning)
         ▼
  ┌─── Phase 2: plan ────────────────────────────────┐
  │                                                   │
  │  Meta-skill loads /skills/plan.md                 │
  │  /skills/javaee-quarkus.md already loaded (rule)  │
  │  Goose reads graph.json from Phase 1              │
  │  Goose generates migration plan                   │
  │  Goose writes:                                    │
  │    /workspace/coolstore/PLAN.md                   │
  │    /workspace/coolstore/plan.json                 │
  │                                                   │
  │  Meta-skill checks: PLAN.md exists? YES           │
  │  Phase 2 complete ✓                               │
  └───────────────────────────────────────────────────┘
         │
         ▼
  Meta-skill writes stage-status.json:
    {"stage": "analysis", "status": "complete",
     "phases": [
       {"name": "detect", "status": "success"},
       {"name": "plan", "status": "success"}
     ]}
```

---

### Step 8: Controller Reads Stage Status, Records It, Tears Down Sandbox A

```
Controller:
  1. Watches Pod status → Pod "Succeeded"
  2. Reads stage-status.json from workspace PVC
  3. Records result in AgentPlanExecution CR:
       status.stages[0]:
         name: analysis
         status: complete
         phases: [{detect: success}, {plan: success}]
  4. Tears down Sandbox A (Pod deleted)
  5. Writes NEW phases.json to PVC for next stage
  6. Moves to Stage 2: execution
  7. Reads agentRef: execution-worker
```

The stage result now lives in the **AgentPlanExecution CR** (Kubernetes-native),
not on the PVC. `stage-status.json` on PVC is transient — it gets overwritten
by the next stage and that's fine.

---

### Step 9: Controller Looks Up Agent CR (execution-worker)

```
Controller reads Agent CR: execution-worker
  image:           migration-harness:latest
  llmProviderRef:  litmaas
  model:           Qwen3.6-35B-A3B        # different model — cheap
  skills:          [execute]
  skillCollections: [konveyor-quarkus-skills]
```

Looks up LLMProvider `litmaas` → gets endpoint, secret, context window (128K).

---

### Step 10: Controller Creates Sandbox B

```
Sandbox B (Pod):
  ┌────────────────────────────────────────────────────┐
  │                                                    │
  │  Container: migration-harness:latest               │
  │  Model: Qwen3.6-35B-A3B (cheap, 128K context)     │
  │                                                    │
  │  ImageVolumes:                                     │
  │    /skills/execute.md                              │
  │    /skills/javaee-quarkus.md   (rule)              │
  │    /meta-skill/orchestrate.md                      │
  │                                                    │
  │  /workspace ← same PVC (PLAN.md is already here)  │
  │                                                    │
  │  phases.json:                                      │
  │    [{"name":"execute","skill":"execute.md"}]       │
  │                                                    │
  │  Fresh LLM context — no graph data from Stage 1   │
  │  Only loads PLAN.md + current file being changed   │
  │                                                    │
  └────────────────────────────────────────────────────┘
```

---

### Step 11: Meta-Skill Runs Phase 3 (Execute)

```
Meta-skill (orchestrate.md):

  Reading phases.json: 1 phase to execute

  ┌─── Phase 3: execute ─────────────────────────────┐
  │                                                   │
  │  Meta-skill loads /skills/execute.md              │
  │  Goose reads PLAN.md from workspace               │
  │  Goose transforms files in dependency order:      │
  │    1. pom.xml (build system)                      │
  │    2. persistence.xml → application.properties    │
  │    3. Entity classes (javax → jakarta)            │
  │    4. Service classes (@Stateless → @AppScoped)   │
  │    5. REST endpoints (JAX-RS → Quarkus REST)      │
  │  Goose writes:                                    │
  │    /workspace/coolstore/execution-log.md          │
  │                                                   │
  │  Meta-skill checks: execution-log.md exists? YES  │
  │  Phase 3 complete ✓                               │
  └───────────────────────────────────────────────────┘
         │
         ▼
  Meta-skill writes stage-status.json:
    {"stage": "execution", "status": "complete"}
```

---

### Step 12: Controller Records Stage 2, Moves to Stage 3

```
Controller:
  1. Watches Pod status → Pod "Succeeded"
  2. Reads stage-status.json from PVC
  3. Records result in AgentPlanExecution CR:
       status.stages[1]:
         name: execution
         status: complete
         phases: [{execute: success}]
  4. Tears down Sandbox B
  5. Writes NEW phases.json to PVC for Stage 3
  6. Moves to Stage 3: verification
  7. Reads agentRef: verification-worker
  8. Looks up Agent CR → gemini-2.5-pro (powerful, for debugging)
  9. Looks up LLMProvider → gcp-vertex
```

---

### Step 13: Controller Creates Sandbox C

```
Sandbox C (Pod):
  ┌────────────────────────────────────────────────────┐
  │                                                    │
  │  Container: migration-harness:latest               │
  │  Model: gemini-2.5-pro (powerful, 1M context)      │
  │                                                    │
  │  ImageVolumes:                                     │
  │    /skills/verify.md                               │
  │    /skills/fix.md                                  │
  │    /skills/javaee-quarkus.md   (rule)              │
  │    /meta-skill/orchestrate.md                      │
  │                                                    │
  │  /workspace ← same PVC (transformed code is here) │
  │                                                    │
  │  phases.json:                                      │
  │    [{"name":"verify","skill":"verify.md"},         │
  │     {"name":"fix","skill":"fix.md"}]               │
  │                                                    │
  └────────────────────────────────────────────────────┘
```

---

### Step 14: Meta-Skill Runs Phases 4 and 5 (Verify + Fix)

```
Meta-skill (orchestrate.md):

  Reading phases.json: 2 phases to execute

  ┌─── Phase 4: verify ──────────────────────────────┐
  │                                                   │
  │  Meta-skill loads /skills/verify.md               │
  │  Goose runs: mvn clean compile                    │
  │  Result: 23 compilation errors                    │
  │  Goose writes:                                    │
  │    /workspace/coolstore/verification-report.md    │
  │                                                   │
  │  Meta-skill checks: verification-report.md? YES   │
  │  Phase 4 complete ✓ (errors found, fix needed)    │
  └───────────────────────────────────────────────────┘
         │
         │ same Sandbox — error context stays in memory
         │ goose knows exactly what failed
         ▼
  ┌─── Phase 5: fix ─────────────────────────────────┐
  │                                                   │
  │  Meta-skill loads /skills/fix.md                  │
  │  Goose reads verification-report.md               │
  │  Fix iteration 1:                                 │
  │    Fixes 15 import errors (javax → jakarta)       │
  │    mvn clean compile → 8 errors remaining         │
  │  Fix iteration 2:                                 │
  │    Fixes 6 annotation errors                      │
  │    mvn clean compile → 2 errors remaining         │
  │  Fix iteration 3:                                 │
  │    Fixes 2 config issues                          │
  │    mvn clean compile → BUILD SUCCESS              │
  │  Goose writes:                                    │
  │    /workspace/coolstore/metrics.json              │
  │    /workspace/coolstore/execution-log.md (updated)│
  │                                                   │
  │  Meta-skill checks: BUILD SUCCESS? YES            │
  │  Phase 5 complete ✓                               │
  └───────────────────────────────────────────────────┘
         │
         ▼
  Meta-skill writes stage-status.json:
    {"stage": "verification", "status": "complete",
     "phases": [
       {"name": "verify", "status": "success", "errors_found": 23},
       {"name": "fix", "status": "success", "errors_remaining": 0,
        "fix_iterations": 3}
     ]}
```

---

### Step 15: Controller Records Stage 3, Marks Execution Complete

```
Controller:
  1. Watches Pod status → Pod "Succeeded"
  2. Reads stage-status.json from PVC
  3. Records result in AgentPlanExecution CR:
       status.stages[2]:
         name: verification
         status: complete
         phases: [{verify: success, errors_found: 23},
                  {fix: success, errors_remaining: 0, fix_iterations: 3}]
  4. Tears down Sandbox C
  5. All 3 stages recorded in CR as complete
  6. Updates AgentPlanExecution CR status: COMPLETE
  7. Developer gets notification: "Migration finished"

AgentPlanExecution CR (final state — the permanent record):
  status:
    overall: complete
    stages:
      - name: analysis,      status: complete, phases: [detect ✓, plan ✓]
      - name: execution,     status: complete, phases: [execute ✓]
      - name: verification,  status: complete, phases: [verify ✓, fix ✓]

Results on workspace PVC:
  /workspace/coolstore/
    detect.json
    graph.json
    GRAPH_REPORT.md
    PLAN.md
    execution-log.md
    verification-report.md
    metrics.json
```

---

### Summary: What Touched What

```
Step   Component        CRDs Read/Written            Action                          PVC Files
────   ─────────        ────────────────             ──────                          ─────────
1      Developer        AgentPlanExecution (create)   Triggers AgentPlan              —
2      Controller       AgentPlan (read)              Reads stages                    —
3      Controller       Agent: planning-worker (read) Reads blueprint                 —
4      Controller       LLMProvider: gcp-vertex (read)Gets endpoint + creds           —
5      Controller       SkillCards (read)             Creates Sandbox A               writes phases.json
6      Controller       —                            Runs goose + meta-skill         —
7      Meta-skill       —                            Drives Phase 1 (detect)         writes detect.json, graph.json
7      Meta-skill       —                            Drives Phase 2 (plan)           writes PLAN.md
7      Meta-skill       —                            Phases done                     writes stage-status.json
8      Controller       AgentPlanExecution (update)   Reads status → records in CR    reads stage-status.json
8      Controller       —                            Kills Sandbox A                 overwrites phases.json
9      Controller       Agent: execution-worker (read)Reads blueprint                 —
10     Controller       LLMProvider: litmaas (read)   Gets endpoint + creds           —
10     Controller       SkillCards (read)             Creates Sandbox B               writes phases.json
11     Meta-skill       —                            Drives Phase 3 (execute)        writes execution-log.md
11     Meta-skill       —                            Phases done                     writes stage-status.json
12     Controller       AgentPlanExecution (update)   Reads status → records in CR    reads stage-status.json
12     Controller       —                            Kills Sandbox B                 overwrites phases.json
12     Controller       Agent: verify-worker (read)   Reads blueprint                 —
13     Controller       LLMProvider: gcp-vertex (read)Gets endpoint + creds           —
13     Controller       SkillCards (read)             Creates Sandbox C               writes phases.json
14     Meta-skill       —                            Drives Phase 4 (verify)         writes verification-report.md
14     Meta-skill       —                            Drives Phase 5 (fix)            writes metrics.json
14     Meta-skill       —                            Phases done                     writes stage-status.json
15     Controller       AgentPlanExecution (update)   Reads status → records in CR    reads stage-status.json
15     Controller       AgentPlanExecution (update)   Marks COMPLETE                  —
15     Controller       —                            Kills Sandbox C                 —
```

### Communication Pattern

```
PVC is a transient message channel, CR is the permanent record:

  Meta-skill ──writes──► stage-status.json (PVC)
                              │
  Controller ──reads───►──────┘
                              │
  Controller ──records──► AgentPlanExecution CR (permanent)
                              │
  Controller ──overwrites──► phases.json (PVC) for next stage
                              │
  Next Sandbox ──reads──►─────┘

  stage-status.json gets overwritten each stage — that's fine
  phases.json gets overwritten each stage — that's expected
  The CR holds the full history of all stages
```

---

## How This Compares to Other Agent Frameworks

The core pattern is universal across all LLM agent systems:

| Konveyor | LangChain | CrewAI | OpenAI Agents SDK |
|---|---|---|---|
| SkillCard | Tool | Tool | Tool/Function |
| Agent CR | Agent | Agent | Agent |
| AgentPlan | Chain/Graph | Crew | Swarm |
| Meta-skill | AgentExecutor | Process | Runner |
| Controller | Runtime | Kickoff | Orchestrator |
| LLMProvider | LLM binding | LLM config | Model config |
| Sandbox | Process/Thread | Task context | Thread |

Every agent system answers the same 5 questions:

| Question | Answer |
|---|---|
| What can the agent do? | Skills / Tools |
| What brain does it use? | LLM Provider / Model |
| How does it run? | Container image / Runtime |
| What's the work order? | AgentPlan / Crew / Chain |
| Who manages lifecycle? | Controller / Runtime |

### What Makes the Konveyor Approach Different

Most agent frameworks run in a single process on a single machine:

```
Typical agent framework:
  Python process
    └── Agent 1 (thread)
    └── Agent 2 (thread)
    └── Agent 3 (thread)
    └── shared memory
```

Konveyor runs agents as Kubernetes workloads:

```
Konveyor:
  Cluster
    └── Sandbox A (Pod) — Agent 1
    └── Sandbox B (Pod) — Agent 2
    └── Sandbox C (Pod) — Agent 3
    └── shared workspace PVC
```

That gives you things no in-process framework can:

- **Isolation** — agent crashes don't take down other agents
- **Different images** — Java agent and Go agent run different containers
- **Scaling** — run 10 migrations in parallel on 10 Pods
- **Recovery** — Pod dies, controller restarts just that stage
- **Resource limits** — each agent gets its own CPU/memory
- **Security** — network isolation, RBAC per agent

The **pattern** is how all LLM agents are built. The **innovation** is running it on Kubernetes with CRDs, Sandboxes, and ImageVolumes instead of in a Python process. That's what makes it an **agentic platform** rather than an **agent framework**.

---

## What Changes, What Stays

| | CLI (today) | Agentic Platform (tomorrow) |
|---|---|---|
| **Steps** | Shell functions in `bin/migration-harness` | SkillCards (OCI artifacts) |
| **References** | `recipes/*.md` files on disk | SkillCollection (OCI + git) |
| **Model config** | `~/.migration-harness/config` | LLMProvider CR |
| **Container** | One Dockerfile, all languages | User brings their own image |
| **Pipeline** | Hardcoded 5-step loop | AgentPlan CR (customizable stages) |
| **Execution** | `oc apply -f pod.yaml` | Controller reconciles AgentPlan |
| **Recovery** | `migration-harness resume` | Controller restarts failed stage |
| **Skills delivery** | Baked into image | Mounted via ImageVolumes at runtime |
| **Orchestration** | Shell script (`bin/migration-harness`) | Controller + meta-skill |

The migration logic stays the same. The packaging and orchestration become Kubernetes-native.

---

## What Exists Today vs What Gets Built

| Piece | Status | Where |
|---|---|---|
| AgentPlan YAML format | Defined | CRD spec in the ADR |
| Controller | **Not yet** | Next step in roadmap |
| Sandbox management | Upstream | kubernetes-sigs/Agent Sandbox |
| Skill packaging/mounting | Upstream | skillimage.dev + ImageVolumes |
| Meta-skill (orchestrate) | **Not yet** | Needs to be built |
| Agent runtime (goose) | Exists | Block's goose |
| Migration skills | **Exists** | migration-harness skill-bundle |
| Reference catalogs | **Exists** | migration-harness recipes |
| Migration pipeline logic | **Exists** | migration-harness bin/lib |
