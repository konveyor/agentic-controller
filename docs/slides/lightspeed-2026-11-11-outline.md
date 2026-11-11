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

The deck has **21 slides**. Eight focused runtime slides use the same questions: what it does, what we
changed, and why Konveyor needs it. Component links identify the implementation. Detailed file/line references are in speaker notes. The demo slide
holds the clickable PR, runtime, UI, and result links. A dedicated Questions
slide leads directly into the operator problem set. There is no pause/continue
choice in the presentation.

## Runtime walkthrough

Slides 1–14 target about **9 minutes 50 seconds**, including the three-minute
demo section. Discussion is additional. Lead with the architectural change on
each technical slide; use the deeper notes to answer questions.

1. **Adopting Lightspeed’s DeepAgents runtime in Konveyor** — 15 seconds.
2. **Runtime integration** — 15 seconds. Setup, runtime changes, demo, questions.
3. **Konveyor with Goose** — 45 seconds. Existing controller, Sandbox, harness,
   runtime and Git responsibilities.
4. **Konveyor with Lightspeed** — 45 seconds. Replace the runtime box; keep the
   harness preparation/finalization and current execution backend.
5. **Keeping the conversation for a second prompt** — 35 seconds. Retain the
   conversation and execution state so the harness can request a handoff.
6. **Asking the user before acting** — 35 seconds. Separate permission and question
   callbacks preserve the interactions Konveyor users already have.
7. **Connecting Lightspeed to Konveyor's UI** — 35 seconds. The optional ACP
   adapter carries the conversation and controls through the existing harness connection.
8. **Guiding or stopping active work** — 35 seconds. Users can correct or stop
   the agent while the harness remains able to save/push committed work and clean up.
9. **Reporting usage and reserving a handoff** — 35 seconds. Show usage while
   work happens and reserve turns for the agent's handoff.
10. **Checking what tools return** — 35 seconds. Explain attempts to hide
    instructions in tool results, the checks we preserved, and why interactive
    runs need them. Keep classifier parser details in the notes.
11. **Applying the model settings we choose** — 35 seconds. Our effort choice
    must reach the provider; output-size overrides remain optional.
12. **Detecting a response that was cut short** — 35 seconds. A provider-reported
    output limit must produce an error rather than apparent task completion.
13. **Coolstore planning demo** — 180 seconds, including the 2:44 MP4. Show the
    question, approvals, guidance, revisions, final approval and committed plan.
    All implementation/result links are clickable on this slide.
14. **Questions** — invite runtime discussion, then advance into operator adoption.

The run produced a plan and passed five current planning checks; application
migration/build/deployment were not performed. The runtime fork is pinned to
4039163, based on upstream 5ea23f3. The November 11 upstream inspection at
a23005e8 is a separate source review, not the deployed runtime or a new live test.
The actual pre-write checkout was b5752b9; analysis uses its direct child 32c1722.
Only .vscode/extensions.json differs. Preserve those evidence boundaries.

## Operator problems and possible integration paths

Slides 15–21 add about **5 minutes 25 seconds** before discussion. These slides
state the adoption requirements and concrete integration work without selecting
an upstream API or asserting a roadmap commitment. The
[source research](../research/lightspeed-operator-integration-2026-11-11.md)
backs the current behavior and discussion paths.

15. **Lightspeed as the execution backend** — 35 seconds. Candidate delegation
    from our reconciler to AgenticRun; Lightspeed owns SandboxClaim lifecycle,
    while the Go harness retains the application workload. Agent Sandbox stays underneath.
16. **Roadblock: selecting the workload image** — 55 seconds. Java/JDK/Maven and
    Go workloads require different images concurrently. Per-invocation image and
    harness startup are firm requirements; image/profile selection is a possible contract.
17. **Problem: workload placement and access** — 60 seconds. Current central
    placement, cluster-scoped Agent/provider, namespaced Secrets, and complete
    Hub/model/Git/MCP/ACP access must be qualified together. Placement is open.
18. **Problem: harness input and run completion** — 60 seconds. Existing batch
    files and Result RBAC may support a harness adapter. Input/outcome mapping,
    verification/push/cleanup and Result publication need qualification.
19. **Problem: stopping the agent or its workload** — 55 seconds. Graceful ACP
    cancellation inside a live Pod differs from operator hard stop and whole-process
    deadlines. Discuss a finalization allowance and publication failure behavior.
    PR588 is an open hard-stop proposal, not merged graceful-cancellation support.
20. **OpenShell roadmap and integration contract** — 60 seconds. Ask whether it
    is planned and when; discuss image, policy, credentials, Result identity,
    ACP and lifecycle implications if it is. No public dated commitment was found.
21. **Questions** — discussion of the operator problems and possible collaboration.

## Diagrams and rendering

The current-flow and runtime-swap diagrams are editable SVGs:
[konveyor-run-today.svg](konveyor-run-today.svg) and
[lightspeed-runtime-swap.svg](lightspeed-runtime-swap.svg).
They narrow the scope of [Ian's PR diagram](https://github.com/konveyor/agentic-controller/pull/280#issuecomment-6372032054)
to the runtime integration being demonstrated. Backend alternatives belong in the
later operator discussion.

The PR contains the editable Markdown deck, the matching rendered PDF, and the
separate MP4 demo. Present the PDF and switch to the video on the demo slide.
Slide 13 links to the exact changes in each repository; the video ending names
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
