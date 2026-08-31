# Konveyor Agentic Platform — Domain Glossary

## Core Resources

**SkillCard** — Exactly one agent capability or behavioral constraint.
The skill itself is an AgentSkills.io directory: a `SKILL.md` carrying
YAML frontmatter, optionally alongside supporting files. A SkillCard
with `type: skill` (default) is on-demand — only its name and
description are loaded at startup; the full content activates when the
agent invokes it. A SkillCard with `type: rule` is always-loaded — its
full content is injected into every agent turn. `type` lives on the CR,
never in skill content, which keeps `SKILL.md` valid against the Agent
Skills spec. A SkillCard names one of three sources: an OCI image ref,
a git source URL, or inline markdown. Only the image source resolves to
an artifact; inline is delivered as a ConfigMap and git is cloned at pod
start, so nothing is built in-cluster. An image or repository holding
several skills is addressed with `subPath`, since a SkillCard is always
one skill. Examples: "maven-migration" (skill), "no-javax-imports"
(rule). See ADR 0015.

**SkillCollection** — A named group of skills an Agent can reference in
one line. Each entry references a skill by SkillCard CR name, or names a
source directly. Grouping is separate from packaging: a collection may
gather skills from several images, and one image may hold several skills
that no collection groups. An Agent references SkillCollections to gain
access to sets of related capabilities. Examples:
"konveyor-quarkus-skills" (a collection of 15 migration skills),
"enterprise-rules" (a curated set of rules).

_Not yet true_: the controller does not create SkillCard CRs for entries
that name a source directly, though it likely should. Whether a
collection becomes the primary type users write, resolving a multi-skill
source into one generated SkillCard per skill, is an open question in
ADR 0015.

**Gateway** — An LLM service endpoint serving exactly one provider/model
combination. Each Gateway declares a provider type (e.g. `anthropic`,
`openai`, `gcp-vertex-ai`), an endpoint, credentials, and a single model
with context window size and optional tier label. The provider type is
injected as `KONVEYOR_LLM_PROVIDER` so the harness can map credentials
to provider-specific env vars (e.g. `ANTHROPIC_API_KEY`). This is a
pre-OpenShell shim — when OpenShell is integrated, `inference.local`
eliminates provider-specific credential mapping and the field is removed.
The controller verifies connectivity on create/update. When OpenShell
lands, the Gateway CRD is replaced by OpenShell Gateway Services — the
field names and interaction model are designed to make this swap seamless.

**Agent** — A capability definition declaring what is available for
execution. References zero or more SkillCards and SkillCollections,
one or more Gateways (each representing a provider/model combination
available for runs), a container image (carrying the agent runtime
and language toolchains), a prompt (standing instructions for how the
agent operates), and optionally a memory service for accumulating
domain knowledge across executions. An Agent does not select a
specific model — it declares what is available. Gateway selection
happens at execution time in the AgentRun. The Agent controller
validates that referenced Gateway CRs exist in the namespace and that
SkillCards/SkillCollections are ready. When OpenShell is integrated,
Gateway CRs will be replaced by OpenShell Gateway Services.
Subagent delegation is a runtime concern — the agent runtime may
spawn subagents internally but this is not modeled in the CRD.
_Avoid_: conflating Agent with AgentRun — Agent is a template,
AgentRun is an invocation.

**AgentWorkflow** — A reusable workflow combining a high-level guide with
an ordered sequence of stages. Each stage references an Agent and
carries instructions. Stages execute sequentially, each getting a
fresh agent session in its own Sandbox. Cross-stage continuity comes
from committed handoff files on the shared target branch (e.g.
`.konveyor/handoff.md`). The workflow's guide provides ambient context
written as a file in the workspace so each agent understands where its
work fits in the bigger picture. An AgentWorkflow is a template —
creating one does not execute anything. A future enhancement may
introduce phases within stages for session continuity via shared PVCs.

