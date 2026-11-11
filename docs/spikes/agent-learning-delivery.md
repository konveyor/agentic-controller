# Spike: AgentLearning for existing runs and workflows

Status: proposed, 2026-11-11. Supports
[ADR 0024](../adr/0024-agent-learnings.md). No experiments described here have run.

This experiment requires neither AgentSession nor a webhook/event-routing system.

## Question

Can existing finite invocations consume a reproducible set of reviewed lessons
through memory files, with race-safe lifecycle accounting and useful delivery
evidence?

## Demonstration

Seed one short verification instruction, one longer procedure, a rejected
proposal, and a learning that does not match the workload. Share an accepted
lesson across two workload label sets, including different Agent templates.
Create a queued standalone AgentRun and a two-stage AgentWorkflowRun, then
accept a replacement before their pods/stages start.

Both invocations retain their creation-time revisions; all workflow stages use
the same set. A newly created run selects the replacement. Attempt revocation
while the original consumers are queued, running, between stages, and cancelling.
Only after they actually finish or stop may revocation succeed.

## Work and evidence

1. **Admission and lifecycle:** propose experimental learning/snapshot/consumer
   records and the authoritative coordination mechanism. Test concurrent
   invocation creation and revocation, failed creation after registration,
   cancellation before worker shutdown, and stale cached reference counts. Do
   not assert atomicity across separate API writes without a recovery protocol.
2. **File delivery:** render the approved index and full files, verify digests,
   enforce read-only projection, and keep scratch state separate. Missing or
   mismatched artifacts must produce visible preparation failure. Re-materialize
   the same selection in a replacement process; this tests knowledge stability,
   not general recovery of an AgentRun's external actions.
3. **Runtime integration:** qualify a pinned Lightspeed/DeepAgents integration,
   including startup inclusion, on-demand access, compaction, subagents, and
   existing inspection boundaries. Capture delivered excerpts after filtering
   and truncation. Describe limitations for arbitrary shell access explicitly.
4. **Context evaluation:** compare all-upfront, selective, and hybrid delivery
   for the same model, tasks, and small/large corpora. Measure task success,
   required-instruction adherence, missed lessons, delivered revisions,
   token/tool cost, and latency. Report repeated trial counts and configuration.
5. **Review contract:** propose concise-summary metadata, applicability fields,
   explicit replacement lineage, conflict/overflow handling, and retention after
   consumers complete. A human-required acceptance must identify the authenticated
   reviewer and exact immutable proposal.

## Exit criteria

Provide reproducible fixtures, source/dependency pins, a storage/transaction map,
proposed fields/conditions, automated lifecycle failure tests, and a report
separating scripted from real-runtime evidence. A recommendation to proceed
requires stable creation-time selection, whole-workflow inheritance, safe
revocation refusal, exact file reproduction, and honest delivery attribution.

Failures should identify the smallest missing runtime or admission capability;
do not replace required guarantees with an eventually consistent counter or an
agent's assertion that it read the files. Storage selection and production API
publication follow review of the results.

## Out of scope

Durable sessions, webhooks, event classifiers, automatic extraction/promotion,
IDE/CLI continuation, and arbitrary side-effect recovery are independent work.
A future session consumer uses the same learning contract at each new input.
