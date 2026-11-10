# Lightspeed runtime demo — preparation and tour

Companion to the [discussion brief](../briefs/lightspeed-2026-11-11.md) and
[presentation outline](lightspeed-2026-11-11-outline.md). This is a preparation
runbook, not a record of a completed live-model demonstration.

## Purpose and scope

Show a live model using the reusable Lightspeed runtime inside Konveyor's
existing Go harness, controller, and Agent Sandbox execution path. The optional
ACP adapter carries the existing UI interactions; core sessions remain independent
of that adapter. This does not deploy the Lightspeed operator. [Accepted direction][adr]

Recommend a scoped planning task inspired by the [September 30 interaction][precedent]:
read Coolstore source and available analysis, ask about one consequential choice,
incorporate human guidance, and commit a reviewable `docs/plan.md`.
Use [Coolstore `mta-test` at `32c1722`][coolstore] as the baseline and a separate
demo output branch. Preserve that baseline; do not commit results back to it.

The exact planning task, target, datasource alternatives, and instructions are
rehearsal-dependent. A datasource decision is a candidate, not a prescribed model
answer. This demonstrates planning and interaction, not a completed migration or
an AgentWorkflowRun. Cancellation and turn-limit handoff have separate evidence.

## Preparation after cluster access arrives

1. Inspect the supplied installation before making changes: namespace, permissions,
   controller and Agent Sandbox versions, Hub/UI connectivity and feature flags,
   application inventory/analysis, registry access, Git access, and cleanup policies.
   Cluster access, provider/model, and the final scenario remain pending. Resolve
   details obtainable from that inspection before asking for additional choices.
2. Record source revisions and image digests for controller/harness, runtime, and
   UI. Start with the [published prototype revisions][prototype] and [UI revision][ui].
   Confirm the deployed image actually contains the modified runtime; a label alone
   does not establish which package a running process imported.
3. Configure a Konveyor **Gateway** and credential **Secret** for the supplied live
   provider/model. A Gateway declares endpoint, provider, credentials, model name,
   and context window. The Agent offers that Gateway; the AgentRun selects it.
   Upstream Lightspeed `LLMProvider` resources are unnecessary here. [Gateway contract][gateway]
4. Select an Agent image carrying the patched runtime and harness with
   `HARNESS_RUNTIME=lightspeed`. Check required language/build tools: the prototype
   image is a development image containing Python, Git, and the harness, not a
   qualified application toolchain. Keep the ACP tee enabled. [Image and setup][prototype]
5. Register/confirm Coolstore against the pinned baseline. Check that analysis, if
   used, describes that source and the intended task. If only source is available,
   present it as source-based planning; do not imply an analyzer ran.
6. Configure a watched run with `mode: approve`, `askUser: true`, and steering
   enabled. Choose a bounded turn allowance and suitable interaction timeout.
   Omit `maxCost`: this prototype rejects it before execution. Available cumulative
   token observations omit isolated inspection-classifier calls and supply no
   monetary cost. Complete runtime usage/cost reporting and monetary-budget
   enforcement remain follow-on work. [Usage and limits][prototype]
7. Rehearse against the live provider before recording. Confirm the chosen question
   changes the plan, permission cards describe actual actions, steering reaches a
   subsequent model step, and the output is readable. Live-model wording, ordering,
   latency, and success are not guaranteed. Keep failed attempts as diagnostic
   evidence rather than editing them into a success claim.

## Verification gate before choosing the final task

The harness can rerun commands from the plan's `## Verification` section after a
turn claims success. In a standalone planning run, future migration build commands
may fail against the unchanged application; missing tools may also prevent checks.
Measurement and enforcement are separate, so `Succeeded` alone is not proof of a
verified migration. Inspect actual verification/enforcement settings and rehearse
this path before committing to the scenario. [Harness behavior][verification]

Preserve the existing verification settings and use checks appropriate to the
actual deliverable. Distinguish plan acceptance from future application verification
and show the real outcome alongside recorded verification. Resolve a mismatch
during rehearsal or explain the remaining limitation in the recording.

## Recording storyboard and narration

Allow time to read each decision and the final diff. Replace the earlier
36-second scripted clip with a contextual walkthrough; set final timing after
rehearsal. Keep interaction and result inspection readable at normal speed.

1. **Application and problem.** Show Coolstore, the baseline revision, relevant
   source/analysis, and the limited task. Say: “We want a reviewable plan for this
   application. The decision we will make together affects what the plan recommends.”
2. **What will run.** Show the selected Agent, live model/Gateway, runtime image
   digest, output branch, and interaction settings before creating the AgentRun.
   Say: “Konveyor still owns this invocation and creates an Agent Sandbox. The
   image runs Lightspeed's modified runtime through our harness.”