**AgentRun** — A request to execute a single Agent with specific
selections. References an Agent, selects which Gateway to use for
this run (from the Agent's available set), carries instructions,
generic parameters (key-value pairs), and a mode (`auto` or
`approve`). The controller validates the selected Gateway, performs
`$(scope.name)` variable substitution in prompt text fields, writes
resolved parameters and execution controls to
`/run/konveyor/params.json`, creates a sandbox, and tracks status to
completion. Parameters are domain-agnostic — the controller passes
them through without interpretation. Execution limits (`maxTurns`,
`maxCost`) are set on the Agent; mode is set on the AgentRun. For
Konveyor-managed agents, Hub injects connectivity info
(`HUB_BASE_URL`, `APP_ID`, scoped API token) into the AgentRun's
env at create time; the harness resolves application metadata from
Hub at runtime.
_Avoid_: putting execution controls (turn limits, cost budgets) in
skills or in arbitrary params — these are CRD-level concerns.

**AgentWorkflowRun** — A request to execute an AgentWorkflow. References an
AgentWorkflow (or inlines the spec) and carries generic parameters,
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

**Architect / PM** — Creates Agents and AgentWorkflows. Defines the
workflow for how migrations (or other agentic work) should be
executed, which agents handle which phases, and what instructions
each phase receives.

**Developer** — Consumes Agents and AgentWorkflows. Selects an application,
picks an Agent or AgentWorkflow, runs it, and receives a branch with
results.

## Infrastructure

**Agent Skills** — The open skill format at agentskills.io, originally
from Anthropic and adopted across agent clients. A skill is a directory
holding a `SKILL.md` with YAML frontmatter (`name` and `description`
required, plus `license`, `compatibility`, `metadata` and
`allowed-tools`), optionally alongside `scripts/`, `references/` and
`assets/`. The field set is closed, and the `skills-ref` reference
library validates against it. Our skills are this format, and our
SkillCard and SkillCollection CRDs keep their names from skillimage
without adopting its packaging. See ADR 0015.

**Agent Sandbox** — Kubernetes SIG Apps project
(kubernetes-sigs/agent-sandbox) providing CRDs for isolated, stateful
agent workloads: Sandbox, SandboxTemplate, SandboxClaim, and
SandboxWarmPool. Single-container design. Handles pod lifecycle,
stable identity, network isolation, and warm pool pre-allocation.
In the current implementation, the controller creates and watches Sandbox
CRs directly. OpenShell is the deferred execution backend described by ADR
0004; ADR 0016 records the accepted interim path.

**OpenShell** — NVIDIA's secure-by-design runtime for autonomous
agents. It is the intended future execution backend and runs on top of
Agent Sandbox. It is not the current controller execution interface:
today, the controller creates Sandbox CRs directly and uses the Gateway
CRD as the pre-OpenShell provider/model configuration shim (ADR 0016).
The intended OpenShell end state adds the gateway supervisor, policy
enforcement, privacy-proxy credential injection, and `inference.local`.
_Avoid_: describing that deferred end state as the current implementation.

**OpenShell Gateway** — An instance of the OpenShell control plane
deployed as a Kubernetes Service in the deferred OpenShell design. Each
OpenShell Gateway serves one provider/model combination via
`inference.local`. The current implementation instead uses the namespaced
Gateway CRD, which the controller verifies with a Job and whose
provider-specific credentials are injected into the Sandbox environment.
_Avoid_: gateway (lowercase) when referring to Kubernetes Gateway API
resources — always capitalize when referring to an OpenShell Gateway.

**Hub** — The Konveyor application inventory and analysis engine. In
the agentic platform Hub serves two roles: (1) a CRUD gateway for
agent CRDs, exposing REST endpoints under `/hub/agent/` backed by
controller-runtime client — following the AddonHandler/ConfigMapHandler
pattern; and (2) a runtime data service that the harness calls (via a
scoped API token) to fetch application metadata, decrypted git
credentials, and analysis results — the same role Hub plays for
addons today. At AgentRun create time, Hub mints a scoped token and
injects `HUB_BASE_URL`, `APP_ID`, the token (`HUB_TOKEN`), and the
token's database ID (`HUB_TOKEN_ID`) into the AgentRun's env/envFrom,
then creates the CR. Hub does not resolve application
data at create time — the harness resolves at runtime. Hub is
fire-and-forget; it does not launch or manage agent workloads.

**Harness** — The Go binary entrypoint in the agent base image,
analogous to the addon adapter (`shared/addon/adapter`) in Hub. The
harness reads its configuration from `/run/konveyor/params.json`
(written by the controller) and environment variables. In
Konveyor-managed mode (`HUB_BASE_URL` + `APP_ID` set), the harness
acts as a Hub client: resolves the application's git URL, branch,
and decrypted credentials from Hub, clones the repo, and configures
the workspace so the agent cannot push (credentials stay in the
harness, not in the agent's env or git config). On exit, the harness
revokes its Hub API token — except in workflow stages where stages
share a token; the harness revokes only on the last stage (determined
via `KONVEYOR_WORKFLOW_STAGE` / `KONVEYOR_WORKFLOW_STAGE_COUNT` env
vars injected by the controller). The harness translates runtime-agnostic CRD values (mode, maxTurns)
to runtime-specific configuration (e.g. `GOOSE_MAX_TURNS`,
`GOOSE_MODE` env vars). It monitors ACP `usage_update` notifications
for cumulative cost and enforces cost limits via cancel-then-handoff.
Turn limits are enforced by the runtime natively; the harness
reserves 15-20% of the turn budget for a handoff prompt when the
runtime stops. On exit, the harness writes usage data to
`/dev/termination-log` as opaque JSON; the controller copies it to
`AgentRunStatus.terminationData` without interpretation.
In both modes, the harness launches the agent
runtime and pushes to the target branch on exit. The agent commits
locally; the harness pushes. The harness is domain-specific — other
platforms can provide their own harness for their use case. The
controller is agnostic to which harness the base image carries. The
`params.json` file shape and execution control semantics form the
harness contract — any harness implementation must honor them.
_Avoid_: putting execution control in skills — skills contain
knowledge and judgment; the harness controls how long, how much, and
in what mode the agent runs.

**Memory Service** — A persistent, queryable knowledge base owned by
an Agent, accessible via MCP. The agent reads from it at session
start and writes discoveries at session end. Accumulates domain
knowledge (patterns, pitfalls, API mappings) across executions,
enabling organizational learning. Each Agent has its own memory
service instance.

## Execution Concepts

**Mode** — Supervision policy for an agent execution: `auto`
(all tool calls approved automatically, headless-safe) or `approve`
(tool calls require explicit human approval via the ACP tee).
Defaults to `auto`. Set on AgentRun for standalone runs and on
individual AgentWorkflow stages for workflow runs. When `approve` is
set with no viewer attached, the tee's fail-closed policy denies all
tool calls. Who can set mode is an authorization concern owned by
Hub/UI, not the controller.
_Avoid_: `smart_approve` (goose-specific, functionally identical to
`approve` for agents that write files and run commands);
`interactive`/`non-interactive` (use `approve`/`auto` instead).

**Execution Limits** — Optional budget constraints on an agent
execution: `maxTurns` (tool-call turns) and `maxCost` (cumulative
USD). Set on Agent as defaults, overrideable per stage and per run.
Whichever limit is hit first triggers wind-down. The harness reserves
15–20% of the budget for a handoff prompt — when the primary budget
is exhausted, the harness cancels the current work and sends a final
prompt for the agent to write a handoff. Only standard ACP
`usage_update` data (`used`, `size`, cumulative `cost`) is used for
enforcement. `maxTokens` is intentionally excluded — the ACP
`usage_update` reports context-window occupancy, not cumulative
consumption, making it unsuitable as a budget metric.
_Avoid_: encoding limits in skills as LLM instructions (e.g.
`MAX_FIX_ITERATIONS`) — limits are harness concerns, not skill
concerns.

**Skill Content Boundary** — A skill contains knowledge and judgment
criteria. It never contains execution control. Allowed: domain
knowledge, approach guidance, quality criteria, output format,
judgment calls ("if stuck, write a handoff"). Not allowed: reading
env vars, counting iterations, branching on exit codes, running
infrastructure tools, git operations, filesystem discovery.
_Avoid_: telling the LLM to read parameters from the environment or
count its own iterations — if a program could do it deterministically,
it belongs in the harness.

## Relationships

- A **SkillCard** is one skill, from one of three sources: OCI image
  ref, git source, or inline content. Only the image source resolves to
  an artifact; the others are delivered without one.
- A **SkillCollection** groups skills, by **SkillCard** CR name or by
  naming a source directly.
- An **Agent** references zero or more **SkillCards** and zero or more
  **SkillCollections**.
- An **Agent** references one or more **Gateways** — declaring the
  set of provider/model combinations available for runs.
- An **AgentRun** references one **Agent** and selects one **Gateway**
  from the Agent's available set.
- An **AgentWorkflow** organizes work into stages. Each stage
  references an **Agent** and carries instructions.
- An **AgentWorkflowRun** references one **AgentWorkflow** (or inlines it)
  and creates **AgentRun** CRs sequentially per stage.
- At execution time, the plan's guide is written to the workspace as a
  context file. Each stage's instructions are joined with the Agent's
  prompt to form the full task for the agent runtime.
