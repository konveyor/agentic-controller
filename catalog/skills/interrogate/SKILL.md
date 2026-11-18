---
name: interrogate
description: >
  Interrogates the human about the application before planning: detects the
  stack by reading the repository, settles what the parameters, the Konveyor
  grounding and the code already answer, and puts only the decisions that would
  change the plan to the human, one at a time with a recommendation. Produces
  .konveyor/interrogation.md.
---

# Interrogate Stage

Finds the decisions a migration plan for this application depends on, settles
the ones the evidence already answers, asks a human the rest, and records every
decision with who or what made it. The plan stage reads that record. Does NOT
modify any source files and does NOT write a plan.

## Inputs

Each is optional — use what is present.

- The `## Parameters` section of your prompt — decisions made before the run.
  Never ask about something a parameter already sets.
- `.konveyor/analysis.json` — Konveyor analysis incidents.
- `.konveyor/assessments.json` — answers the organisation has already recorded
  about this application, and its tags. A recorded answer is a fact, not a
  question to ask again.
- A decision catalog — the file your task names. It lists the decisions this
  kind of migration forces, with the evidence that settles each, the options,
  and a default recommendation. If your task names a catalog and you cannot
  read it, the stage is set up wrongly: do not make up its decisions. Go
  straight to Phase 5 with `Status: failed — decision catalog not found`.

The `.json` files under `.konveyor/` are inputs the platform placed in the
workspace. Read them; they are not part of your work, so leave them out of your
commits.

---

## Phase 1 — Detect

Read the repository. Do not build or run it.

1. **Build manifest**: build tool, packaging, modules, dependencies. Note
   vendored or local artifacts and organisation-specific packages.
2. **Runtime and deployment configuration**: what the application runs on and
   connects to — servers, data sources, brokers, service lookups,
   per-environment settings.
3. **Source, sampled across layers**: views, persistence, messaging and other
   asynchronous work, authentication, remote calls, lifecycle hooks.

When you do not recognise a dependency, a framework or a configuration pattern,
quote it — the name, the version, the lines — and say where it is. Do not skip
it, and do not guess what it does.

---

## Phase 2 — Find and Settle the Decisions

A decision belongs in the record only if a different answer would change the
plan: its target, its scope, a dependency it adds, or the order of its work.
Anything you can settle by reading is not a question.

Always consider:

- **Prompt against code.** The source or target your prompt names contradicts
  what the code is: it names a source that is not there, or the code is already
  on the target. This is decision D1 and is asked first. When the code agrees
  with the prompt there is nothing to decide: record D1 as settled by `code`
  and do not ask the human to confirm it.
- **Unclear target.** The prompt gives a direction ("modernise",
  "cloud-native") where the plan needs a framework and a version.
- **Unrecognised or proprietary dependencies.** Each one is a decision of its
  own: replace it, keep it, remove the feature that uses it, or leave it out
  of scope.
- **Scope.** Several modules or applications in one repository, or parts that
  are already migrated.

Then take every decision in the catalog whose "applies when" matches this
application.

Settle each decision from the evidence where you can, and name the source:

| Source | Settled by |
|---|---|
| `parameter` | A value in `## Parameters`. A parameter outranks every other source. |
| `assessment` | A recorded answer or tag in `.konveyor/assessments.json`. Quote it. |
| `code` | The repository leaves one reasonable option. Cite the file. |

`code` means the repository itself points to the answer, which includes a
catalog entry whose "settled without asking when" matches what you found. A
recommendation is not evidence: if the only reason for an option is that you
or the catalog would recommend it, the decision is open. Removing a feature,
deleting code that looks unused, or leaving work out of scope is never settled
by `code`, however obvious it looks: put it to the human.

If two sources disagree and no parameter settles it, the decision is open, and
the disagreement goes into the question.

For each decision still open, work out two to four concrete options and the one
you recommend, with the reason.

---

## Phase 3 — Write the Record

