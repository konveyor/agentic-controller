# Durable agents and delivery of approved learnings

Research date: 2026-11-11. This note records primary-source research and a
recommendation to evaluate. It does not select a dependency, define new CRD
fields, or amend an ADR. AgentLearning remains a proposed resource;
continuing sessions are outside this proposal. Vendor uses of “agent” and “session” are not our glossary
definitions.

The proposed contract and next experiment are captured in
[ADR 0024](../adr/0024-agent-learnings.md) and its
[spike plan](../spikes/agent-learning-delivery.md). The findings
below are research evidence, not implemented guarantees. Continuing sessions
and their event-ingress contract are outside the learning-delivery proposal.

## Finding

The strongest direction for this project is a **pinned approved learning set
with hybrid delivery**: short essential guidance enters context automatically;
specialized procedures and supporting evidence remain discoverable through
files or tools. This is a recommendation from the documented patterns below,
not a measured winner for our workloads.

Approval and label matching answer **which knowledge is eligible**. They do not
answer how much text belongs in a model call. Likewise, persisting knowledge
does not ensure that a model discovers it, and supplying it does not prove that
the model followed it.

There are three separate state responsibilities:

1. **Execution progress:** accepted inputs, decisions, action intents/results,
   child references, and recovery checkpoints.
2. **Conversation continuity:** transcripts, summaries, working notes, and the
   context needed to interpret the next input.
3. **Reusable knowledge:** reviewed lessons that can apply across independent
   AgentRuns and AgentWorkflowRuns.

The proposed AgentLearning belongs to the third category. A transcript or
generated summary is not automatically an approved learning or an action receipt.

## What current implementations actually provide

### DeepAgents and LangGraph: the closest fit to our accepted runtime direction

