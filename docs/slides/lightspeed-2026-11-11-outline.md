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

The deck has **20 slides**. Seven focused runtime slides explain upstream
behavior, the architecture we changed, why Konveyor needs it, and the components
involved. Detailed file/line references are in speaker notes. The demo slide
holds the clickable PR, runtime, UI, and result links. A dedicated Questions
slide leads directly into the operator problem set. There is no pause/continue
choice in the presentation.

## Runtime walkthrough

Slides 1–13 target about **9 minutes 15 seconds**, including the three-minute
demo section. Discussion is additional. Lead with the architectural change on
each technical slide; use the deeper notes to answer questions.

1. **Adopting Lightspeed’s DeepAgents runtime in Konveyor** — 15 seconds.
2. **Runtime integration** — 15 seconds. Setup, runtime changes, demo, questions.
3. **Konveyor with Goose** — 45 seconds. Existing controller, Sandbox, harness,
   runtime and Git responsibilities.
4. **Konveyor with Lightspeed** — 45 seconds. Replace the runtime box; keep the
   harness preparation/finalization and current execution backend.
5. **Prompt completion and session lifetime** — 35 seconds. Move graph, model
   clients and checkpoints from one query into a reusable in-memory session.
6. **Human interaction through runtime callbacks** — 35 seconds. Tool permission
   and ask_user are independent host callbacks in the session core.
7. **ACP stays outside the session core** — 35 seconds. Separate adapter maps
   those controls to the existing harness tee and UI transport.
8. **Steering and cancellation during a prompt** — 35 seconds. Model-boundary
   guidance, awaited local cancellation, and consistent checkpoint tool history.
9. **Usage reporting and turn-limit handoff** — 35 seconds. Cumulative completion
   observations and explicit prompt budgets let the harness reserve same-session
   wind-down. No claim of complete cost accounting.
10. **Tool-output inspection in the session path** — 35 seconds. Preserve both
    baseline gates; identify the narrow human-input/parser changes. Current
    upstream redaction/shaping work must be reconciled before contribution.
11. **Model settings and incomplete generations** — 35 seconds. Provider effort
    mapping, an optional main-output allowance, and explicit known-cutoff failure.
12. **Coolstore planning demo** — 180 seconds, including the 2:44 MP4. Show the
    question, approvals, guidance, revisions, final approval and committed plan.
    All implementation/result links are clickable on this slide.
13. **Questions** — invite runtime discussion, then advance into operator adoption.

The run produced a plan and passed five current planning checks; application
migration/build/deployment were not performed. The runtime fork is pinned to
4039163, based on upstream 5ea23f3. The November 11 upstream inspection at
a23005e8 is a separate source review, not the deployed runtime or a new live test.
The actual pre-write checkout was b5752b9; analysis uses its direct child 32c1722.
Only .vscode/extensions.json differs. Preserve those evidence boundaries.

## Operator problems and possible integration paths

Slides 14–20 add about **5 minutes 25 seconds** before discussion. These slides
state the adoption requirements and concrete integration work without selecting
an upstream API or asserting a roadmap commitment. The
[source research](../research/lightspeed-operator-integration-2026-11-11.md)
backs the current behavior and discussion paths.

14. **Lightspeed as the execution backend** — 35 seconds. Candidate delegation
    from our reconciler to AgenticRun; Lightspeed owns SandboxClaim lifecycle,
    while the Go harness retains the application workload. Agent Sandbox stays underneath.
15. **Roadblock: selecting the workload image** — 55 seconds. Java/JDK/Maven and
    Go workloads require different images concurrently. Per-invocation image and
    harness startup are firm requirements; image/profile selection is a possible contract.
16. **Problem: workload placement and access** — 60 seconds. Current central
    placement, cluster-scoped Agent/provider, namespaced Secrets, and complete
    Hub/model/Git/MCP/ACP access must be qualified together. Placement is open.
17. **Problem: harness input and run completion** — 60 seconds. Existing batch
    files and Result RBAC may support a harness adapter. Input/outcome mapping,
    verification/push/cleanup and Result publication need qualification.
18. **Problem: stopping the agent or its workload** — 55 seconds. Graceful ACP
    cancellation inside a live Pod differs from operator hard stop and whole-process
    deadlines. Discuss a finalization allowance and publication failure behavior.
    PR588 is an open hard-stop proposal, not merged graceful-cancellation support.
19. **OpenShell roadmap and integration contract** — 60 seconds. Ask whether it
    is planned and when; discuss image, policy, credentials, Result identity,
    ACP and lifecycle implications if it is. No public dated commitment was found.
20. **Questions** — discussion of the operator problems and possible collaboration.

## Diagrams and rendering

The current-flow and runtime-swap diagrams are editable SVGs:
[konveyor-run-today.svg](konveyor-run-today.svg) and
[lightspeed-runtime-swap.svg](lightspeed-runtime-swap.svg).
They narrow the scope of [Ian's PR diagram](https://github.com/konveyor/agentic-controller/pull/280#issuecomment-6372032054)
to the runtime integration being demonstrated. Backend alternatives belong in the
later operator discussion.

The PR contains the editable Markdown deck, the matching rendered PDF, and the
separate MP4 demo. Present the PDF and switch to the video on the demo slide.
Slide 12 links to the exact changes in each repository; the video ending names
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
