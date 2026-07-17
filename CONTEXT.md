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

**LLMProvider** — *Deprecated, to be removed.* Replaced by OpenShell
gateway inference routing. See ADR-0004.

**Agent** — A capability definition declaring what is available for
execution. References zero or more SkillCards and SkillCollections,
one or more OpenShell gateways (each representing a provider/model
combination available for runs), a container image (carrying the
agent runtime and language toolchains), a prompt (standing
instructions for how the agent operates), and optionally a memory
service for accumulating domain knowledge across executions. An Agent
does not select a specific model — it declares what is available.
Gateway selection happens at execution time in the AgentRun. The
Agent controller validates that referenced gateways exist as Services
in the namespace and that SkillCards/SkillCollections are ready.
Subagent delegation is a runtime concern — the agent runtime may
spawn subagents internally but this is not modeled in the CRD.
_Avoid_: conflating Agent with AgentRun — Agent is a template,
AgentRun is an invocation.

**AgentPlaybook** — A reusable playbook combining a high-level guide with
an ordered sequence of stages. Each stage references an Agent and
carries instructions. Stages execute sequentially, each getting a
fresh agent session in its own Sandbox. Cross-stage continuity comes
from committed handoff files on the shared target branch (e.g.
`.konveyor/handoff.md`). The playbook's guide provides ambient context
written as a file in the workspace so each agent understands where its
work fits in the bigger picture. An AgentPlaybook is a template —
creating one does not execute anything. A future enhancement may
introduce phases within stages for session continuity via shared PVCs.

**AgentRun** — A request to execute a single Agent with specific
selections. References an Agent, selects which OpenShell gateway to
use for this run (from the Agent's available set), carries
instructions and generic parameters (key-value pairs injected as
environment variables into the sandbox). The controller validates
that the selected gateway is in the Agent's gateway list and that the
gateway Service exists, creates a sandbox through the OpenShell
gateway API, and tracks status to completion. Parameters are
domain-agnostic — the Konveyor UI knows to populate Hub-specific
params (APP_ID, HUB_BASE_URL, etc.) for migration use cases.

**AgentPlaybookRun** — A request to execute an AgentPlaybook. References an
AgentPlaybook (or inlines the spec) and carries generic parameters,
a gateway selection, and env/envFrom. The controller orchestrates the
execution: creates an AgentRun per stage sequentially (each using the
selected gateway), all sharing the same target branch. Cross-stage
continuity comes from committed handoff files (e.g.
`.konveyor/handoff.md`). Tracks per-stage status (Pending, Running,
Succeeded, Failed).

## Personas

**Platform Admin** — Creates and manages SkillCards, SkillCollections,
and OpenShell gateways. Deploys gateways via Helm, configures
providers and inference routing on each gateway via the OpenShell CLI.
Responsible for what capabilities and infrastructure are available to
agents.

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
The controller does not interact with Agent Sandbox directly —
OpenShell manages Sandbox CRs on our behalf.

**OpenShell** — NVIDIA's secure-by-design runtime for autonomous
agents. Runs on top of Agent Sandbox. The controller's primary
execution interface — sandboxes are created through the OpenShell
gateway API (via the Go SDK), not by creating Sandbox CRs directly.
Each gateway is deployed via Helm as a Kubernetes Service, configured
with one provider and one model. The gateway's supervisor is
sideloaded into sandbox pods and provides: kernel-level isolation,
declarative YAML-based security policies, credential injection via
privacy proxy, and inference routing through `inference.local`. The
agent inside the sandbox calls `inference.local` and never sees real
LLM credentials. Providers, inference routes, and policies are
configured on the gateway by the Platform Admin — they are not
Kubernetes CRDs.
_Avoid_: treating OpenShell as optional — it is a hard dependency
for sandbox creation, replacing the direct Agent Sandbox dependency.

**OpenShell Gateway** — An instance of the OpenShell control plane
deployed as a Kubernetes Service. Each gateway serves exactly one
provider/model combination via `inference.local`. Platform Admin
deploys multiple gateways (one per provider/model combo) into the
shared namespace via Helm. All gateways must live in the same
namespace as the controller and Hub — OpenShell's gateway, sandbox
pods, TLS Secrets, and database are all namespace-scoped. An Agent
references one or more gateways; an AgentRun selects one. The
controller connects to the gateway using the OpenShell Go SDK and
the gateway's client TLS credentials.
_Avoid_: gateway (lowercase) when referring to Kubernetes Gateway API
resources — always capitalize when referring to an OpenShell Gateway.

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
- An **Agent** references one or more **OpenShell Gateways** —
  declaring the set of provider/model combinations available for runs.
- An **AgentRun** references one **Agent** and selects one **OpenShell
  Gateway** from the Agent's available set.
- An **AgentPlaybook** organizes work into stages. Each stage
  references an **Agent** and carries instructions.
- An **AgentPlaybookRun** references one **AgentPlaybook** (or inlines it)
  and creates **AgentRun** CRs sequentially per stage.
- At execution time, the plan's guide is written to the workspace as a
  context file. Each stage's instructions are joined with the Agent's
  prompt to form the full task for the agent runtime.
