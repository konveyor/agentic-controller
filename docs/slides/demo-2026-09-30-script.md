# Demo script, September 30, 2026

Read against `demo-2026-09-30-reel.mp4` (4:00) in this directory. Times are
where each card or clip starts. Say the line while its clip plays and stop
when the next black card fades in; each card gives two and a half seconds of
air. Lines are sized to fit their clip at a normal speaking pace.

The recording has two halves, each behind a black part card:

1. One run of the `java-ee-to-quarkus` workflow against coolstore, from
   Create workflow run to the branch and the handoff report. Footage from a
   run on a Konveyor 0.11 hub with the downstream UI.
2. The same workflow with `spec.execution.askUser: true` on its plan stage
   and instructions that hand the datasource decision to a human. Footage
   from a later run on the same cluster with three things switched on:
   steering for the UI through the operator (`agentic_steer_enabled` on the
   Tackle CR, which sets `AGENTIC_STEER_ENABLED` on the UI deployment), a UI
   built from `release-0.11` plus konveyor/tackle2-ui#3625, and a hub built
   from `release-0.11` plus konveyor/tackle2-hub#1143, so the `askUser`
   field round-trips.

## Part 1: One workflow run, on a real application

| Time | On screen | Say |
| --- | --- | --- |
| 0:00 | Part card | "A hub with agents on. The operator installed the agents, the workflow and their skills. An admin created a Gateway, the object that points a run at a model, here Claude on Bedrock. Coolstore is registered and analyzed. From here, one workflow run." |
| 0:09 | Create workflow run dialog, then the run page | "A run is three choices: which workflow, which gateway, which application. Create, and the controller starts the first stage in its own sandbox." |
| 0:26 | Plan stage, live session | "Plan is an agent with one job: read the analysis and the source, write docs/plan.md. That file is the contract for the next stage. You are watching its live session, not a summary after the fact." |
| 0:53 | Execute stage | "Execute is a different agent in a fresh sandbox. It gets the plan, not the planner's memory, and works through the steps, recording what it did in a handoff file." |
| 1:14 | Verify stage | "Verify builds it, fixes what does not compile, runs the tests, and appends its own report to the handoff. The stages talk through files in the repo, which is why any of them can be re-run." |
| 1:40 | The finished run, then the branch on GitHub | "Done: three stage runs, each Succeeded, and the stage outputs point at a branch. One commit per stage." |
| 1:55 | The verify commit, then .konveyor/handoff.md | "The handoff report: built, tested, what is left. Git is the memory layer." |

## Part 2: A human in the loop

| Time | On screen | Say |
| --- | --- | --- |
| 2:03 | Part card | "Same workflow, one change: the plan stage may stop and ask. The other two still run headless." |
| 2:12 | Create workflow run, the hitl variant, then the run page | "The hitl variant of the workflow, Opus on Bedrock, coolstore. Nothing to tick. The opt-in lives on the stage, set when the workflow was written." |
| 2:26 | Session panel, 6x | "It starts by reading the analysis already attached to the application, the Konveyor analyzer's results, then the build and the source." |
| 2:37 | The question card, then the answer | "Ninety seconds in it reaches the datasource decision and asks, instead of guessing. The turn is parked: nothing moves until someone answers, and an unanswered question times out with no answer invented. I take the in-memory database; this is a development plan." |
| 2:57 | Typing a redirect; queued, then picked up | "Now the other direction. I type into the running turn: check in with me before you commit. Queued, then picked up at its next step. The run never stopped." |
| 3:11 | Writing the plan, 8x | "It writes the plan around the answer it was given." |
| 3:23 | Plan summary, the second card, the answer, the commit | "And my redirect comes back as a question: a summary, fifty-four steps, and should I commit. A nudge cannot stop an agent. The agent turned it into the one thing that can: the same tool, the same parked turn. Yes, commit." |
| 3:47 | Commit, stage done, execute starting | "Committed. Execute picks up the plan without the ask tool. The plan's first decision reads: database, H2, chosen by the user. Planning, with a human in the loop." |
| 4:00 | Black | Back to slides. |

## If asked

- **Why is asking opt-in?** "An unanswered question would otherwise stall a run nobody is watching. Unattended runs stay headless; a stage somebody watches can ask." (ADR 0017)
- **Is that the model asking, or a prompt trick?** "A tool call. The tool result is what the human said, word for word, or that nobody answered. The model never invents an answer."
- **What about approving every action?** "Separate control, same plumbing: supervision mode `approve` gates each tool call with a permission card. May I, versus which one." (ADR 0011)
- **Can I redirect a run that has no ask tool?** "Yes. Steering is on by default. It just cannot make the agent stop; only a question can."
- **What is next?** "The ask opt-in as a control in the create-run dialog, and durable stage gates: approve the plan before execute runs." (#253, #254)
