---
adr: "0021"
title: "Interrogation Stage: ask the human about the application before planning"
description: "Puts human interrogation of the target application in a workflow stage of its own, driven by skills and carried by ask_user, with the answers committed as the plan's input; the controller gains nothing and the harness only delivers grounding."
status: proposed
status_note: "Spike outcome for konveyor/agentic-controller#253. Recommends the seam and the mechanism; the stage, skill, and harness grounding it describes are follow-up work."
date: "2026-10-23"
last_updated: null
authors:
  - "Ian Bolton"
last_reviewed: "2026-10-23"
implementation_status: deferred
review_note: "Nothing in this ADR is implemented. The askUser opt-in (ADR 0017), the Refused outcome on a handoff verdict (ADR 0018/0020), and per-stage execution overrides it builds on are shipped; the interrogate stage, skill, record, and assessment grounding are not."
relates_to:
  - "ADR 0007 (harness as thin runner; git as cross-stage memory)"
  - "ADR 0010 (skill content boundary)"
  - "ADR 0013 (platform-resolved params)"
  - "ADR 0014 (skill loading; a stage's driver is a rule)"
  - "ADR 0017 (ask_user and ACP elicitation; the stage-gate / in-turn split)"
  - "ADR 0018 (execution fields per stage; Succeeded condition)"
  - "ADR 0020 (run outcome model; Refused)"
  - "konveyor/agentic-controller#253 (this spike), #254 (ADRs in the target repo), #255 (skills in the target repo), #128 (continuation runs)"
---

# ADR 0021: Interrogation Stage — ask the human about the application before planning

## Context

Today analysis goes straight to planning. The `java-ee-to-quarkus` workflow
runs `plan → execute → verify`; the plan stage reads `.konveyor/analysis.json`
and the code, and where "the prompt, target, or source left something
unclear" it chooses for itself and lists the choice under *Key Decisions
Applied* in `docs/plan.md`. The only way a human takes part before code
changes is opportunistic: with `spec.execution.askUser: true` on the plan
stage the agent *may* call `ask_user` mid-turn (ADR 0017), which the
2026-09-30 demo showed working — the planner asked which database to target
and asked again before committing the plan. Whether it asks, what it asks,
and whether anyone is there to answer are all left to chance.

Issue #253 asks for a planning step where a human is deliberately
interrogated about the application before any plan is written, using the
skills loaded for the agent, with the answers kept as input to the plan (and,
per #254, as the raw material for decision records in the target
repository). It names three things to resolve, and this ADR resolves them:

1. **Where the generic / domain-specific seam lies.** Interrogation
   concepts could live in the controller, in Konveyor-specific code that
   reads Hub's questionnaires, or in a harness layer above the controller.
2. **Whether `ask_user` suffices.** It blocks one tool call on one
   question, and fails the run closed when nobody answers; a
   multi-question interrogation and a headless run both look awkward on it.
3. **What to do with the unshipped questionnaire skill** at
   `harness/experiments/skills/questionnaire/SKILL.md`.

### What the repository already provides

Nothing below is new; the decision leans on all of it.

