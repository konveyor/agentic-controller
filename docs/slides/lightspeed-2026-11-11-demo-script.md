# Coolstore runtime demo

Final edit: `lightspeed-2026-11-11-demo.mp4` — 2 minutes 36 seconds, silent, with captions. Suggested live narration follows. Pause speaking briefly over the database question and the plan sections so the audience can read them.

- **0:00–0:24 — What is running.** “This is Coolstore, our Java EE example. We already have its source analysis. We are asking for a Quarkus migration plan, using our existing Konveyor flow. The selected Agent image contains our harness and the modified Lightspeed runtime. Questions and tool approvals are enabled.”
- **0:24–0:44 — A real question.** “The agent asks whether the future Quarkus application should reuse PostgreSQL or use a separate service. We choose the existing service. That decision now belongs in the plan.”
- **0:44–0:52 — Guidance during a turn.** “We can also give guidance while the agent works. This message is queued for a later model call. We ask it to explain the consequences of database reuse, transaction boundaries, and testing risks.”
- **0:52–1:15 — Review and revision.** “The runtime asks permission to write the plan. Later it asks whether it may commit. We request changes first and review its edits in the same conversation. The full run had more edits and reviews than this excerpt shows.”
- **1:15–1:39 — Approving the commit.** “Once the revised plan is acceptable, we approve the local commit and the actual Git command. The harness handles verification and pushing. The agent runtime does not receive push credentials.”
- **1:39–2:16 — What came out.** “Here is the committed plan. It records the PostgreSQL choice and requires a schema and migration-history review before migration work. It identifies transaction work, testing gaps, and open questions. These checks verify the plan and unchanged source. The application migration and its future validation have not been performed.”
- **2:16–2:36 — Result and limits.** “The run succeeded and the harness passed all five planning checks before pushing the approved commit. We have token usage, with classifier calls excluded; this is not a cost total. The running workload was verified against these Lightspeed and harness revisions. The result is an approved plan in Git.”

## Editing disclosure

All moving footage comes from the single successful AgentRun `ui-2rhxw`. The actual run took **28 minutes 9 seconds**. Moving interactions play at normal speed. Idle waits and repeated edit/review cycles are omitted; result sections are arranged for explanation.

There are **three explicitly labelled reading pauses**: four seconds holding an exact queued-guidance frame extracted from the run video, and twelve seconds each holding the take’s exact database-decision and verification screenshots. Crops enlarge selected regions. The setup account name is covered with a “Demo account” label. UI and plan text are unchanged. No generated replies, failed takes, synthetic terminal output, or supplementary recordings are used.

## Verified result and scope

Independent checks confirmed terminal success (`Succeeded`, exit 0, `Verified=True`, `VerificationPassed`), all five current planning checks, and the remote GitHub commit/tree/blob. This edit shows representative interactions and the result; it does not display every check execution.

- Commit: `9e97db02b641dc2608fcc399503bbc1a0ed3b842`
- [Committed plan](https://github.com/djzager/coolstore/blob/9e97db02b641dc2608fcc399503bbc1a0ed3b842/docs/plan.md): 141 lines, 13,457 bytes; SHA-256 `fc700a9a04d77288b821325f323db5fcfb579bda8d956436963e9715a26aa621`.
- Only `docs/plan.md` was added. Application source, dependencies, and build configuration remain unchanged. No application migration, build, deployment, or migration tests were performed.
- Reported usage: 33 native model calls, 563,794 input tokens, 10,391 output tokens. Inspection-classifier calls are excluded; monetary cost is not reported.
- Runtime source: `403916314aa4f869fc7733bef6e20fff32cf2680`; harness source: `73a44d59f9e4cf6644ab4fc48f42211a7eff2548`.
- Image digest: `sha256:1c74a3ea20283cc92a3b3d0ccc24dbd797eb422541c5bec0ba7f37c1e1b934e5`.
- Model: GLM 5.3, effort `low`, per-response output allowance 16,384 (reasoning plus answer).

Source qualification: analysis referenced `32c1722efdf883f5f400170adb770a46a9b58116`; the prepared checkout was `b5752b936f01acd858ca21fd2d3c4f03d3a6321e`. Their only difference is `.vscode/extensions.json`; application and build files are identical. The edit does not claim the run checked out the analysis revision. The committed plan records this qualification.
