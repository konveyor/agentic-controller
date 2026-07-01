# ADR 0001: Agentic Platform CRD Architecture

**Status:** Accepted
**Date:** 2026-06-01
**Updated:** 2026-06-29
**Authors:** David Zager, Dylan Murray

## Context

Konveyor's POC for AI-powered code migration (tackle2-addon-kai) proved
that an AI agent running in a container can clone a repo, fetch analysis
results from Hub, apply migration skills, and push a branch. The POC
used Hub's addon framework to launch the agent container and pallet to
sync skills at runtime.

To move toward a release we need to simplify the architecture and
deliver domain-agnostic building blocks that are not coupled to Hub's
task lifecycle. The goals are:

1. Define the core primitives as Kubernetes CRDs so they are portable,
   GitOps-friendly, and not tied to Hub's API.
2. Align with the skillimage project (redhat-et) for skill packaging
   and distribution rather than inventing parallel concepts.
3. Leverage Agent Sandbox (k8s-sigs) for execution rather than
   building bespoke pod management.
4. Keep Hub as a valuable data service (analysis results, app metadata)
   without requiring it as the workload launcher.
5. Support multiple agent runtimes (goose, opencode, and future
   options) without baking runtime-specific concepts into the CRDs.
6. Keep CRDs domain-agnostic — the primitives work for migration,
   code review, infrastructure automation, or any future agentic
   workload. The controller has no dependency on Hub or any
   inventory system.

## Decision

### Seven CRDs

We introduce seven Custom Resource Definitions. The first two adopt
skillimage's existing YAML format as their CRD spec, aligning our
Kubernetes API with the emerging OCI skills ecosystem.

**Definition resources** (templates, inert until executed):

| CRD | Purpose |
|-----|---------|
| **SkillCard** | Individual skill or rule. Adopts skillimage.io/v1alpha1 SkillCard format. A `type: rule` field distinguishes always-loaded constraints from on-demand skills. Supports three source types: OCI image ref, git source, or inline markdown — all resolve to an OCI artifact. |
| **SkillCollection** | Group of skills. Each entry references a skill by OCI image ref, git source, or SkillCard CR name. |
| **LLMProvider** | LLM service endpoint, credentials (Secret ref), and available models with context window sizes and optional tier labels. |
| **Agent** | Template declaring what is available for execution. References one or more LLMProviders, SkillCards, SkillCollections, a container image, a prompt, and declares typed parameters (inputs the AgentRun must supply). Does not select a specific model — model selection happens at execution time. Analogous to a Tekton Task. |
| **AgentPlaybook** | Ordered sequence of stages. Each stage references an Agent and carries instructions. Stages execute sequentially with fresh agent sessions. Cross-stage continuity is through git branch content. |

**Execution resources** (created to trigger work):

| CRD | Purpose |
|-----|---------|
| **AgentRun** | Execute a single Agent with specific values. References an Agent, selects models, supplies parameter values, carries instructions, and may include additional `env` and `envFrom` entries passed through to the Sandbox. The controller validates params against the Agent's declarations, creates a Sandbox, and tracks status. Analogous to a Tekton TaskRun. |
| **AgentPlaybookRun** | Execute an AgentPlaybook. Creates AgentRun CRs sequentially per stage, all sharing the same target branch. Each stage reads the previous stage's committed handoff files. |

### Naming alignment with skillimage

SkillCard and SkillCollection are skillimage's existing YAML metadata
formats. By adopting them as CRD kinds rather than inventing AgentSkill
and AgentRule, we:

- Align our Kubernetes API with the OCI skills ecosystem.
- Make a SkillCard file on a developer's laptop the same shape as a
  SkillCard CR in the cluster.
- Position Konveyor to contribute the CRD definitions upstream to
  skillimage as their Kubernetes story.
- Contribute `type: rule` support upstream for the skill-vs-rule
  distinction.

### Skill and rule packaging via skillimage