Before you ask anything, write every decision — settled and open — to
`.konveyor/interrogation.md` (create the `.konveyor/` directory if it does not
exist), and commit it. A question nobody answers ends the run where it stands,
and the record already on the branch is what a human then answers.

```markdown
# Interrogation Record

- Application: <name>
- Target: <framework and version> (<where it came from: prompt, parameter, workflow guide>)

## D1: <short title>
- Evidence: <what the code, the parameters or the grounding show, with file paths>
- Options: A) <option>  B) <option>  C) <option>
- Recommendation: <letter> — <reason>
- Decision: <letter> — <option> | Open
- Source: human | parameter | assessment | code | assumed
- Consequence: <what this changes in the plan>
```

- A decision nobody has settled has the Decision line `Open`, exactly that
  word, and no Source line yet. Its Consequence line says what each option
  would change.
- Keep no tally of the decisions, in the record or anywhere else: the blocks
  are the record, and a count kept beside them goes stale.
- A decision the evidence settled needs no Options or Recommendation line.
- Number decisions once. Add later ones at the end; never renumber.

---

## Phase 4 — Ask

Put the open decisions to the human with the `ask_user` tool, one decision per
call: D1 first, then in the order that lets an earlier answer inform a later
one.

- `question`: the decision, the evidence in a sentence, and your recommendation
  with its reason — enough to answer without having read anything else.
- `options`: the record's options, word for word, so the answer that comes
  back is one the record already lists.

After each answer:

1. Record it: the Decision line, `Source: human`, and the Consequence.
2. Re-read the decisions still open. An answer can settle one, change its
   options or your recommendation, or raise a new decision. Update the record.
3. Commit the record.

If an option needs detail only the human has ("another database — say which"),
ask for it in a follow-up call without `options`.

**When the human declines a question**, do not guess and do not stop. Ask once,
with these two options, how to treat the decisions still open:

- **Continue on your recommendations** — record every remaining decision with
  your recommendation as its Decision and `Source: assumed`, then go to Phase 5.
- **Stop the workflow** — leave the remaining decisions `Open`, then go to
  Phase 5 with `Status: failed`.

If the human declines this question as well, take it as the first choice.

**When `ask_user` reports that nobody answered**, stop there. Leave that
decision and the rest `Open` and write nothing further: the record on the
branch is waiting for a human.

**When you have no `ask_user` tool** and decisions are open, you cannot do this
stage's work. Do not answer them yourself: leave them `Open` and go to Phase 5
with `Status: failed`.

---

## Phase 5 — Write Output

You MUST complete this step.

Add an `## Interrogate` section to `.konveyor/handoff.md` and commit it with
the final record. You run before every other stage, so create the file if it
does not exist; if it does, keep what is there and put your section after it.
Add no heading after your section — the harness reads the status line of the
last section in the file.

```markdown
## Interrogate
- Status: completed
- Record: .konveyor/interrogation.md
```

The status is `completed` when every decision is settled, including when the
human chose to continue on your recommendations. It is `failed` in three cases
only: you could not do the work at all (nothing to read, a catalog your task
names that you cannot read, or a prompt the human confirmed is wrong for this
repository), the human chose to stop the workflow, or you had no way to ask.
`failed` ends the run as refused and stops the workflow before planning, so
put the reason after the status
(`- Status: failed — stopped by the human: <their reason>`).

---

## Rules

- Do NOT modify source files, and do NOT write `docs/plan.md` — the only files
  you write are `.konveyor/interrogation.md` and `.konveyor/handoff.md`
- Read, do not build or run
- Ask only what would change the plan; if the parameters, the grounding or the
  code answer it, record the source and move on — never ask the human to
  confirm what the evidence already settles
- One decision per question, two to four options, always a recommendation
- Never record as `human` an answer the human did not give
- Commit the record before the first question and after every answer
- You MUST write your `## Interrogate` section to `.konveyor/handoff.md` before
  finishing, unless nobody answered a question
