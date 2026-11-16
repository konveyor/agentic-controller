# Lightspeed presentation and demo walkthrough

Present the [PDF](lightspeed-2026-11-11.pdf), then play the
[separate 2:44 video](lightspeed-2026-11-11-demo.mp4) on slide 14.
The [editable deck](lightspeed-2026-11-11.md) includes detailed speaker notes and
pinned implementation sources. Slides 1–15 take about 8–10 minutes including the
video. Slides 16–22 discuss the operator contract and leave time for questions.

Slides 3–4 explain the runtime swap and retained harness responsibilities.
Slides 5–13 cover retained conversations, official ACP, human controls, handoff,
inspection, approved viewer replay, model settings and output cutoff. Operator
slides ask about per-run images, placement and access, batch inputs/results,
graceful finalization and OpenShell. These are integration questions under
[ADR 0021](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md).

The recording uses **runtime `4039163` with the earlier custom ACP adapter**.
The deck also describes the later **official ACP integration at `2291ce9`**,
qualified separately by local scripted tests. This video remains unchanged and
does not validate that later implementation or a real classifier rejection.
See the [integration guide](../lightspeed-runtime-prototype.md) for current scope.

## Timed narration

- **0:00–0:24:** “This is Coolstore, our Java EE example. We already have its
  source analysis. We are asking for a Quarkus migration plan through Konveyor.
  The selected Agent image contains our harness and the modified Lightspeed
  runtime. Questions and tool approvals are enabled.”
- **0:24–0:44:** “The agent asks whether the future application should reuse
  PostgreSQL or use a separate service. We choose the existing service.
  That decision now belongs in the plan.”
- **0:44–0:52:** “We can give guidance while the agent works. This message is
  queued for a later model call. We ask it to explain the consequences of
  database reuse, transaction boundaries and testing risks.”
- **0:52–1:15:** “The runtime asks permission to write the plan, then to commit.
  We request changes and review its edits in the same conversation. The full
  run had more edits and reviews than this excerpt shows.”
- **1:15–1:39:** “Once the plan is acceptable, we approve the local commit and
  the actual Git command. The harness verifies and pushes. Push credentials
  stay in the harness.”
- **1:39–2:16:** “The committed plan records our PostgreSQL choice and requires
  schema and migration-history review. It identifies transaction work, testing
  gaps and open questions. These checks verify the plan and unchanged source.
  Application migration and its future validation remain to be done.”
- **2:16–2:28:** “The run succeeded and the harness passed all five planning
  checks before pushing. We have token observations, excluding classifier calls.
  Monetary cost is unavailable. The result is an approved plan in Git.”
- **2:28–2:44:** “The harness integration is in djzager/agentic-controller, the
  runtime in djzager/lightspeed-agentic-sandbox, the UI in djzager/tackle2-ui,
  and the plan in djzager/coolstore. Slide 14 links to the exact changes.”

## Recorded result and source provenance

AgentRun `ui-2rhxw` finished `Succeeded` at `2026-11-11T02:52:34Z`, exit 0, with
`Verified=True` and reason `VerificationPassed`. The full run took **28:09**.
It pushed [commit `9e97db0`][result] to `demo/lightspeed-live-plan-take-07`.
Only [docs/plan.md][plan] was added: 141 lines, 13,457 bytes, SHA-256
`fc700a9a04d77288b821325f323db5fcfb579bda8d956436963e9715a26aa621`.
The approved plan, local commit and remote file matched.

All five planning checks passed: plan existence, no unintended prepared-tree
changes, application/build parity with analysis input, tracked plan, and retained
Konveyor analysis context. No application migration, build, deployment or migration
tests ran. Reported usage was 33 native model calls, 563,794 input tokens and
10,391 output tokens. Isolated inspection-classifier calls are excluded and no
monetary cost is reported.

- Harness: [`73a44d5`](https://github.com/djzager/agentic-controller/tree/73a44d59f9e4cf6644ab4fc48f42211a7eff2548).
- Recorded runtime: [`4039163`](https://github.com/djzager/lightspeed-agentic-sandbox/tree/403916314aa4f869fc7733bef6e20fff32cf2680).
  Later locally tested official ACP runtime: [`2291ce9`](https://github.com/djzager/lightspeed-agentic-sandbox/tree/2291ce9e449976d080dd1a287a72a4e8c12f2359).
- UI: [`10dc62e`](https://github.com/djzager/tackle2-ui/tree/10dc62e6b44036920d3e050ee04d75343fc859a7).
- Workload image: `sha256:1c74a3ea20283cc92a3b3d0ccc24dbd797eb422541c5bec0ba7f37c1e1b934e5`.
- Model: GLM 5.3 through an Anthropic-compatible Gateway, enabled thinking,
  effort `low`, and 16,384 tokens per response including reasoning.
- Actual pre-write checkout: [`b5752b9`](https://github.com/konveyor-ecosystem/coolstore/tree/b5752b936f01acd858ca21fd2d3c4f03d3a6321e).
  Analysis input: [`32c1722`](https://github.com/konveyor-ecosystem/coolstore/tree/32c1722efdf883f5f400170adb770a46a9b58116),
  its direct child. Only `.vscode/extensions.json` differs. Application and build
  files are identical. The harness used default `main` rather than the configured
  source reference; this result is checked against the actual checkout.

## Recording disclosure

The **silent, captioned 2:44 edit** contains footage from that one successful run.
Interactions play at normal speed. Idle waits and repeated edit/review cycles
are omitted, and result sections are arranged for explanation. Three labelled
reading pauses hold exact captured frames: four seconds for queued guidance,
twelve for the database decision, and twelve for verification. Crops enlarge
selected regions. A “Demo account” label covers the setup account name.
UI and plan text are unchanged. The final sixteen-second card names the four
repositories and their roles. The edit uses no generated replies, synthetic
terminal output, failed takes or supplementary recordings.

Video SHA-256:
`0391eb847cd23b4489c3652f0cc42d203fb982ec2ccff77ea53350be65cee3a8`.
The full take and diagnostic evidence remain in the private local archive.

## Presenting and preparing a later run

Render deck changes with:

```sh
make slides DECK=docs/slides/lightspeed-2026-11-11.md
```

The two editable diagrams live beside the deck. Keep generated HTML previews
local and regenerate the PDF when visible slides change.

For a new run, first require green GitHub checks and download its image artifacts
within their configured retention window. Use the normal language image, then
record the actual image/imported runtime, Gateway/model, frozen source checkout,
analysis revision and separate output branch. Keep `maxCost` unset. Check the
actual verification commands, pushed file/diff and terminal status together.
Live-model wording, latency and results can vary.

Keep the installation, completed AgentRun and branch available for the tour.
A retained completed pod does not preserve a live ACP process or session memory.
Capture transcript and runtime evidence during execution; reloading a completed
run cannot reconstruct its conversation. Preserve normal Hub-token revocation.
A later interactive demonstration needs a new AgentRun.

[result]: https://github.com/djzager/coolstore/commit/9e97db02b641dc2608fcc399503bbc1a0ed3b842
[plan]: https://github.com/djzager/coolstore/blob/9e97db02b641dc2608fcc399503bbc1a0ed3b842/docs/plan.md