- **Stages are the unit of sequencing and of execution policy.** An
  AgentWorkflow stage names an Agent, carries instructions, and may override
  `execution` — including `askUser` — for that stage alone (ADR 0018, PR
  #242). Each stage is a fresh Sandbox; the only state that crosses stages
  is what the agent commits to the target branch.
- **A stage can refuse.** The harness reads the `- Status:` line of the
  stage's own `## <Stage>` section in `.konveyor/handoff.md`; `failed`
  exits 3, the AgentRun ends `Succeeded=False{Refused}`, and the workflow
  halts (PR #249, ADR 0020). The harness reads that one line and nothing
  else.
- **`ask_user` is a tool the harness mounts, opt-in per stage.** One call
  carries one question, optionally with fixed options, and blocks the turn
  until a viewer answers. Nobody attached, or no answer within
  `HARNESS_HITL_TIMEOUT_SECONDS` (180 s default, capped at 600 s), cancels
  the question **and fails the run** (exit 1); the model is never handed an
  invented answer. After one timeout the tee treats attached viewers as
  unresponsive and fails later asks immediately. A viewer attaching while a
  question is pending is shown it. A declined question hands the model
  "decide for yourself if you safely can". Partial work is still pushed
  when the gate fails.
- **The harness delivers grounding and never composes.** It writes Hub's
  analysis insights to `.konveyor/analysis.json` before the agent starts;
  it relays human answers verbatim; it authors no commits (ADR 0007). The
  controller interprets no parameter values and never looks inside the pod
  (ADR 0001).
- **A stage's driving skill is a rule** (`type: rule`), always in context,
  because ADR 0014 measured a plan stage that never loaded its own skill.
- **The maintainer skill `grill-me`** already encodes the interrogation
  method this repository uses on itself: one question at a time, a
  recommended answer with every question, and "if a question can be
  answered by exploring the codebase, explore the codebase instead".

### Hub's questionnaires, inventoried

Hub's assessment model (tackle2-hub `docs/questionnaire-yaml.md`,
`shared/api` schemas, `/questionnaires`, `/applications/{id}/assessments`,
`/archetypes/{id}/assessments`):

- A **Questionnaire** is `sections[] → questions[] → answers[]`, each answer
  carrying a **risk** (`red | yellow | green | unknown`), optional
  `rationale` and `mitigation`, `applyTags` (tags stamped on the application
  when chosen) and `autoAnswerFor` (tags that pre-select it). Questions carry
  `includeFor` / `excludeFor` tag filters. The questionnaire carries risk
  `thresholds` and `riskMessages`. Hub seeds a `builtin` questionnaire and
  accepts custom ones by YAML upload.
- An **Assessment** binds a questionnaire snapshot to one application or
  archetype, holds the chosen answers (`selected`), and rolls them up to a
  `risk`, `confidence` and `status`. Creating one for an application copies
  the sections and auto-answers from the application's tags
  (`assessment.PrepareForApplication`).

Two facts about this model decide its role here. First, it is a
**portfolio risk instrument**: its questions are about suitability and
readiness, its answers are coloured by risk, and its output is a risk
roll-up for a report — not the choices a migration plan has to make
("which database in production?", "keep the JSF views or go API-only?").
Second, its answers are **facts the organisation has already recorded**
about the application, and they arrive with tags that the analysis and the
code cannot see (team familiarity, compliance constraints, ownership).

### The questionnaire experiment, evaluated

`harness/experiments/skills/questionnaire/SKILL.md` (never shipped) gets
the method right and the mechanics wrong.

Keep: *detect by reading, not building*; *validate the prompt against the
code before asking anything* (a "migrate from X" prompt against code already
on Y is the first question); *only ask what changes the plan*; *describe
what you don't recognise verbatim*; *record the reasoning with every
decision*; *recommend an option every time*.

Drop: its **`non-interactive` mode**, in which the agent picks every answer
itself and marks them `needs-confirmation` — that is the self-answering
fail-open that ADR 0017 observed live and rejected, dressed as a feature;
its **`questionnaire.json` output**, a schema the human who must review
and amend the answers cannot comfortably read or edit, and which the plan
skill never consumed; and its **seven fixed decision categories**, which
are domain knowledge (what a Java EE to Quarkus migration forces you to
decide) hard-coded into a stage skill that claims to be language-agnostic.
It also predates native skill loading and the handoff contract, so it
neither names its driver in the stage instructions nor writes a verdict.

Verdict: a starting point for the stage skill's method, a dead end as a
file. Section *Decision 6* says what happens to it.

## Decision

### 1. Interrogation is a workflow stage, not a stage type

The workflow gains a stage, conventionally named `interrogate`, placed
before `plan`. It references an Agent carrying the `interrogate` stage
skill as a rule plus the workflow's domain skills, and sets
`execution.askUser: true` **on that stage alone**. The controller learns
nothing: no new CRD field, no stage `type`, no pause-between-stages
machinery.

A distinct stage type was considered and is not needed. The one thing it
could express — "this stage needs a human present" — is already stated by
`askUser: true`, and the controller could not enforce anything more: it does
not see inside the pod, and whether a human is attached is a property of the
tee, not of the CR. A type would be a label the controller carries but
cannot act on, and ADR 0001 keeps CRDs domain-agnostic. If a UI wants to
tell a user "this workflow will ask you questions", `askUser` on a stage is
the signal, and that is a Hub/UI concern.

Making it a stage rather than a phase of `plan` buys four things:

- **The attended window is short and explicit.** `askUser` is per stage.
  Putting it on `plan` makes a 200-turn planning run attended and failable
  by an unanswered question anywhere in it; the demo's plan-stage asking
  is exactly this. A dedicated stage asks its questions and ends; the
  human is needed for minutes, not for the plan.
- **The answers are reviewable before they are spent.** The stage commits
  its record (Decision 4) to the target branch; a human who was not
  watching can read and amend it in git before `plan` runs. This is the
  stage-gate half of ADR 0017's table, obtained from stage boundaries and
  git rather than from a new mechanism.
- **Refusal costs nothing downstream.** Nobody there, or the human says
  stop: the workflow halts at a stage that spent a few dozen turns reading
  the repository, not after a plan was written.
- **Headless workflows omit it.** A workflow run with no human simply has
  no `interrogate` stage; `plan` falls back to today's behaviour of
  deciding for itself. Nothing special-cases headlessness.

Nothing forbids the degenerate configuration — `askUser: true` on `plan`
with no interrogate stage. It remains what it is today: the agent may ask
opportunistically. This ADR adds the deliberate form; it does not remove
the opportunistic one.

### 2. The seam: controller generic, harness delivers grounding, skills ask

| Layer | Owns | Does not own |
| --- | --- | --- |
| **Controller** (generic) | Stage sequencing; stamping `askUser` and limits per stage; `Refused` halting the workflow. | Any notion of questions, answers, or questionnaires. |
| **Konveyor harness** (`migration-harness`) | Fetching the application's **grounding** from Hub into `.konveyor/` before the agent starts — today `analysis.json`; with this ADR also the application's tags and its assessments (chosen answers and their rationale). Mounting `ask_user`; relaying answers verbatim. | Composing or sequencing questions; reading Hub questionnaires as a question template; writing anything back to Hub; committing. |
| **Skills** (knowledge) | Which decisions this migration forces (domain skill), how to find the evidence for each in the code and in the grounding, how to interrogate (stage skill), what to record. | Execution control: timeouts, turn counting, whether a viewer is present. |

The Konveyor-specific seam is therefore the harness's **grounding fetch**,
and only that. It is the same seam the harness already has: it is the Hub
client (CONTEXT.md: "the harness resolves application metadata from Hub at
runtime"), and fetching an application's assessments and tags into a file
is deterministic work with no judgment in it — ADR 0010's test puts it in
the harness. A different platform's harness delivers its own grounding
files, or none; the stage skill treats every `.konveyor/*.json` input as
optional.

Reading Hub questionnaires to *generate* the interrogation is rejected.
The harness would be composing questions, which inverts the invariant ADR
0017 built on (the harness relays, never composes); the questionnaire's
risk vocabulary is not the plan's decision vocabulary; and a fixed
questionnaire cannot ask about the vendored jar the agent just found,
which is the question most worth asking. Hub's assessments enter as
**answers already given**, not as questions to re-ask: a decision the
grounding settles is recorded with `Source: assessment` and never put to
the human. This is `grill-me`'s "explore the codebase instead" rule,
extended to the inventory.

Which questions to ask is judgment, so it is skill content, split along
ADR 0007's stage/domain line:

- The **domain skill** (e.g. `javaee-to-quarkus`) gains a **decision
  catalog** under `references/`: the choices this migration forces, when
  each applies, the evidence that settles it, the options with their
  trade-offs, and a default recommendation. The experiment's seven
  categories (target clarification, unrecognised dependencies, view layer,
  authentication, persistence, messaging, scope) are the first draft of
  that catalog for Java EE to Quarkus.
- The **stage skill** `interrogate` (a rule) carries the method and the
  output contract and knows no framework: detect; validate the prompt
  against the code; reconcile the catalog against the evidence, the
  parameters, and the grounding; put the residue to the human one decision
  at a time with a recommendation; record everything. It is `grill-me`
  turned on the application instead of on a design.

Answers known before the run — the target version, a house rule — are
**parameters**, not questions (ADR 0009/0013). The stage skill reads
`## Parameters` and does not ask what is already set.

### 3. Questions ride `ask_user`, one at a time — and that suffices, under stated conditions

The turn-blocking model fits the interrogation it has to carry. An
interrogation is a conversation: the next question depends on the last
answer ("PostgreSQL — then is there a schema migration tool in use?"), and
`grill-me`'s "ask the questions one at a time" is the method precisely
because batching loses that. Each `ask_user` call is one decision with its
options as the enum and the recommendation named in the question text;
the human's answer comes back verbatim and re-grounds the model. Nothing
needs to change in the protocol or the tee for a stage of five to fifteen
such calls.

What has to hold, and where each is enforced:

| Condition | Why | Enforced by |
| --- | --- | --- |
| A human is attached for the whole stage. | Each question waits at most `HARNESS_HITL_TIMEOUT_SECONDS`; after one timeout later asks fail immediately. | Decision 1: the stage is short and `askUser` is on it alone. The workflow run should raise the timeout toward its 600 s cap for this stage's reading time. |
| The turn budget covers detection plus the questions. | Every `ask_user` call is a tool call, and a turn. | The stage's `execution.maxTurns`, sized for detection (tens of turns) plus the catalog's upper bound. |
| An unanswered question must not strand the work done so far. | The HITL gate cancels the turn; the agent never gets to write a closing summary. | Decision 4: the record is written and committed **before** the first question, and updated after each answer. |
| A declined answer is not a guess. | `ask_user` tells the model to "decide for yourself if you safely can". | The stage skill: a declined decision is recorded as `Assumed` with the recommendation, never silently applied. |

Two things the model does **not** do, and is not asked to:

- **Run unattended.** ADR 0017 rejected an "ask, then proceed on timeout"
  mode, and this ADR does not reopen it. The unattended path is the
  **record plus the stage boundary**: a run nobody answers fails (exit 1,
  HITL gate) with the questions already on the branch marked `Open` and a
  recommendation beside each; a human fills them in, in git, on their own
  time, and the workflow resumes from `plan` — which is continuation (#128),
  not a new mechanism. The durable, answerable-later half of ADR 0017's
  table is thereby served without a new protocol.
- **Present a form.** MCP elicitation can carry a multi-property schema,
  and `ask_user` deliberately flattens it to one `answer`. A multi-question
  form would cut round trips at the price of the adaptivity above, and
  would need UI work in tackle2-ui. Deferred; not needed for the stage to
  work.

One small extension to `ask_user` is worth making alongside the stage
and is recorded as a follow-up to ADR 0017, not a condition of this one:
an optional `recommendation` argument naming the recommended option, so a
console can render it as the default instead of the model having to spell
it out in the question text.

### 4. The record: `.konveyor/interrogation.md`, committed by the agent, consumed by `plan`

The stage's output is one Markdown file on the target branch,
`.konveyor/interrogation.md`, written by the agent and committed by the
agent — the harness commits nothing (ADR 0007), and #254's constraint that
"the run writes a file" must mean "the agent is instructed to write and
commit it" is met without touching the content boundary. It is Markdown,
not JSON, because its first reader is the human who may need to amend it
and its second is `plan`, which reads prose fine; and because every block
is shaped to become one decision record if #254 chooses to promote it.

One block per decision:

```markdown
# Interrogation Record

- Application: coolstore
- Target: Quarkus 3 (workflow guide)
- Decisions: 7 — decided 4, known 2, assumed 1, open 0

## D1: Production database
- Evidence: `META-INF/persistence.xml` names an H2 datasource; no production
  datasource configuration is in the repository.
- Options: A) PostgreSQL  B) Keep H2  C) Another database (say which)
- Recommendation: A
- Decision: A — PostgreSQL
- Source: human
- Consequence: build config adds `quarkus-jdbc-postgresql`; the datasource
  step in Phase 1 targets PostgreSQL; H2 stays for tests only.
```

`Source` is one of `human` (answered through `ask_user`), `parameter`
(set on the run), `assessment` (settled by a Hub assessment answer or
tag), `code` (settled by evidence in the repository), or `assumed` (the
human declined, or the question is still open and the recommendation
stands provisionally). A decision whose `Decision` line is `Open` has not
been settled by anyone; the summary line counts them.

Order of operations, which is what makes the record survive a failed gate:

1. Detect and reconcile. Write the record with every unsettled decision
   marked `Open`, its evidence, options and recommendation filled in.
   Commit.
2. Ask, one decision at a time. After each answer, update that block and
   commit.
3. Write the handoff section and commit:

```markdown
## Interrogate
- Status: completed
- Record: .konveyor/interrogation.md
- Open decisions: 0
```

`Status: failed` is reserved for the stage being unable to do its work at
all (no repository to read, a prompt that contradicts the code and a human
who confirmed the run is wrong); it ends the run `Refused` by the existing
contract and stops the workflow. A record with `Open` decisions and a
human who chose to stop is `completed` with a non-zero open count — the
human decided, and `plan` proceeds on the recommendations.

The **plan skill** gains one input and one rule: read
`.konveyor/interrogation.md` if present; every decision in it appears
under *Key Decisions Applied* with its source, and `Assumed` decisions are
flagged there as assumptions. The plan may not silently contradict a
`human`-sourced decision; where the evidence forces it to, it says so in
*Notes*. Nothing about `plan`'s output structure otherwise changes.

Relationship to the sibling spikes: #254 decides whether and how these
blocks become ADRs in the target repository; the block shape above
(evidence → decision → consequence) is chosen so that promotion is a
reformat, and the `Source` line gives it the provenance it asks for. #255
decides which skills stay in the repository after the run; the
`interrogate` stage skill is run machinery and clearly does not, while the
domain skill's decision catalog is exactly the "durable knowledge about the
resulting stack" it is looking for.

### 5. Hub assessments and tags are grounding, read-only

The harness fetches, for the application it was given, its tags and its
assessments — the chosen answer text, the answer's `rationale` and
`mitigation`, the question text, and the questionnaire name — and writes
them to `.konveyor/assessments.json` next to `analysis.json`, uncommitted,
before the agent starts. The plan-ladder preparation rung reports their
count the way it reports analysis insights. The stage skill treats the file
as optional and reads it as **already-answered questions**: a decision it
settles is recorded `Source: assessment` with the answer quoted, and is not
asked.

The harness does not write assessments, apply tags, or create anything in
Hub. The run's token is scoped for reading the application; the issue puts
Hub's questionnaire model out of scope; and a migration run's decisions
are not a portfolio risk assessment. Whether an interrogation's answers
should ever flow *back* into Hub (as tags, say) is a Hub question for
after #254 settles where decisions live.

### 6. The questionnaire experiment is superseded

`harness/experiments/skills/questionnaire/SKILL.md` is superseded by the
`interrogate` stage skill and the domain decision catalog described in
Decision 2, which carry forward the parts of it listed as *Keep* above. It
is deleted when the catalog skill lands; until then it stays where it is,
labelled by this ADR as not the direction.

## What changes

None of this is in this ADR's PR. It is the follow-up issue's scope, in
the order the dependencies suggest.

1. **Catalog skill `interrogate`** (`catalog/skills/interrogate/SKILL.md`,
   shipped `type: rule`): the method of Decision 2, the record and handoff
   contract of Decision 4, the `Source` vocabulary, and the asking rules of
   Decision 3 (one decision per `ask_user`; options as the enum; the
   recommendation named; a declined answer recorded as `Assumed`). A sketch
   is in the appendix.
2. **Decision catalog for `javaee-to-quarkus`**
   (`catalog/skills/javaee-to-quarkus/references/decisions.md`), seeded
   from the experiment's seven categories, each with: when it applies,
   evidence to look for, options, recommendation.
3. **Defaults**: an Agent `migration-interrogate-agent` (same image as the
   plan agent, skill cards `interrogate` + the domain collection), and an
   `interrogate` stage at the head of `agentworkflow_javaee_to_quarkus.yaml`
   with `execution: {askUser: true, maxTurns: <sized per 1>}` and
   instructions that name the skill, per ADR 0014's finding. The
   headless-variant question (ship a second workflow without the stage, or
   document removing it) is decided with the defaults, not here.
4. **Plan skill**: the one input and one rule of Decision 4.
5. **Harness**: fetch tags and assessments for `APP_ID` into
   `.konveyor/assessments.json` (new `hub.Client` method; Hub's
   `/applications/{id}` carries tags, `/applications/{id}/assessments` the
   assessments); report the count on the preparation rung. Nothing about
   questions.
6. **ADR 0017 follow-ups** it already lists, now with a consumer: a
   longer default timeout for questions than for permission asks, and the
   optional `recommendation` argument.
7. **Docs**: CONTEXT.md gains *Interrogation*, *Interrogation Record* and
   *Grounding* (done alongside this ADR); `docs/entry-point.md` and
   `harness/README.md` list the new grounding file when 5 lands.
8. **Experiment**: delete `harness/experiments/skills/questionnaire/` with 1.

## Alternatives considered

**Interrogation inside the plan stage (`askUser` on `plan`).** The demo's
configuration. Rejected as the deliberate form for the reasons in
Decision 1: the attended window is the whole plan, a question at turn 150
fails 150 turns of work, and the answers are never a reviewable artifact
on their own. Kept as the opportunistic form.

**A harness-driven questionnaire.** The harness reads a Hub questionnaire
(or a questionnaire param), puts each question to viewers through the tee
before the agent starts, and writes the answers into `params.json`.
Deterministic and headless-safe in shape, but it makes the harness compose
and sequence questions — the thing ADR 0017 keeps it from doing — and
every question has to be known before anyone has read the code, which
rules out the questions that matter most. A fixed up-front form belongs in
the UI as parameters (ADR 0013), where Hub already has one.

**A distinct stage type, or a controller pause between stages.** A
`type: gate` stage the controller holds until a human marks it passed
would give a durable stage-gate for approving the plan as well. Rejected
here because interrogation does not need it (Decision 1 and 3 get the
durable path from the record and `Refused`), and because approving a plan
before `execute` is a different decision with its own consumers that
deserves its own ADR rather than a side effect of this one. The controller
stays domain-agnostic and semantics-free about stages.

**Hub questionnaires as the question set.** Their answers are facts, their
questions are about risk, and they are fixed before the code is read.
Taken as input (Decision 5), rejected as template.

**A multi-question elicitation form.** Fewer round trips, no adaptivity,
tackle2-ui work. Deferred (Decision 3).

**Ship the experiment's skill as is.** Its `non-interactive` mode is the
fail-open ADR 0017 rejected, and its JSON output serves neither the human
nor `plan`. Superseded (Decision 6).

## Consequences

- A human can be interrogated before planning with no controller change
  and no protocol change. The cost is one extra Sandbox and clone per
  workflow run, and a stage whose `maxTurns` must be sized for the
  catalog.
- The seam is named: Konveyor-specific knowledge enters through the
  harness's grounding files and through skills; the controller and the
  `ask_user` carrier stay generic. Another platform's harness supplies its
  own grounding or none, and the stage skill still works.
- `ask_user`'s turn-blocking model is confirmed sufficient for attended
  interrogation, with the headless case handled at the stage boundary
  rather than by weakening the gate. ADR 0017's "no proceed-on-timeout"
  stance stands.
- Interrogation answers become a committed, human-editable artifact with
  provenance, which #254 can promote and #255 can classify. The plan
  becomes traceable to who decided what.
- The human must be present for the stage. A run started and walked away
  from will fail at its first question with the questions already on the
  branch; resuming from `plan` after editing the record depends on
  continuation (#128) or, until then, on running a workflow that omits the
  stage against the same branch.
- Who answered is not recorded by name: the tee relays answers without
  identity, and who may write on the stream at all is Hub's authorization
  question (tackle2-hub#1116). `Source: human` is as specific as the
  record can honestly be today.
- The decision catalog is a new kind of domain-skill content that authors
  of other migration skills will need to write for interrogation to ask
  anything useful about their stack. Without one the stage still runs, and
  asks only what the generic method surfaces (prompt mismatch, unrecognised
  dependencies, scope).

## Appendix: sketch of the `interrogate` stage skill

Not the shipped text; the shape the follow-up implements.

```markdown
---
name: interrogate
description: >
  Interrogates the human about the application before planning: detects
  the stack by reading the repository, reconciles what the parameters,
  the Konveyor grounding and the code already settle, and puts only the
  decisions that would change the plan to the human, one at a time with
  a recommendation. Produces .konveyor/interrogation.md.
---

# Interrogate Stage

Planning only — modify no source files. Read, do not build.

## Inputs (each optional)
- `## Parameters` in your prompt — decisions already made; never ask these.
- `.konveyor/analysis.json` — analysis incidents.
- `.konveyor/assessments.json` — the organisation's recorded answers about
  this application; treat a chosen answer as a fact.
- The domain skill's `references/decisions.md` — the decisions this
  migration forces, with evidence, options and a recommendation each.

## Method
1. Detect: manifest, layout, layers, runtime config, auth, persistence,
   messaging, views. Quote what you do not recognise.
2. Validate: if the prompt's source or target contradicts the code, that
   is decision D1 and is asked first.
3. Reconcile: for each catalog decision that applies, settle it from
   parameters, assessments or code where you can, with the source named.
4. Write `.konveyor/interrogation.md` with every unsettled decision marked
   `Open`, its evidence, options and your recommendation. Commit.
5. Ask the open decisions one at a time with `ask_user`: options as the
   choices, your recommendation named in the question. Record each answer
   and commit. A declined answer is recorded as `Assumed` with your
   recommendation; never apply a guess silently.
6. Append `## Interrogate` to `.konveyor/handoff.md` with `- Status:`,
   the record path and the open count. Commit.

## Rules
- Only ask what would change the plan. If the code or the grounding
  answers it, record the source and move on.
- One decision per question; two to four options; always a recommendation.
- `Status: failed` only when you could not do the work at all.
```
