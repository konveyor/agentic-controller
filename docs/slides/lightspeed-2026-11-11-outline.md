# Konveyor and Lightspeed presentation guide

November 11, 2026. The [Markdown deck](lightspeed-2026-11-11.md),
[rendered PDF](lightspeed-2026-11-11.pdf), and
[separate 2:44 demo video](https://github.com/djzager/agentic-controller/blob/c43abc5e5509fff9ea982a1a170e7bf51ae1aafe/docs/slides/lightspeed-2026-11-11-demo.mp4)
are committed in the same PR. The [brief](../briefs/lightspeed-2026-11-11.md)
contains the evidence and limits; the [demo guide](lightspeed-2026-11-11-demo.md)
covers the completed run and cluster tour.

## Decision baseline and presentation flow

Follow accepted ADR 0021: reuse the Lightspeed runtime inside our Go harness,
qualify its operator as an alternative execution backend, and migrate resource
APIs later as equivalents cover our needs. ADR 0016's current Gateway selection
and direct Agent Sandbox default remain in force. OpenShell is not a
predetermined destination, and its adoption would not automatically remove
Gateway/provider configuration. Proposed ADR 0020 has not settled the outcome taxonomy.

The deck has **22 slides**. Slide 5 summarizes the runtime changes and why
Konveyor needs them. Slides 6–13 provide source-guided technical detail for
discussion. Each topic has clickable implementation links and deeper speaker
notes. The demo slide holds the clickable harness PR, runtime, UI and result
links. A dedicated Questions slide leads into the operator problem set.

## Runtime walkthrough

Slides 1–15 target **8–10 minutes**, including the separate **2:44 video**.
Keep the setup and summary short, then spend about 25–30 seconds on each
technical topic. The detailed notes and source links support questions rather
than adding a minute of narration to every topic. Discussion is additional.

1. **Adopting Lightspeed’s DeepAgents runtime in Konveyor** — 15 seconds.
2. **Runtime integration** — 15 seconds. Setup, runtime changes, demo, questions.
3. **Konveyor with Goose** — 45 seconds. Existing controller, Sandbox, harness,
   runtime and Git responsibilities.
4. **Konveyor with Lightspeed** — 45 seconds. Replace the runtime box while
   retaining harness preparation/finalization and the current execution backend.
5. **Runtime changes and why we need them** — 40 seconds. Summarize retained
   conversation state, official ACP, inspected output, and truthful execution reporting.
6. **A second prompt in the same conversation** — 25–30 seconds. Lightspeed's query
   wrapper owns graph/client lifetime per call. Retain preparation across prompts.
   DeepAgents and official ACP already support history and repeated prompts.
7. **Lightspeed behind the official ACP server** — 25–30 seconds. Reuse
   AgentServerACP's prompt core, preserving Lightspeed preparation and policies.
8. **User control during execution** — 25–30 seconds. Approval and questions have
   distinct callbacks. Steering reaches a safe model boundary. Active local
   cancellation must stop/await work so the harness can finalize.
9. **Turn limits, usage and the handoff prompt** — 25–30 seconds. Count main-model
   calls explicitly and report timely observed usage. The harness sends a second
   session/prompt request in the same session after the primary reaches its allowance.
10. **Inspection before model and viewer access** — 25–30 seconds. Model inspection
    and approved client release are separate gates. A classification cache entry
    alone does not authorize display.
11. **Viewer replay without reopening rejected output** — 25–30 seconds. Journal
    authorized updates, override private _replay_session and explain pinned-version
    coupling. This is same-process viewer replay, not durable recovery.
12. **Applying the model settings we choose** — 25–30 seconds. Deliver the selected
    reasoning effort to the provider. A main-model output override stays optional.
13. **Detecting a response that was cut short** — 25–30 seconds. Fail an explicit
    provider output cutoff before executing tools from the unfinished response.
14. **Coolstore planning demo** — 180 seconds, including the 2:44 MP4. Show the
    question, approvals, guidance, revisions, final approval and committed plan.
    All implementation/result links are clickable on this slide.
15. **Questions** — runtime discussion, then operator adoption.

The video records the **earlier custom ACP implementation at runtime fork
4039163**, based on upstream 5ea23f3. It remains unchanged and does not validate
the revised official AgentServerACP implementation. Its result was a plan that
passed five current planning checks. Application migration/build/deployment were
not performed. The actual pre-write checkout was b5752b9. Analysis uses its direct
child 32c1722, whose only difference is .vscode/extensions.json.

The [updated runtime at `2291ce9`](https://github.com/djzager/lightspeed-agentic-sandbox/commit/2291ce9e449976d080dd1a287a72a4e8c12f2359)
uses official ACP with Lightspeed-owned preparation, output release and bounded
same-process replay. The private replay method and four additional private hooks
require compatibility checks at the pinned ACP version. Local checks use the
real harness, graph, inspection, tools, ACP and Git with scripted HTTP model
and Hub services. They include six harness cases and 23 native graph/ACP cases.
There was no fresh UI or cluster run of the updated implementation.
Current upstream a23005e8 is a separate source review and inspection-parity
reconciliation target, not the deployed runtime in the video. Keep all three
source/evidence boundaries explicit.

## Operator problems and possible integration paths

Slides 16–22 add about **5 minutes 25 seconds** before discussion. These slides
state the adoption requirements and concrete integration work without selecting
an upstream API or asserting a roadmap commitment. The
[source research](../research/lightspeed-operator-integration-2026-11-11.md)
backs the current behavior and discussion paths.

16. **Lightspeed as the execution backend** — 35 seconds. Candidate delegation
    from our reconciler to AgenticRun; Lightspeed owns SandboxClaim lifecycle,
    while the Go harness retains the application workload. Agent Sandbox stays underneath.
17. **Roadblock: selecting the workload image** — 55 seconds. Java/JDK/Maven and
    Go workloads require different images concurrently. Per-invocation image and
    harness startup are firm requirements; image/profile selection is a possible contract.
18. **Problem: workload placement and access** — 60 seconds. Current central
    placement, cluster-scoped Agent/provider, namespaced Secrets, and complete
    Hub/model/Git/MCP/ACP access must be qualified together. Placement is open.
19. **Problem: harness input and run completion** — 60 seconds. Existing batch
    files and Result RBAC may support a harness adapter. Input/outcome mapping,
    verification/push/cleanup and Result publication need qualification.
20. **Problem: stopping the agent or its workload** — 55 seconds. Graceful ACP
    cancellation inside a live Pod differs from operator hard stop and whole-process
    deadlines. Discuss a finalization allowance and publication failure behavior.
    PR588 is an open hard-stop proposal, not merged graceful-cancellation support.
21. **OpenShell roadmap and integration contract** — 60 seconds. Ask whether it
    is planned and when; discuss image, policy, credentials, Result identity,
    ACP and lifecycle implications if it is. No public dated commitment was found.
22. **Questions** — discussion of the operator problems and possible collaboration.

## Diagrams and rendering

The current-flow and runtime-swap diagrams are editable SVGs:
[konveyor-run-today.svg](konveyor-run-today.svg) and
[lightspeed-runtime-swap.svg](lightspeed-runtime-swap.svg).
They narrow the scope of [Ian's PR diagram](https://github.com/konveyor/agentic-controller/pull/280#issuecomment-6372032054)
to the runtime integration being demonstrated. Backend alternatives belong in the
later operator discussion.

The PR contains the editable Markdown deck, the matching rendered PDF, and the
separate MP4 demo. Present the PDF and switch to the video on the demo slide.
Slide 14 links to the exact changes in each repository; the video ending names
those repositories and explains their roles.

The deck uses the existing Konveyor Marp theme. Render the committed PDF with:

```sh
make slides DECK=docs/slides/lightspeed-2026-11-11.md
```

For a scrollable browser preview:

```sh
mkdir -p .scratch/lightspeed-slides
cp docs/slides/konveyor-logo.svg docs/slides/konveyor-run-today.svg \
  docs/slides/lightspeed-runtime-swap.svg docs/slides/lightspeed-2026-11-11-demo.mp4 \
  .scratch/lightspeed-slides/
npx --yes @marp-team/marp-cli \
  --theme docs/slides/konveyor-theme.css \
  --template bare docs/slides/lightspeed-2026-11-11.md \
  -o .scratch/lightspeed-slides/index.html
```

Keep generated HTML preview files local. Commit the Markdown and SVG sources,
rendered PDF, and shared MP4 recording. Regenerate the PDF whenever the deck changes.