3. **Question and answer.** Let the model inspect actual inputs. At a meaningful
   question, read its alternatives before answering and explain the consequence.
   Say: “This is a question from the running agent. My answer returns to its
   conversation and should be reflected in the plan.” Do not promise a specific card.
4. **Permission and steering.** Read an actual permission request before approving
   the selected action. Explain that permission authorizes a tool; answering a
   question supplies a decision. Send useful guidance, such as asking to review
   decisions before a commit, and show acceptance and subsequent uptake. Steering
   is consumed at a safe model boundary; it is not a durable approval gate and
   does not itself pause the run. Show only the interactions the rehearsal supports.
5. **Plan and result.** Inspect `docs/plan.md` and the Git diff long enough to find
   the chosen decision and guidance. Show target-branch commits, actual AgentRun
   outcome, observed tokens, and the harness verification record. Say: “This is
   the plan this run produced. Its status and verification describe this work;
   they do not establish that the application has been migrated.”
6. **Runtime evidence.** Show a sanitized summary linking the run, pod image digest,
   source revisions, imported Lightspeed package/session source, and active ACP
   process observed during execution. Capture this while the process exists.
   Explain which observations establish runtime identity and which are merely labels.

Capture a recording, screenshots, sanitized interaction summaries, status and
verification evidence, runtime provenance, and resulting Git diff during the run.
Exclude credential values, authentication query strings, and raw logs from shared
artifacts. If idle waits are shortened, disclose the edit; preserve complete takes
for review. Do not reuse scripted-model footage as evidence of this live run.

## Preserve the setup for a post-demo tour

- Keep the namespace, Hub/UI installation and application data, Agent/skills/Gateway
  configuration, completed AgentRun, and output branch available until the tour ends.
  Do not run teardown after recording. Check the actual cluster's retention settings.
- Controller-wide `--agentrun-ttl=0` defaults to no default expiry. An omitted
  per-run TTL inherits that setting; explicit `spec.ttlSecondsAfterFinished: 0`
  means immediate deletion. Choose retention before creating the run: its spec is
  immutable. TTL deletion cascades to owned Sandbox/pod and per-run resources.
  If an existing default is required, select a positive TTL covering the tour.
  [TTL field][ttl], [controller default][ttl-default]
- Retained status and termination data preserve outcome, usage, and verification;
  remote Git preserves pushed commits. Revisiting the UI can show the finished
  result, persisted usage, configuration, and branch links. Inspect `Verified` through
  Hub/CR status; this UI does not render that condition. [Finished UI][finished-ui]
- Transcript content is mounted React state, with no built-in transcript export
  found in this UI. Keep the tab open for an immediate walkthrough and capture
  evidence during execution. Reloading a completed run cannot rebuild its transcript.
  [UI state and connection gate][transcript]
- Normal harness exit stops runtime and ACP tee; the controller reports
  `ACPReady=False`, reason `Finished`. A retained completed pod or Service does
  not preserve ACP or resumable session memory. Preserve normal scoped Hub token
  revocation. A later interactive demonstration requires a new AgentRun. [Lifecycle][lifecycle]

[adr]: https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
[precedent]: https://github.com/konveyor/agentic-controller/blob/369070c659a71cecf50d57c376cb13d09d332701/docs/slides/demo-2026-09-30-script.md#part-2-a-human-in-the-loop
[coolstore]: https://github.com/konveyor-ecosystem/coolstore/tree/32c1722efdf883f5f400170adb770a46a9b58116
[prototype]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/docs/lightspeed-runtime-prototype.md
[ui]: https://github.com/djzager/tackle2-ui/commit/aa4f9b93b98d029ba2652e8b645b53018dd1de1c
[gateway]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/api/v1alpha1/gateway_types.go#L58-L77
[verification]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/harness/cmd/migration-harness/main.go#L711-L765
[ttl]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/api/v1alpha1/agentrun_types.go#L310-L320
[ttl-default]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/cmd/main.go#L102-L107
[finished-ui]: https://github.com/djzager/tackle2-ui/blob/aa4f9b93b98d029ba2652e8b645b53018dd1de1c/client/src/app/pages/agent-runs/components/ChatPanel.tsx#L1397-L1467
[transcript]: https://github.com/djzager/tackle2-ui/blob/aa4f9b93b98d029ba2652e8b645b53018dd1de1c/client/src/app/pages/agent-runs/components/ChatPanel.tsx#L569-L594
[lifecycle]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/internal/controller/agentrun_controller.go#L1576-L1605
