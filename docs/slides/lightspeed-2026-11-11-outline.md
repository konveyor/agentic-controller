# Konveyor and Lightspeed presentation guide

November 11, 2026. The editable [slide deck](lightspeed-2026-11-11.md) contains
on-slide text and speaker notes. The [rendered PDF](lightspeed-2026-11-11.pdf)
and [separate demo video](https://github.com/djzager/agentic-controller/blob/c43abc5e5509fff9ea982a1a170e7bf51ae1aafe/docs/slides/lightspeed-2026-11-11-demo.mp4) are committed alongside it. The [discussion brief](../briefs/lightspeed-2026-11-11.md)
contains detailed evidence, and the [demo guide](lightspeed-2026-11-11-demo.md)
covers setup, recording and the cluster tour.

## The story

Explain how a run works today, show the Goose runtime being replaced with
Lightspeed, then explain what we changed in Lightspeed to make the demo work.
The demo follows that explanation. Keep the operator discussion after the
runtime feedback so the group can choose whether to continue.

Use concrete examples and words we would say aloud. The slides should make
sense before anyone reads the code or ADR. Detailed contracts and qualifications
belong in the brief and speaker notes.

## Runtime section

Slides 1–7 target **9 minutes 30 seconds**, including a three-minute demo section
with the 2 minute 44 second recording.
Discussion time is additional. Timings are rehearsal guides.

1. **Adopting Lightspeed’s DeepAgents runtime in Konveyor** — 20 seconds.
   Introduce the prototype and interest in contributing reusable changes.
2. **Runtime integration** — 20 seconds. Follow the agenda from the Goose setup
   through the Lightspeed prototype and runtime changes, then demo and questions.
3. **Konveyor with Goose** — 70 seconds. Use the current-flow diagram to explain
   application selection, repository preparation, Goose's work and the Git result.
4. **Konveyor with Lightspeed** — 70 seconds. Show the same controller, Agent
   Sandbox and Go harness with the runtime changed. Explain both sides of the
   integration before discussing the runtime contribution.
5. **Lightspeed runtime changes** — 180 seconds. Explain what changed in their
   runtime: reusable sessions, human controls, the optional ACP adapter and token
   observations. Include forwarding explicit reasoning settings and reporting
   output cutoffs. Use limit-triggered handoff as the same-session example. Cover
   preserved inspection, evidence boundaries and the remaining cost/accounting
   work in the notes before the demo, without separate detour slides.
6. **Coolstore planning demo** — 180 seconds. Establish the application and task,
   show the human interaction, then inspect its effect in the plan and Git result.
7. **Runtime questions** — 30 seconds to invite feedback, then stop. Ask whether
   session support and the optional ACP contribution are useful, and how the
   community would like to review them. No slide walkthrough follows this demo
   before questions.

The live Coolstore run completed with a verified, pushed
[migration plan](https://github.com/djzager/coolstore/blob/9e97db02b641dc2608fcc399503bbc1a0ed3b842/docs/plan.md).
The user chose database reuse, reviewed tool permissions, sent guidance, requested
plan changes, and approved the corrected result. All five planning checks passed.
Application source stayed unchanged; this does not establish a completed migration.

The actual pre-write checkout is `b5752b9`; analysis uses its direct child
`32c1722`. Only `.vscode/extensions.json` differs. Keep those revisions distinct
in the walkthrough and final diff. The full run lasted 28 minutes 9 seconds;
the [recording](https://github.com/djzager/agentic-controller/blob/c43abc5e5509fff9ea982a1a170e7bf51ae1aafe/docs/slides/lightspeed-2026-11-11-demo.mp4) is 2 minutes 44 seconds, using
normal-speed excerpts and labelled reading pauses with idle waits omitted. Preserve
the distinction from the November 6/9 scripted evidence.

## Optional operator discussion

After runtime questions, slides 8–12 add about **four minutes** before discussion.

8. **Lightspeed operator discussion** — 20 seconds. A separate agenda makes the
   transition explicit. The group can continue immediately or read these slides
   later. The runtime demo and tour have not deployed the Lightspeed operator.
9. **Workload images and startup** — 50 seconds. Explain the need to choose an
   image per run and start its harness. The image's normal entrypoint may satisfy
   startup; a separate command override is not prescribed.
10. **Placement and credentials** — 65 seconds. Discuss how the workload reaches
    Hub, receives model settings and credentials, and accepts the UI connection.
    Work through that complete access path before choosing a namespace.
11. **Run completion and cancellation** — 65 seconds. Explain why stopping the
    agent must leave room for verification, Git push, reporting and cleanup.
    Use successful push followed by failed Result publication as a failure case.
12. **Operator questions** — 40 seconds. Propose tests with different images,
    credentials, cancellation and reporting failures. Agree useful follow-up
    without assuming upstream acceptance or a delivery schedule.

## Diagrams and rendering

The current-flow and runtime-swap diagrams are editable SVGs:
[konveyor-run-today.svg](konveyor-run-today.svg) and
[lightspeed-runtime-swap.svg](lightspeed-runtime-swap.svg).
They narrow the scope of [Ian's PR diagram](https://github.com/konveyor/agentic-controller/pull/280#issuecomment-6372032054)
to the runtime integration being demonstrated. Backend alternatives belong in the
later operator discussion.

The PR contains the editable Markdown deck, the matching rendered PDF, and the
separate MP4 demo. Present the PDF and switch to the video on the demo slide.
Slide 7 links to the exact changes in each repository; the video ending names
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
