# Lightspeed runtime demo — result and tour

Companion to the [discussion brief](../briefs/lightspeed-2026-11-11.md) and
[presentation outline](lightspeed-2026-11-11-outline.md). This records the completed
live-model planning run and provides a guide for a rerun or cluster tour.
The [presentation recording](https://github.com/djzager/agentic-controller/blob/c43abc5e5509fff9ea982a1a170e7bf51ae1aafe/docs/slides/lightspeed-2026-11-11-demo.mp4) is 2 minutes 44 seconds.
Use the [timed narration](lightspeed-2026-11-11-demo-script.md) for the presentation.
The PR also includes the [editable slide deck](lightspeed-2026-11-11.md) and its
[rendered PDF](lightspeed-2026-11-11.pdf). Present the PDF, then play the MP4 separately.
The final video card names the Konveyor harness, Lightspeed runtime, and Konveyor UI
repositories. Their [pinned source changes](../briefs/lightspeed-2026-11-11.md#runtime-contribution-for-review)
and the [Coolstore plan result][live-plan] are linked for review.

## Purpose and scope

Show a live model using the reusable Lightspeed runtime inside Konveyor's
existing Go harness, controller, and Agent Sandbox execution path. The optional
ACP adapter carries the existing UI interactions; core sessions remain independent
of that adapter. This does not deploy the Lightspeed operator. [Accepted direction][adr]

The task, inspired by the [September 30 interaction][precedent], was to read
Coolstore source and analysis, ask about the database, incorporate human guidance,
and commit a reviewable `docs/plan.md`. The user chose to reuse the existing
PostgreSQL service. The result is a plan; application source is unchanged.
This was a single AgentRun. Cancellation and turn-limit handoff have separate
evidence.

The [Coolstore `mta-test` analysis uses `32c1722`][coolstore]. The actual run started
from [`b5752b9`][coolstore-checkout], and pushed to a separate output branch.
Preserve that pre-write baseline; do not commit results back to either upstream
input branch.

## Confirmed setup

The OpenShift installation uses Konveyor's installation script, authentication is
on, and the demo uses its own account. Coolstore is registered from a fork at
upstream `mta-test` commit `32c1722efdf883f5f400170adb770a46a9b58116`.
A real Quarkus source analysis completed at that commit with 49 insights.
The actual pre-write checkout is `b5752b936f01acd858ca21fd2d3c4f03d3a6321e`.
`32c1722` is its direct child, and the complete tree difference is only
`.vscode/extensions.json`, an editor-extension recommendation. All application
source, build files, and application configuration are identical. Analysis uses
`32c1722`; this run starts from `b5752b9`.

The existing harness replaces the source branch with the target branch before
cloning and does not select the requested clone reference. It therefore used
default `main` at `b5752b9`. Preserve the configured source reference during clone
as follow-up work. Keep the plan's provenance and final diff tied to the actual
pre-write checkout, with the separate application-tree comparison to `32c1722`.

The Gateway selects GLM 5.3 through an Anthropic-compatible service. Hub holds
the application's Git credential for the harness. Keep endpoints, credentials,
and account details out of the deck and recording.

Current review revisions:

- Go harness in the workload image: [`73a44d5`][controller-revision].
  Later integration documentation at [`447afec`][controller-docs] changes no binaries.
- Lightspeed runtime: [`4039163`][runtime-revision], including provider inspection
  parsing, explicit output-limit handling, and reasoning-effort forwarding.
- Tackle UI: [`10dc62e`][ui], including tool arguments on permission cards.

The selected model-specific image is
`sha256:1c74a3ea20283cc92a3b3d0ccc24dbd797eb422541c5bec0ba7f37c1e1b934e5`,
using runtime `4039163` with the unchanged Go harness at `73a44d5`. It sets
`LIGHTSPEED_MODEL_MAX_OUTPUT_TOKENS=16384` and
`LIGHTSPEED_REASONING_CONFIG={"thinking":{"type":"enabled"},"effort":"low"}`.
The Agent selects it through `Agent.spec.image` and is Ready. Both a native image
smoke check and the running pod's typed parsers confirmed enabled thinking,
low effort, and `max_tokens=16384`. All 39 runtime Python files match `4039163`.
The UI image is
`sha256:2314cfb22672c414cc305cdb35646be87bb182394ad6b715730096324a391f2c`.

## Verified live result

AgentRun `ui-2rhxw` completed `Succeeded` at `2026-11-11T02:52:34Z`, exit code 0,
with no pod restarts. The full run took 28 minutes 9 seconds. It pushed
[commit `9e97db0`][live-commit] to `demo/lightspeed-live-plan-take-07`; only
`docs/plan.md` was added against the actual prepared checkout `b5752b9`.

The UI interaction included the database-reuse answer, reviewed tool permissions,
and guidance consumed by later model steps. The reviewer requested changes to
the first plan, reviewed the corrections, and approved the final commit. The
harness then verified and pushed the result.

All five actual planning checks ran and passed with exit code 0:

1. The plan file exists.
2. The prepared tree is unchanged outside the plan and Konveyor context.
3. Application source, dependencies, and build content match the analysis input.
4. The plan is tracked after the local commit.
5. Konveyor analysis context remains present.

Status reports `Verified=True`, reason `VerificationPassed`, for commit
`9e97db02b641dc2608fcc399503bbc1a0ed3b842`. The approved, locally committed, and
remote [plan][live-plan] are identical: 141 lines, 13,457 bytes, SHA-256
`fc700a9a04d77288b821325f323db5fcfb579bda8d956436963e9715a26aa621`.
The runtime reported 33 native model calls, 563,794 input tokens, and 10,391 output
tokens. Inspection-classifier calls are excluded; no monetary cost is supplied.
These checks verify a plan and unchanged application, with future migration
build/tests and runtime validation listed separately.

The [presentation recording](https://github.com/djzager/agentic-controller/blob/c43abc5e5509fff9ea982a1a170e7bf51ae1aafe/docs/slides/lightspeed-2026-11-11-demo.mp4) uses normal-speed
excerpts from this take, with idle waits omitted and three labelled reading pauses
on exact result screenshots. The full raw take is retained privately.
Use a separate output branch for any new rehearsal.

## Runtime qualification

Two prompts in one core session passed against the live model. The provider
compatibility fix passed a separate live read-file check with inspection enabled,
plus the full runtime suite. The new output-limit tests use native streamed
responses and the actual graph/ACP path: an explicit provider cutoff raises
`ModelOutputLimitError` before a tool side effect or another model call, preserves
observed usage, and permits an explicit same-session follow-up. There is no
automatic retry. The latest suite passed with 2,096 tests, 13 skips, and 7 subtests,
plus Ruff, format, and mypy checks.

Explicit reasoning effort now reaches `output_config.effort` on Anthropic routes,
with tests covering direct, Bedrock, and Vertex clients. Thinking changes only
when configured. [GLM 5.3 always reasons and defaults to max effort][glm-reasoning].
The [official Anthropic mapping][glm-anthropic-mapping] combines enabled thinking
with `output_config.effort`. A small live low-effort probe returned a valid tool
call. The completed planning run supplies separate task evidence above.

The model-specific image selects low effort for the interactive demo and applies
the main-model output allowance without API or UI changes. API clients can
alternatively use `AgentRun.spec.env`. The Agent has no `spec.env`, and the current
UI creation form does not expose AgentRun environment settings. The output-token
override is optional. Omitting it preserves the finite SDK/provider allowance.
Global defaults, inspection-classifier and shaping allowances are unchanged.
Keep failed rehearsal takes and diagnostic evidence private.

## Checks for a rerun

1. Record the deployed controller/harness, runtime, and UI image digests. Confirm
   the running process imports the modified runtime files. Source revisions and
   image labels alone do not prove which package the process used.
2. Use the Agent image with `HARNESS_RUNTIME=lightspeed` and keep the ACP tee
   enabled. The prototype image carries Python, Git, and the harness. This
   planning task does not qualify a complete application build toolchain.
3. Confirm the selected Gateway/model, configured source reference, actual
   checkout, analysis revision, and separate output branch. Freeze the actual
   checkout before the agent writes; investigate any difference from the
   configured reference before describing the run. The Agent offers the
   Gateway and the AgentRun selects it. Upstream Lightspeed `LLMProvider`
   resources are unnecessary here. [Gateway contract][gateway]
4. To reproduce this demo profile, confirm the image applies the 16,384-token
   allowance and explicit enabled thinking with low effort to the main model.
   Output-limit tests cover visible
   failure for an explicit cutoff. Short provider checks establish configuration
   acceptance; every complete planning run still needs its own result checks.
5. Use `mode: approve`, `askUser: true`, and steering, with a bounded turn allowance
   and enough time to answer. Omit `maxCost`: the Konveyor harness rejects it before
   execution. Token observations exclude inspection-classifier calls and supply
   no monetary cost. The live UI turn label approximates tool calls. Use the final
   runtime-provided count for native model calls. [Usage and limits][prototype]
6. Keep the recording machine awake and watch every question and tool permission.
   This demo allows 180 seconds for a response. A permission timeout marks the
   viewer unresponsive and later questions fail closed. Start a fresh AgentRun
   after a timeout.
7. Check that questions matter to the plan, permission cards show the action and
   arguments, and guidance reaches a later model step. Record the actual order
   and outcome. Live-model wording, latency, and success can vary.
8. Inspect `docs/plan.md`, the pushed commit and branch, the diff against that
   run's frozen pre-write checkout (`b5752b9` for this recording), run status,
   verification, observed tokens, and
   runtime identity before calling the demonstration complete. A terminal
   `Succeeded` phase alone does not establish that the requested plan exists.

## Verification for a planning task

The harness reran all five commands from this plan's `## Verification` section;
all passed. They check the plan and unchanged application. Proposed Maven/build,
application-test, runtime, and cutover checks live under
`## Future migration validation` and were not run during this planning task.

The source defaults enable verification but do not enforce it. A terminal
`Succeeded` phase alone is insufficient evidence: inspect the actual commands,
exit codes, `Verified` condition, pinned commit, and remote file. This run has
all five passing checks and `Verified=True`. [Harness behavior][verification]

For a rerun, preserve the existing verification settings. Put commands that check
the plan itself in `## Verification` and keep future migration commands in a
separate section. The application remains unchanged during planning.

## Recording storyboard and narration

The full live run lasted 28 minutes 9 seconds. The presentation recording is
2 minutes 44 seconds, using selected normal-speed excerpts and three labelled
reading pauses.
Keep selected interactions and result inspection readable at normal speed;
disclose omitted idle waits. This footage comes from the live-model run and
must remain distinct from the earlier 36-second scripted clip.

1. **Application and problem.** Show Coolstore, actual checkout `b5752b9`, analysis
   revision `32c1722`, and the limited task. Explain that the application trees
   match, with only editor-extension recommendations differing. Say: “We want a
   reviewable plan for this application. The decision we will make together affects
   what the plan recommends.”
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

Keep the full take, screenshots, sanitized interaction summaries, status and
verification evidence, runtime provenance, and Git diff for review. Exclude
credential values, authentication query strings, and raw logs from shared artifacts.
The published edit must disclose shortened idle waits and preserve the meaning
of the actual interactions.

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
[prototype]: https://github.com/djzager/agentic-controller/blob/447afecfb249b13e95b2c6ef1a156a0f03ce388c/docs/lightspeed-runtime-prototype.md
[ui]: https://github.com/djzager/tackle2-ui/commit/10dc62e6b44036920d3e050ee04d75343fc859a7
[gateway]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/api/v1alpha1/gateway_types.go#L58-L77
[verification]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/harness/cmd/migration-harness/main.go#L711-L765
[ttl]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/api/v1alpha1/agentrun_types.go#L310-L320
[ttl-default]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/cmd/main.go#L102-L107
[finished-ui]: https://github.com/djzager/tackle2-ui/blob/aa4f9b93b98d029ba2652e8b645b53018dd1de1c/client/src/app/pages/agent-runs/components/ChatPanel.tsx#L1397-L1467
[transcript]: https://github.com/djzager/tackle2-ui/blob/aa4f9b93b98d029ba2652e8b645b53018dd1de1c/client/src/app/pages/agent-runs/components/ChatPanel.tsx#L569-L594
[lifecycle]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/internal/controller/agentrun_controller.go#L1576-L1605

[controller-revision]: https://github.com/djzager/agentic-controller/commit/73a44d59f9e4cf6644ab4fc48f42211a7eff2548
[runtime-revision]: https://github.com/djzager/lightspeed-agentic-sandbox/commit/403916314aa4f869fc7733bef6e20fff32cf2680

[controller-docs]: https://github.com/djzager/agentic-controller/commit/447afecfb249b13e95b2c6ef1a156a0f03ce388c

[glm-reasoning]: https://docs.z.ai/guides/llm/glm-5.3
[glm-anthropic-mapping]: https://github.com/zai-org/ZCode/blob/main/config/provider/zcode-builtin.json

[coolstore-checkout]: https://github.com/konveyor-ecosystem/coolstore/tree/b5752b936f01acd858ca21fd2d3c4f03d3a6321e

[live-plan]: https://github.com/djzager/coolstore/blob/9e97db02b641dc2608fcc399503bbc1a0ed3b842/docs/plan.md
[live-commit]: https://github.com/djzager/coolstore/commit/9e97db02b641dc2608fcc399503bbc1a0ed3b842
