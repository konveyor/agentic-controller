# Konveyor and Lightspeed presentation guide

November 11, 2026. The editable [slide deck](lightspeed-2026-11-11.md) contains
on-slide text and speaker notes. The [discussion brief](../briefs/lightspeed-2026-11-11.md)
contains detailed evidence, and the [demo plan](lightspeed-2026-11-11-demo.md)
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

Slides 1–7 target **8 minutes 40 seconds**, including a 2 minute 30 second demo.
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
5. **Lightspeed runtime changes** — 160 seconds. Explain what changed in their
   runtime: reusable sessions, human controls, the optional ACP adapter and token
   observations. Use limit-triggered handoff as the same-session example. Cover
   preserved inspection, evidence boundaries and the remaining cost/accounting
   work in the notes before the demo, without separate detour slides.
6. **Coolstore planning demo** — 150 seconds. Establish the application and task,
   show the human interaction, then inspect its effect in the plan and Git result.
7. **Runtime questions** — 30 seconds to invite feedback, then stop. Ask whether
   session support and the optional ACP contribution are useful, and how the
   community would like to review them. No slide walkthrough follows this demo
   before questions.

The live Coolstore recording remains pending setup and rehearsal. Cluster access
has been supplied and is being checked separately. Keep the recording placeholder
until actual evidence is reported. Before presenting, update the demo notes with
its observed result and scope. Keep the existing scripted evidence distinct from
the live demo. Planning does not establish a completed application migration.

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

The deck uses the existing Konveyor Marp theme. Render a PDF with:

```sh
make slides DECK=docs/slides/lightspeed-2026-11-11.md
```

For a scrollable browser preview, render bare HTML and copy the three SVG assets
into the same local preview directory:

```sh
mkdir -p .scratch/lightspeed-slides
cp docs/slides/konveyor-logo.svg docs/slides/konveyor-run-today.svg \
  docs/slides/lightspeed-runtime-swap.svg .scratch/lightspeed-slides/
npx --yes @marp-team/marp-cli \
  --theme docs/slides/konveyor-theme.css \
  --template bare docs/slides/lightspeed-2026-11-11.md \
  -o .scratch/lightspeed-slides/index.html
```

Keep generated preview files local. Commit edits to the Markdown and SVG sources.