Skills and rules are packaged as OCI artifacts using
redhat-et/skillimage (`skillctl build`, `skillctl push`). This gives
us:

- Distribution through any OCI registry (Quay, Harbor, GHCR).
- Supply chain security (cosign signing, SLSA provenance, SBOMs).
- Kubernetes-native mounting via ImageVolumes (K8s 1.33+ / OpenShift
  4.20+) — read-only, cached by kubelet, no init container.
- A local development story (`skillctl install --target goose`) using
  the same artifacts.
- Lifecycle management (draft → testing → published → deprecated →
  archived).

### Skill mounting: one directory

All skills live at `/opt/skills/{skillName}/SKILL.md`. Built-in
skills are baked into the base image at build time. ImageVolume-
mounted skills are mounted into subdirectories alongside them:

```
/opt/skills/
  konveyor-migration/SKILL.md   ← baked into image
  javax-rules/SKILL.md          ← ImageVolume from SkillCard
  custom-rules/SKILL.md         ← ImageVolume from SkillCard
```

`skillctl build` puts `SKILL.md` at the root of the OCI image.
Mount at `/opt/skills/{name}` → it lands at the right path. No
symlinks, no fan-out, no entrypoint discovery logic needed.

The agent runtime points at `/opt/skills/` and discovers all
skills regardless of source.

### UI access to CRDs via Hub passthrough

All seven resources are Kubernetes CRDs. The UI accesses them through
Hub's existing service passthrough pattern (`ServiceHandler.Forward`),
which proxies requests to the Kubernetes API server using Hub's
service account token. Hub provides RBAC scope checking on the way
in. This avoids adding a separate `/k8s` proxy route to the UI
server and keeps all UI traffic flowing through Hub.

The passthrough approach is adopted for the POC. The long-term API
surface (whether Hub should expose a more curated API for agent
resources, or continue with the passthrough) is left for future
investigation. Exposing raw Kubernetes API responses through Hub
has trade-offs (k8s API conventions differ from Hub's REST
conventions, privilege escalation risk via Hub's SA, joining agent
data with Hub application data requires cross-plane queries) that
need further evaluation.

### Agent as template, AgentRun as invocation

The Agent declares what the execution needs. The AgentRun supplies
concrete values. The controller validates and passes them through.

**Agent declares:**
- `params` — typed parameters (name, type, description, default)
- `providers` — available LLM providers and models
- `skillCards`, `skillCollections` — available skills
- `image` — container image with runtime and toolchains
- `prompt` — standing instructions

**AgentRun supplies:**
- `params` — values for each declared parameter
- `models` — specific provider/model selections
- `instructions` — what to do in this run
- `env`, `envFrom` — additional env vars and secret refs passed
  through to the Sandbox as raw Kubernetes primitives

The controller does not interpret parameter values. It injects them
as environment variables (`KONVEYOR_PARAM_{NAME}`) into the Sandbox.
Git URLs, Hub tokens, MCP server addresses — these are all just
parameter values to the controller. The harness and agent inside
the Sandbox know what to do with them.

This follows the Tekton Task/TaskRun pattern: the Task declares
inputs, the TaskRun supplies values, the controller creates a Pod.

### Execution via Agent Sandbox

Agent workloads run as Agent Sandbox resources
(kubernetes-sigs/agent-sandbox). Agent Sandbox is a hard dependency —
pinned version with CI contract test. Each Sandbox is one pod with
one container image.

- **AgentRun → Sandbox:** Each AgentRun creates a Sandbox CR. The
  Sandbox's `podTemplate.spec` is a full Kubernetes PodSpec, which
  supports `image` volume types (K8s 1.33+ / OpenShift 4.20+) for
  mounting skill OCI artifacts at `/opt/skills/{name}/`. LLM
  credential Secrets, params as env vars, user-specified `env` and
  `envFrom` are passed through, along with the Agent's container
  image.