DeepAgents documents filesystem-backed memory with configurable storage and
scope. Memory files can enter the startup system prompt; skills initially
expose descriptions and load full instructions on demand. Memory can be writable,
read-only, or consolidated between conversations. Application-managed read-only
organization knowledge is an explicit pattern. These are delivery and storage
building blocks; they do not supply our proposed approval lifecycle.
[DeepAgents memory](https://docs.langchain.com/oss/python/deepagents/memory)

LangGraph separately describes checkpointers for thread state and stores for
cross-thread data. Its in-memory saver loses checkpoints on process restart;
durability requires an appropriate persistent implementation. Selecting a memory
API is therefore insufficient to establish recovery behavior.
[LangGraph persistence](https://docs.langchain.com/oss/python/langgraph/persistence)

### Cloudflare: context assembly is explicit and can itself be persisted

Cloudflare documents full context blocks, searchable context, and on-demand
skills. `freezeSystemPrompt()` renders a prompt once; `withCachedPrompt()` can
retain it across hibernation and eviction. It stays frozen across turns until
`refreshSystemPrompt()` is called. Freezing that text does not freeze an
independently mutable search provider or select approved learning membership.
The documentation uses experimental APIs, and current source documentation has
evolving names; exact SDK compatibility needs qualification.
[Conversation state and memory](https://developers.cloudflare.com/agents/concepts/conversation-state-and-memory/)

Its Workflows integration separates the interactive Agent from durable
multi-step work, retries, and event waits, with explicit result reporting back
to the Agent. That is a useful precedent for a continuing session delegating
finite work, without requiring the session's compute to stay alive.
[Run Workflows](https://developers.cloudflare.com/agents/agent-class/run-workflows/)

### Temporal and Restate: execution recovery is separate from memory selection

Temporal's official example records model and tool Activity results for replay
while storing cross-run case notes separately in SQLite. Later Activities
retrieve those notes into context. An external effect whose result was never
recorded can still require an idempotent retry or outcome lookup. This is a
vendor demonstration, reported verified on October 26, not a deployment or
benchmark we reproduced.
[Temporal agent memory and state](https://docs.temporal.io/ai/featured/memory-state-stripe)

Restate's first-party agent reference snapshots instructions, guardrails, and
memory count at turn start, then searches descriptions and reads memory entries
on demand. Entries are mutable per-agent state; this does not pin their full
contents. Journaled model/tool effects reuse recorded results, while unrecorded
external completions remain ambiguous. This evidence comes from moving `main`,
not a release-pinned integration test.
[Restate reference architecture](https://github.com/restatedev/agent/blob/main/docs/architecture.md)

### Letta: current memory is a filesystem with automatic and selective delivery

Current Letta MemFS documentation describes Git-backed memory: root files enter
every prompt, while indexed directories are read on demand. Ordinary file tools
are the default; semantic search is optional. Git versioning provides history,
but this is mutable memory rather than our fixed consumer manifest.
[MemFS](https://docs.letta.com/concepts/memfs)

Shared memory can use organization-owned repositories whose paths and top-level
listings are advertised in context. Agents synchronize changes with Git. The
documented synchronization behavior does not establish an immutable revision set
for an entire workflow.
[Shared memory](https://docs.letta.com/concepts/shared-memory)

Letta's dreaming agents consolidate conversations into memory. Its optional
review by another agent explicitly does not require human approval. That is a
candidate-generation mechanism for our purposes, not our acceptance mechanism.
[Memory and dreaming](https://docs.letta.com/configuration/memory)

The SDK also distinguishes persistent memory/history from an active connection:
missed stream events are not replayed, and uncertain sends should be checked
against conversation history before retrying. Persistent memory alone does not
establish safe execution replay.
[Sessions and durability](https://docs.letta.com/agent-sdk/sessions)

### Mastra and OpenHands: two more useful delivery patterns

Mastra Observational Memory uses Observer and Reflector agents to compress
conversation history into observations placed in context. Optional recall can
recover original message ranges; semantic search additionally needs a vector
store. Reflection rewrites the observations. This is useful continuity with
source recall, but not immutable approved shared knowledge.
[Observational Memory](https://mastra.ai/docs/memory/observational-memory)

OpenHands combines repository `AGENTS.md` instructions in the initial prompt,
advertised skills loaded by the model, and deterministic keyword or file-path
activation. Its trigger extensions are not general Agent Skills guarantees.
This is a particularly relevant coding-agent precedent for several delivery
methods within one runtime.
[OpenHands skills](https://docs.openhands.dev/overview/skills)

## What the research supports—and what it does not

Anthropic's context-engineering guidance describes a mixture of preloaded
instructions and just-in-time retrieval. Its Claude Code example combines an
upfront instruction file with search and file access. The argument is to curate
context and make additional material discoverable, not to copy every available
document into a prompt. This is engineering guidance, not a controlled comparison
on our tasks.
[Effective context engineering](https://www.anthropic.com/research/effective-context-engineering-for-ai-agents)

Anthropic's November 6, 2026 memory research distinguishes transcripts, notes,
derived representations, and injected context. Its experiments show that memory
quality and retrieval behavior depend on the model and task. Derived wikis
helped some multi-document tasks and gave little benefit on others; one grew
larger than its source corpus. The article also illustrates correcting a bad
memory against its transcript and cautions that injected context should stay
small. These are first-party evaluations with task-specific setups, not proof
that automatic learning or a particular delivery strategy will improve Konveyor
workloads. We should preserve sources and test omission, incorrect lessons, and
irrelevant material alongside successful recall.
[The components of agent memory](https://www.anthropic.com/research/components-of-agent-memory)

Taken together, the evidence supports a layered design. It does not establish
one universal “state-of-the-art” memory product or justify selecting one by a
conversational recall score. Remembering a past fact, following a build
requirement, and safely recovering an external action are different outcomes.
This research inspected documentation and source; it ran no model benchmark or
crash-recovery experiment.

## Prompt injection and MCP are not exclusive alternatives

MCP defines communication between hosts, clients, and servers, including tools,
resources, and prompts. The host aggregates context. It does not define our
approval, immutable-version, or recovery contract.
[MCP architecture, version 2025-11-25](https://modelcontextprotocol.io/specification/2025-11-25/architecture)

For this design, an MCP server could provide learning search/read operations.
The runtime could also prefetch content through that server before invoking the
model. Retrieved tool content eventually becomes model context too. The
important choices are **who selects content, when it is loaded, and which
version is available**, independently of the access protocol.

Three strategies merit comparison:

- **All eligible text upfront:** deterministic delivery and simple auditing;
  increasing context cost and distraction as the corpus grows. Being delivered
  still does not guarantee compliance.
- **Catalog plus model-driven retrieval:** accommodates larger collections;
  depends on the model recognizing relevance and making the right reads.
  A useful lesson may never be discovered.
- **Hybrid:** explicitly load essential guidance, advertise additional
  procedures, and retrieve supporting detail. This requires a presentation
  policy and evaluation, but fits our existing runtime direction best.

These are expected tradeoffs to test, not measured performance claims.

## Recommendation for this repository

### Resolve approved membership before choosing presentation

The proposed lifecycle carried into ADR 0024 is:

- Content and applicability selector are immutable. Human acceptance covers
  that exact proposal. Labels express generic applicability; Hub supplies
  application meaning. Labels do not substitute for authorization.
- A standalone AgentRun pins its selection at creation. An AgentWorkflowRun
  pins one set at creation for all stages, including stages not yet started.
- An accepted replacement changes future selections automatically. Existing
  consumers keep their pinned revision.
- **Revocation is refused while any unfinished consumer references the
  learning.** Finishing, stopping, or cancelling consumers must actually release
  their use first. This is not a disable-now-and-drain policy. Admission and
  revocation require race-safe coordination.

Persist actual immutable identities and content digests, with retained content
or durable artifact references. Saving only a label selector or a pointer to
“latest” would allow recovery to see different knowledge. Object storage could
hold these artifacts, but would not itself supply selection, acceptance,
execution recovery, or consumer accounting.

### Give different kinds of content different treatment

For an initial experiment, I recommend:

1. **Short essential guidance:** automatically render the approved statement
   when relevant to the workload. For example, a required verification command
   should not depend on the model guessing that it should search for it.
2. **Specialized procedures:** advertise a concise description and an exact
   versioned path/tool reference; load the full procedure when applicable.
   Deterministic activation from known task metadata is also worth testing.
3. **Evidence and historical detail:** retain source links and make details
   retrievable without making the whole transcript standing instructions.

These are proposed presentation categories, not new CRD fields. We have not
decided who assigns them or the default for a newly accepted learning. If a
summary becomes the authoritative injected instruction, its wording should be
part of the approved revision rather than an unreviewed replacement generated
at startup. Detect conflicting required guidance and explicit context-budget
overflow instead of silently dropping it. For machine-checkable requirements,
runtime validation can provide evidence that prompt delivery alone cannot.

ADR 0024 proposes read-only learning snapshots exposed as files to the
runtime, with a small injected core and discoverable catalog. MCP remains an
optional access mechanism if a shared remote service or additional clients
subsequently need it. This leaves
the storage backend and future IDE/CLI access open without requiring another
service merely to read a bounded snapshot.

### Keep responsibility in the existing layers

The repo already distinguishes always-loaded rules from on-demand skills in
[CONTEXT.md](../../CONTEXT.md) and
[ADR 0014](../adr/0014-skill-loading-and-prompt-assembly.md).
[Prompt assembly](../../harness/internal/prompt/prompt.go) renders rule content;
the runtime discovers on-demand skills. That is a useful delivery precedent,
not a decision to make AgentLearning synonymous with SkillCard.

[ADR 0021](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md)
accepts reuse of Lightspeed's DeepAgents runtime while leaving production
integration unimplemented. Evaluate learning delivery at that integration seam:
the harness prepares the immutable inputs, and the runtime manages context,
retrieval, compaction, and subagent propagation. Preserve its inspection
boundaries when bringing retrieved evidence into the model or viewer.

The controller should continue to reconcile generic resources and references;
it need not interpret lessons, connect over ACP, or run semantic search. The
existing glossary's per-Agent Memory Service is a separate concept that will
need explicit reconciliation if we adopt shared, approved AgentLearnings.

### Preserve context decisions across recovery

The proposed durable sequence is: accept the input; persist its learning
manifest before its first dependent model call; prepare required context;
record retrievals and execution results; persist the completed input's state.
The exact transaction/checkpoint boundaries still require a spike.

Keep three observable records distinct:

- **Eligible:** the immutable learning revisions available to this consumer.
- **Delivered:** the text injected or returned by a retrieval, with revision
  identities and relevant prompt/runtime/retriever versions.
- **Outcome:** model/tool results and subsequent verification evidence.

An access log proves delivery, not that the model understood or obeyed a lesson.
Search must be limited to the pinned set. Record returned content before
subsequent reasoning depends on it so recovery can reuse the result rather than
search today's corpus again. Membership pinning alone does not freeze ranking,
prompt formatting, or compaction. Exact model-input retention is a separate
audit requirement to settle.

Restore required guidance after restart and context compaction, and define how
subagents receive it. Reuse completed recorded decisions rather than replaying
the entire prompt. If an external action succeeded without a saved receipt,
learning persistence does not resolve that uncertainty; the action needs its
own recovery policy, outside the scope of the learning-delivery spike.

When a session selects a replacement for the next input, explicitly present
the current revision. Older guidance may still appear in historical messages;
replacing a selection does not erase those messages. Likewise, generated
working notes must not silently overwrite accepted knowledge. Reflection or
“dreaming” can propose a new AgentLearning, with evidence, for the agreed review
path.

## What we should establish before choosing a delivery policy

Use the same model, runtime, approved corpus, and representative tasks to
compare all-upfront, retrieval-only, and hybrid delivery. Include a small corpus
where everything fits and a larger one where selection matters. Measure task
success and required-guidance adherence, missed relevant lessons, irrelevant
retrievals, prompt/tool tokens, latency, and successful access to source evidence.
Do not select an approach solely by token savings.

Include failures specific to our contract: replacement between workflow
stages; restart after selection or retrieval; compaction followed by a relevant
action; two invocations sharing knowledge without sharing scratch state; attempted
revocation during an active consumer; and an unavailable or digest-mismatched
artifact. Verify both selection stability and what the model actually receives.

The remaining design choices are the default presentation for a learning,
who can change that presentation, the behavior when essential guidance exceeds
the context budget, inheritance for independently delegated runs, and how much
exact context to retain for audit. These are narrower questions than selecting
an entire “durable agent platform.” Sleeping between completed inputs remains
compatible with this direction; no process snapshot or microVM backend is
assumed.
