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

Slides 1–10 target **8 minutes 50 seconds**, including a 2 minute 30 second demo.
Discussion time is additional. Timings are rehearsal guides.

1. **Trying Lightspeed in Konveyor** — 20 seconds. State the change directly:
   replacing Goose with Lightspeed's DeepAgents runtime.
2. **What we'll walk through** — 20 seconds. Explain the runtime-first agenda
   and optional operator discussion.
3. **How a run works today** — 35 seconds. Use the current-flow diagram to show
   application selection, repository preparation, Goose's work and the Git result.
4. **Lightspeed takes Goose's place** — 40 seconds. Show the same controller,
   Agent Sandbox and harness, with the runtime box changed. The harness still
   prepares the repository, verifies work and pushes.
5. **What we changed in Lightspeed** — 70 seconds. Name the additions: reusable
   sessions, human controls, an optional ACP adapter and token reporting. Explain
   that we reused their existing DeepAgents engine, tools and inspection.
6. **Why we need another prompt** — 60 seconds. Use the turn-limit example:
   ask the same conversation to write down its progress before stopping.
7. **Demo: a plan for Coolstore** — 150 seconds. Establish the application and
   task, show the human interaction, then inspect its effect in the plan.
8. **What works so far** — 45 seconds. Explain what the scripted integration
   tests establish and identify the live-model work still pending.
9. **Token counts now, cost limits next** — 45 seconds. State the gaps plainly
   and propose improving runtime reporting together.
10. **What do you think?** — 45 seconds. Ask about the session changes, optional
    ACP contribution and how they would like to review them. Stop for feedback.

The live Coolstore recording remains pending cluster/model access and rehearsal.
Replace that placeholder and update the evidence slide with the observed result
when available. Keep the existing scripted evidence distinct from the live demo.
A planning result does not establish a completed application migration.

## Optional operator discussion

Slides 11–14 add about **four minutes** before discussion.

11. **Could your operator run our workloads?** — 50 seconds. Explain the need
    to choose an image per run and start its harness. The image's normal entrypoint
    may satisfy startup; a separate command override is not prescribed.
12. **Where would the workload run?** — 65 seconds. Ask how it will reach Hub,
    receive model settings and credentials, and accept the UI connection. Discuss
    the complete access path before choosing a namespace.
13. **Finishing or stopping a run** — 65 seconds. Explain why stopping the agent
    must leave room for verification, Git push, reporting and cleanup. Use successful
    Git push followed by failed Result publication as a concrete failure case.
14. **Working through this together** — 60 seconds. Propose tests with different
    images, credentials, cancellation and reporting failures. Agree a useful
    follow-up without assuming upstream acceptance or a delivery schedule.

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

For a scrollable browser preview, render HTML beside the source so its image
links resolve:

```sh
npx --yes @marp-team/marp-cli \
  --theme docs/slides/konveyor-theme.css \
  --template bare docs/slides/lightspeed-2026-11-11.md \
  -o docs/slides/lightspeed-2026-11-11.html
```

Keep generated preview files local. Commit edits to the Markdown and SVG sources.