- **EmptyDir workspace:** The workspace is ephemeral. The harness
  clones the git repo, the agent works, the harness commits and
  pushes. When the pod dies, the EmptyDir is gone. All durable
  state is in git.
- **Future: SandboxTemplate + SandboxWarmPool + SandboxClaim:**
  Pre-allocated agent pods for running workloads at fleet scale.
  SandboxTemplate defines the pod shape, SandboxWarmPool pre-warms
  instances, and SandboxClaim binds a run to a warm Sandbox. Note:
  the SandboxClaim API does not yet support `envFrom` — this is a
  dependency on Agent Sandbox API evolution.

### Git-as-persistence

Workspaces are ephemeral. Git is the persistence layer.

The harness (entrypoint in the base image) manages the full git
lifecycle:

1. Harness reads git credentials from mounted Secret
2. Clone the source repo into `/workspace/repo` (harness uses
   credentials)
3. Configure workspace remote without credentials (agent cannot
   push)
4. Create or checkout the target branch
5. Agent works, harness commits incrementally
6. On exit: commit `.konveyor/handoff.md` and
   `.konveyor/session.json` to the branch
7. Harness pushes to the target repo using credentials
8. Write `/.konveyor/results.json` (pod-local, read by controller)

The agent does not receive git credentials in its environment
variables or git configuration. The workspace remote is configured
without authentication — `git push` from the agent will fail. The
harness reads credentials from a mounted Secret and performs all
push operations on the agent's behalf.

Note: in the POC (Agent Sandbox, single container), the credential
exists on the container filesystem (the mounted Secret). A
determined or compromised agent could locate it. This design
reduces accidental credential exposure (logging, prompt injection
exfiltration) but does not provide full isolation. Full credential
isolation requires OpenShell (NVIDIA) filesystem policy enforcement,
which can deny reads to the Secret mount path at the kernel level.
The harness manages all credentialed git operations.

No PVCs survive between runs. Cross-stage continuity in playbooks
is through committed files on the shared branch. Parallel agents
on the same application use different branches.

### Two repos per application

The controller resolves git coordinates from the application
inventory (Hub, Backstage, or direct git config):

- **Source repo** — clone from (read-only credentials sufficient)
- **Assets repo** — push results to (write credentials required)

If no assets repo is configured, the harness pushes to the source
repo on a `konveyor/<run-name>` branch. This follows the pattern
established by the assets-generation enhancement and implemented
in tackle2-addon-platform.

### AgentPlaybook execution model

An AgentPlaybook is a flat sequence of stages. Each stage references
an Agent and carries instructions. The AgentPlaybookRun controller
creates AgentRuns sequentially, all targeting the same branch.

Each stage gets a fresh agent session. Cross-stage knowledge
transfer happens through committed files on the branch:

1. **Handoff files** — each stage commits `.konveyor/handoff.md`
   summarizing what was accomplished and what remains. The next
   stage's agent reads this on checkout.
2. **Playbook guide** — the playbook's guide is committed as
   `.konveyor/guide.md` before stage 1, providing ambient context.

Prompt composition at execution time:

1. Agent Prompt → standing instructions (how the agent operates)
2. Stage Instructions → specific task (what to do now)
3. `.konveyor/handoff.md` → context from previous stages (read by
   the agent as a file in the working tree)

### Future: Phases within stages

The current model uses flat stages with fresh sessions. A future
enhancement may introduce phases within stages for session
continuity — multiple phases sharing a single agent session via
PVC-backed session state (SQLite database). This would enable
tool reconfiguration between steps without losing conversation
history. This is deferred until the flat-stage model is proven.

### Per-agent memory service (Phase 4)

An Agent may reference a memory service — a persistent MCP server
that stores structured domain knowledge (patterns, pitfalls, API
mappings, diary entries). The agent reads from memory at session start
and writes discoveries at session end. Memory accumulates across all
executions of that Agent, enabling organizational learning.

