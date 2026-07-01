# Konveyor Agentic Platform — Domain Glossary

## Core Resources

**SkillCard** — An individual agent capability or behavioral constraint,
following the skillimage.io/v1alpha1 SkillCard format. A SkillCard with
`type: skill` (default) is on-demand — only its name and description
are loaded at startup; the full content activates when the agent
invokes it. A SkillCard with `type: rule` is always-loaded — its full
content is injected into every agent turn and counts toward the LLM's
context budget. A SkillCard CR supports three source types: an OCI
image ref (pre-built artifact), a git source URL (controller clones,
builds, and pushes the OCI artifact), or inline markdown content
(controller builds and pushes). All three converge to a resolved OCI
image ref in status. Examples: "maven-migration" (skill),
"no-javax-imports" (rule).

**SkillCollection** — A group of skills, following the
skillimage.io/v1alpha1 SkillCollection format. Each entry references a
skill by OCI image ref, git source URL, or SkillCard CR name. The
controller creates SkillCard CRs for git-sourced entries and reports
readiness when all child SkillCards are resolved. An Agent references
SkillCollections to gain access to sets of related capabilities.
Examples: "konveyor-quarkus-skills" (a collection of 15 migration
skills from a git repo), "enterprise-rules" (a curated set of rules
as OCI images).

**LLMProvider** — An LLM service endpoint together with credentials and
the set of models it serves. Each model declares its context window
size and an optional tier label (e.g. premium, efficient). The context
window is used to validate that an Agent's always-loaded rules fit
within budget before execution begins.

**Agent** — A capability definition declaring what is available for
execution. References zero or more SkillCards and SkillCollections,
one or more LLMProviders (the set of providers and models available
for runs), a container image (carrying the agent runtime and language
toolchains), a prompt (standing instructions for how the agent
operates), and optionally a memory service for accumulating domain
knowledge across executions. An Agent does not select a specific
model — it declares what is available. Model selection happens at
execution time in the AgentRun. The Agent controller validates
referenced resources, computes context budget (always-loaded rule
content vs. available model context windows), and reports readiness.
Subagent delegation is a runtime concern — the agent runtime may
spawn subagents internally but this is not modeled in the CRD.

**AgentPlaybook** — A reusable playbook combining a high-level guide with
an ordered sequence of stages. Each stage groups one or more phases
that share session continuity. Each phase references its own Agent
and is an independently-executed unit of work that runs in its own
Sandbox, resuming the agent session from the previous phase via
shared persistent storage (PVC). Phases within a stage can use
different Agents (different skills, prompts) as long as they share
the same underlying runtime for session resumption. Phase boundaries
provide checkpoints — results can be inspected, a failed phase
retried, or execution stopped early. Stages start with fresh agent
context. Cross-stage continuity comes from the shared workspace PVC:
each stage updates handoff files (e.g. PLAN.md tracking what's done
and what remains) that the next stage's agent reads. The plan's guide
provides ambient context (written as a context file in the workspace)
so each agent understands where its work fits in the bigger picture.
An AgentPlaybook is a template — creating one does not execute anything.

**AgentRun** — A request to execute a single Agent with specific
selections. References an Agent (or inlines the spec), selects which
providers and models to use for this run (from the Agent's available
set), carries instructions and generic parameters (key-value pairs
injected as environment variables into the Sandbox). Each model
selection includes a role (e.g. primary, efficient) that the base
image entrypoint maps to runtime-specific configuration. The
controller validates that selected providers/models are in the
Agent's available set, creates a Sandbox, and tracks status to
completion. Parameters are domain-agnostic — the Konveyor UI knows
to populate Hub-specific params (APP_ID, HUB_BASE_URL, etc.) for
migration use cases.

**AgentPlaybookRun** — A request to execute an AgentPlaybook. References an
AgentPlaybook (or inlines the spec) and carries generic parameters,
model selections, and env/envFrom. The controller orchestrates the
execution: creates an AgentRun per stage sequentially, all sharing the
same target branch. Cross-stage continuity comes from committed handoff
files (e.g. `.konveyor/handoff.md`). Tracks per-stage status (Pending,
Running, Succeeded, Failed).

## Personas

**Platform Admin** — Creates and manages SkillCards, SkillCollections,
and LLMProviders. Responsible for what capabilities and infrastructure
are available to agents.

**Architect / PM** — Creates Agents and AgentPlaybooks. Defines the
playbook for how migrations (or other agentic work) should be
executed, which agents handle which phases, and what instructions
each phase receives.

**Developer** — Consumes Agents and AgentPlaybooks. Selects an application,
picks an Agent or AgentPlaybook, runs it, and receives a branch with
results.

## Infrastructure

**skillimage** — Red Hat Emerging Technologies project
(redhat-et/skillimage) providing OCI-based packaging and distribution
for agent skills and rules. The `skillctl` CLI builds, validates,
promotes, pushes, pulls, and installs skills. SkillCard and
SkillCollection are skillimage's YAML metadata formats — our
Kubernetes CRDs adopt the same shape. Supported install targets:
claude, cursor, windsurf, opencode, openclaw.

**Agent Sandbox** — Kubernetes SIG Apps project
(kubernetes-sigs/agent-sandbox) providing CRDs for isolated, stateful
agent workloads: Sandbox, SandboxTemplate, SandboxClaim, and
SandboxWarmPool. Single-container design. Handles pod lifecycle,
stable identity, network isolation, and warm pool pre-allocation.

**OpenShell** — NVIDIA's secure-by-design runtime for autonomous
agents. A policy enforcement and governance layer that runs on top of
Agent Sandbox. Provides kernel-level isolation, declarative YAML-based
security policies, and inference routing. Complementary to Agent
Sandbox, not a replacement.

**Hub** — The Konveyor application inventory and analysis engine. In
the agentic platform Hub serves as a data service: agents call its API
at runtime to fetch analysis results, application metadata, and git
credentials. Hub does not launch or manage agent workloads.

**Memory Service** — A persistent, queryable knowledge base owned by
an Agent, accessible via MCP. The agent reads from it at session
start and writes discoveries at session end. Accumulates domain
knowledge (patterns, pitfalls, API mappings) across executions,
enabling organizational learning. Each Agent has its own memory
service instance.

## Relationships

- A **SkillCard** resolves to an OCI artifact from one of three
  sources: OCI image ref, git source, or inline content.
- A **SkillCollection** references skills by OCI image ref, git
  source, or **SkillCard** CR name. Git-sourced entries produce child
  **SkillCard** CRs.
- An **Agent** references zero or more **SkillCards** and zero or more
  **SkillCollections**.
- An **Agent** references one or more **LLMProviders** — declaring the
  set of providers and models available for execution.
- An **AgentRun** references one **Agent** (or inlines it) and selects
  specific providers, models, and roles from the Agent's available set.
- An **AgentPlaybook** organizes work into stages; each stage contains
  one or more phases. Each phase references an **Agent** and carries
  instructions.
- An **AgentPlaybookRun** references one **AgentPlaybook** (or inlines it)
  and creates **AgentRun** CRs sequentially per phase.
- At execution time, the plan's guide is written to the workspace as a
  context file. Each phase's instructions are joined with the Agent's
  prompt to form the full task for the agent runtime.
