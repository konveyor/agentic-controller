# Spike: AgentLearning for existing runs and workflows

Status: proposed, 2026-11-11. Supports
[ADR 0024](../adr/0024-agent-learnings.md). No experiments described here have run.

Updated 2026-11-18: includes learning-authoring guidance, proposal-only run
identities, Hub/Kubernetes approval boundaries, and concrete selector fixtures.

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

Use the illustrative selectors in
[ADR 0024](../adr/0024-agent-learnings.md#match-learning-selectors-to-hub-supplied-invocation-labels)
for an application-42 proposal accepted for applications 42 and 87, plus a
separate learning selected by the `java-ee` archetype. Hub-supplied labels on
both invocation kinds must determine selection. Include fixture app 99 with
the same archetype and app 100 with another archetype: app 99 receives only the
archetype learning, and app 100 receives neither. Verify that an unaccepted or
unauthorized matching proposal is excluded. These are proposed contracts to
exercise, not existing Hub archetype labels or implemented CRD fields.

Both invocations retain their creation-time revisions; all workflow stages use
the same Learning Snapshot. Changing workload labels after creation does not
change it. A newly created run selects the replacement. Attempt revocation
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
   consumers complete. Show how a reviewer requests a new immutable proposal when
   content or applicability needs revision before acceptance.
6. **Approval authority:** exercise Hub's existing user authentication and scope
   authorization before its service account writes the decision through the
   Kubernetes API. Record the actual Hub reviewer and exact proposal. Use a
   separately RBAC-protected status write or approval resource for direct API
   enforcement. Test unauthorized Hub users, direct unauthorized API writes,
   forged reviewer fields, and creating an already-approved learning. Label
   matching and ordinary proposal creation must never grant approval authority.
   State whether Hub checks are integration-tested or represented by fixtures.
7. **API immutability:** use CRD CEL transition validation on immutable proposal
   fields, following AgentRun's existing `self == oldSelf` pattern. Verify that
   changes to the title, summary, relevance, lesson, evidence references, or selector
   are rejected while authorized lifecycle changes remain possible. Exercise
   realistic bounded content sizes. Delete/recreate under the same name must
   not replace a pinned UID/digest; mutation of external lesson content must
   produce digest failure, not silent substitution.
8. **Agent proposals:** supply a learning-authoring skill covering reusable
   lessons, evidence, relevance, and suggested applicability. Automatically
   provision a per-AgentRun ServiceAccount and RoleBinding with proposal-creation
   permission only, and demonstrate the submission integration using that
   identity. Bind originating-run provenance to the authenticated identity;
   validate any asserted source application through a trusted boundary. Test
   attempts to spoof another run, self-approve, change lifecycle state, or
   delete a learning. Clean up run credentials without cascading deletion of
   shared learnings when the source run is pruned. Keep Kubernetes credential
   and RBAC mechanics out of the skill content.

## Exit criteria

Provide reproducible fixtures, source/dependency pins, a storage/transaction map,
proposed fields/conditions, automated lifecycle failure tests, and a report
separating scripted from real-runtime evidence. A recommendation to proceed
requires stable creation-time selection, whole-workflow inheritance, safe
revocation refusal, exact file reproduction, and honest delivery attribution.
Also provide the authoring skill, proposal identity/RBAC fixtures, label-selector
fixtures for both invocation kinds, and API failure tests establishing immutable
content, protected approval, and trustworthy source attribution. Distinguish
real Hub authorization evidence from simulated caller/permission fixtures.

Failures should identify the smallest missing runtime or admission capability;
do not replace required guarantees with an eventually consistent counter or an
agent's assertion that it read the files. Storage selection and production API
publication follow review of the results.

## Out of scope

Durable sessions, webhooks, event classifiers, automatic background extraction
and promotion, IDE/CLI continuation, and arbitrary side-effect recovery are
independent work. Skill-guided agent proposals and their submission path are
in scope; a generated proposal still requires the protected acceptance path.
A future session consumer uses the same learning contract at each new input.