A POC exists in dymurray/tackle2-addon-kai using mempalace. Memory
service integration is deferred to Phase 4 — after the core execution
model (AgentRun), playbooks (AgentPlaybook), and configurable git
strategy are proven.

### Hub as a data service

Hub is not the workload launcher. The controller does not call Hub.

**At AgentRun creation time** — the creator of the AgentRun (UI,
Backstage plugin, CLI, CI pipeline) resolves application metadata
from whatever inventory it uses and supplies git URLs, credentials,
and any other context as params and env/envFrom on the AgentRun.
The controller passes these through without interpretation.

**At agent runtime** — the agent inside the Sandbox may call Hub's
API for analysis results, application facts, etc. The creator of
the AgentRun provides Hub coordinates and credentials via params
and envFrom. The controller does not know about Hub.

This means the CRDs are fully decoupled from any inventory system.
Hub, Backstage, or a plain git URL — the controller treats them
all the same.

### Subagent delegation is a runtime concern

Modern agent runtimes (goose, opencode, Claude Code) all support
in-process subagent delegation with isolated context, scoped tools, and
per-subagent model selection. Subagent delegation is handled entirely
by the runtime inside the container — it is not modeled in the CRDs.
The runtime has access to all skills mounted in the Sandbox and can
spawn subagents as it sees fit.

### Context budget validation

SkillCards with `type: rule` are always-loaded and LLMProvider models
declare context window sizes. The system can validate that an Agent's
total rule content fits within the selected model's context window,
preventing runtime failures from context overflow. This validation is
advisory — it does not gate Agent readiness. Context budget
computation is deferred from the POC.

### Agent container image strategy

The Agent CRD's `containerImage` field determines the execution
environment — agent runtime, language toolchains, and Konveyor tools.
Skills provide knowledge (what to do); the image provides capability
(what you can do it with). This separation eliminates the need for
skills to declare system-level tool dependencies.

#### Layered image architecture

Three layers:

**Layer 1 — Base image** (`quay.io/konveyor/agent-base`):

- UBI 9
- Harness entrypoint (Go binary)
- Konveyor tools (`fetch-analysis`, `run-analysis`, `skillctl`)
- Core skills baked into `/opt/skills/`
- git, ssh, Python 3.12 (for graphify), basics
- No agent runtime — runtime is added in the next layer

**Layer 1.5 — Runtime images** (e.g., `agent-base-goose`,
`agent-base-opencode`):

- Extends base
- Adds one agent runtime binary
- One image per runtime — different release cadences, no bloat

**Layer 2 — Language images** (e.g., `agent-java-goose`):

- Extends a runtime image
- Adds language toolchains (e.g., JDK 21 + Maven)
- The agent uses the available tools to build and test code

**Layer 3 — Custom images** (built by users):

- Extends a language image
- Adds corporate-specific dependencies

```dockerfile
FROM quay.io/konveyor/agent-java-goose:latest
COPY internal-ca-bundle.pem /etc/pki/ca-trust/source/anchors/
RUN update-ca-trust
```

See the agent-base-image-composition enhancement for full details.

#### Self-describing images via labels (deferred)

A future enhancement may add machine-readable capability labels to
agent images (following the APB pattern of base64-encoded YAML in a
Docker label). This would surface available capabilities in the UI
without pulling the image. Deferred because the harness auto-detects
the runtime, and skill-to-image compatibility is enforced by
convention, not validation.

These labels are also the planned mechanism for skill-to-image
compatibility validation (e.g., preventing a Maven skill from being
composed with a Python-only image). Runtime failure mode is accepted
until image labels are implemented.

#### Air-gap and network-restricted support

Everything must be in the image at build time. Agent containers
cannot download tools or dependencies at runtime — many enterprise
environments are network-restricted or fully air-gapped. This rules
out runtime tool managers (sdkman, nvm, pyenv) as a general solution.

Skills packaged as OCI artifacts are mirrored to internal registries
using standard tooling (oc-mirror, skopeo) and mounted via
ImageVolumes from the internal registry.

#### Old Java versions in containers

The agent's use case is building and testing code, not running
production workloads. JDK container-awareness issues (cgroup memory
detection, thread pool sizing) affect long-running Java processes but
not short-lived build tools like `javac` and `mvn compile`. JDK 6
through 21 all compile code correctly in containers. JDK 8u192+ and
JDK 11+ have full cgroup awareness. Older JDKs work for builds but
would need explicit `-Xmx` flags if tests exercise memory-sensitive
code paths.

## Alternatives Considered

### Hub adapter in the controller

We considered having the controller resolve application metadata from
Hub (git URLs, credentials) via an internal adapter. The AgentRun
would carry an application ID, and the controller would call Hub's
API to resolve it.

Rejected because: it couples the controller to Hub's API and makes
the CRDs domain-specific. The Tekton model is better — the AgentRun
carries resolved values (params), and the controller passes them
through without interpretation. The UI or CLI resolves from Hub,
Backstage, or wherever before creating the AgentRun.

### Hub API resources instead of CRDs

The existing dymurray/agent branch defines AgentRecipe, AgentPlan, and
Agent as Hub API resources (`/hub/agents`, `/hub/agent-recipes`,
`/hub/agent-plans`). This couples the resources to Hub's lifecycle and
API surface. CRDs are portable, work with standard Kubernetes tooling
(kubectl, GitOps, RBAC), and can be consumed by non-Hub systems.

### Hub as temporary backend with CRD-shaped API

We considered reshaping Hub's API endpoints to match the CRD model
as a transitional step. This would require new Go models, database
migrations, and API handlers — significant work for temporary
plumbing. Talking directly to the K8s API via the UI's existing proxy
is simpler and proves the actual CRD model.

### Separate AgentSkill and AgentRule CRDs

Skills and rules have different activation semantics (on-demand vs
always-loaded). We considered separate CRDs to make this explicit.
However, skillimage's SkillCard format already supports a type field,
and using a single SkillCard CRD with `type: skill | rule` aligns
with skillimage's ecosystem. The activation semantic difference is
handled by the runtime based on the type field.

### pallet as runtime sync engine

The POC runs `pallet sync .` at container startup to fetch skills from
git repos. This adds network dependency and startup latency. OCI
artifacts via skillimage are pre-mounted by kubelet before the
container starts — faster, deterministic, and auditable via image
digests.

### Custom pod/job creation instead of Agent Sandbox

We considered having the controller or UI create bare Pods or Jobs
directly. Agent Sandbox provides lifecycle management (hibernation,
warm pools, stable identity, network isolation) that we would
otherwise need to build ourselves.

### Tekton as the orchestration layer

Tekton Tasks/Pipelines were considered for orchestrating multi-stage
agent playbooks. Deferred, not rejected: the MVP requires only
sequential stage execution, and a custom controller is simpler than
adding a Tekton dependency. The architecture does not preclude Tekton
integration later — AgentPlaybookRun could generate Tekton
PipelineRuns as an implementation detail when users need conditionals,
parallelism, retries, or supply chain signing.

### Stages with phases and session continuity

We originally designed AgentPlaybook with stages containing multiple
phases, where phases within a stage shared session state via a PVC
(SQLite database). This provided full conversation continuity within
a stage.

Rejected for MVP because: session PVC management adds significant
complexity (PVC lifecycle, runtime homogeneity constraints, SQLite
concurrent access concerns). The flat-stage model with git-based
handoff is dramatically simpler and sufficient for the discover →
implement → review workflow. Phase-within-stage session continuity
is deferred as a future enhancement.

### PVC-based workspace persistence

We considered using PVCs for workspace persistence across runs
instead of git-as-persistence. Each application would have a
workspace PVC that survives between AgentRuns.

Rejected because: PVC lifecycle management per application adds
operational complexity. Git-as-persistence eliminates this — the
workspace is ephemeral, the branch is the durable state. This model
is validated by fullsend (Red Hat/Konflux) and aligns with how
cloud agent products (Cursor, Codex) handle workspaces.

### Skill-declared tool dependencies

We considered having skills declare their system-level tool
requirements (e.g., `requires: [java, mvn, git]`) and validating at
Agent composition time that the container image satisfies them. The
agentskills.io spec has a `compatibility` field (free text, max 500
chars, experimental) but nothing machine-readable for this purpose.
skillimage's schema adds `spec.dependencies` but only for
skill-to-skill dependencies. The agentoperations/agent-registry
project defines a `SkillBOM.toolRequirements` concept but the
project is inactive (2 stars, last pushed Feb 2026).

We decided against this approach because:

- No existing spec or ecosystem project solves this problem — we
  would be inventing a dependency declaration and resolution system.
- The agentskills.io spec is intentionally lightweight and used by
  30+ clients. A `requires` field would need broad ecosystem buy-in.
- Skills provide knowledge (instructions), not executables. The
  container image is the natural place to guarantee tool
  availability.
- The image-as-contract approach (layered images with self-describing
  labels) eliminates the compatibility surface without spec changes.

A `requires` field may be worth proposing to the agentskills.io
community as the containerized agent use case grows, but it is not
needed for our architecture.

### Runtime tool installation via tool managers

We considered shipping tool managers (sdkman, nvm, pyenv) in the
base image and having the agent install the correct tool versions at
runtime based on the target application's build configuration. This
would avoid per-version images entirely.

Rejected because enterprise environments are frequently
network-restricted or air-gapped. Runtime downloads are not possible
in these environments. All tools must be pre-baked into the image at
build time. Including all supported versions of a language SDK in a
single image (e.g., JDK 8/11/17/21) achieves the same flexibility
without runtime network access.

### Per-version language images

We considered publishing separate images for each language version
(e.g., `agent-java:8`, `agent-java:11`, `agent-java:17`,
`agent-java:21`). This creates a matrix of images to maintain and
forces users to know which JDK version the target application
requires before selecting an agent — information that is often
unknown until the agent inspects the project.

A single language image with all supported versions (selected at
runtime via `JAVA_HOME`) is simpler to maintain, simpler to
document, and handles the common migration scenario where the agent
needs both the source JDK (e.g., 8) and target JDK (e.g., 21)
during the same session.

## Consequences

- **Dependency on Agent Sandbox:** The project depends on a SIG Apps
  project that is still maturing. Agent Sandbox is a hard dependency
  — pinned version with CI contract test. OpenShell (NVIDIA) layers
  on top for security hardening.
- **Dependency on skillimage:** skillimage is v0.7.2. We mitigate by
  contributing directly. Red Hat OCTO backing reduces risk. Konveyor
  is a significant consumer driving stability.
- **ImageVolume requirement:** K8s 1.33+ or OpenShift 4.20+ required.
  Older clusters would need an init container fallback.
- **Git remote availability:** Git-as-persistence means work in
  progress is lost on pod crash if the harness hasn't pushed yet.
  Mitigation: incremental push during execution, not just on exit.
- **Write credentials required:** Pushing to the assets/source repo
  requires write-capable git credentials. The same credential model
  used by tackle2-addon-platform (identity with `role: "asset"` or
  `role: "source"`).
- **Controller has no inventory dependency:** The CRDs are domain-
  agnostic. The controller does not call Hub, Backstage, or any
  inventory system. The creator of the AgentRun (UI, CLI, plugin)
  resolves application metadata and supplies it as params.
- **No Hub backend changes needed:** CRDs eliminate the need for Hub
  API endpoints, database tables, and Go models for agent resources.
- **Image maintenance burden:** The project must maintain base,
  runtime, and language-specific images. See the agent-base-image-
  composition enhancement for details.
- **No skill-to-image compatibility validation (by design):** Skills
  do not declare tool dependencies. Convention and curated images
  are the primary guardrails.
