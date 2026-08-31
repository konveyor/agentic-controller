---
adr: "0014"
title: "Skill Loading and Prompt Assembly"
description: "Defines native skill discovery, always-loaded rules, and how the loader supplies rule metadata for prompt assembly."
status: proposed
status_note: "Refines ADR 0007's prompt-assembly step and supersedes ADR 0001's runtime-discovery claim; ADR 0007's other decisions stand."
date: "2026-08-11"
last_updated: "2026-08-31"
authors:
  - "Fabian von Feilitzsch"
last_reviewed: "2026-08-31"
implementation_status: amended
review_note: "Native skill discovery, always-loaded rules, and the loader metadata path are implemented; catalog relocation is recorded by ADR 0019."
---

# ADR 0014: Skill Loading and Prompt Assembly

**Update (2026-08-31):** Native runtime discovery, the linked
`~/.agents/skills` root, loader-produced rule metadata, and rule prompt
assembly are implemented. The shipped catalog now lives under
`catalog/skills`; references to the former `skills/plan` paths below are
historical and are reconciled by the catalog-layout ADR.

## Context

ADR 0007 made the harness a thin runner: discover skills by globbing
`/opt/skills/*/SKILL.md`, concatenate them, send one prompt, block until
goose finishes. That concatenation step is the last piece of migration
intelligence still living in Go. goose already had its own skills
implementation when it was written — the two have simply never been
connected, because nothing points goose's discovery at the mount.

The #70 spike established that goose loads skills natively and never sees
the ones we mount. The mechanics are at the end of this document. Two of
them drive everything here: discovery never looks at `/opt/skills`, and
nothing forces a skill into context once it is found.

What the concatenation step costs us in the pod as it ships today:

1. Everything is always loaded. A plan stage with one domain skill
   puts ~10.8 KB of `SKILL.md` in context for the whole run, and the cost
   grows with every skill an Agent references. Most of that particular
   figure is the stage skill (`skills/plan` is 8.2 KB of the 10.8 KB),
   which this ADR keeps always-loaded — the saving is on domain content,
   not on the total.

2. Supporting files are mounted and unreachable.
   `skills/javaee-to-quarkus` ships 12 files (~31 KB) under `modules/`
   and `references/`, and its `SKILL.md` says "Load each phase's module
   when starting that phase" using relative paths — which resolve against
   the clone dir, because the prompt never says where the skill lives.
   `skills/plan`, `skills/execute` and `skills/verify` work around this by
   hardcoding `/opt/skills/*/references/` globs into their markdown. The
   knowledge is in the image and the agent cannot reliably reach it.

3. `SkillCard.spec.type` works only by accident. The enum
   (`skill` | `rule`) is declared, defaulted, and surfaced as a `kubectl`
   print column, and is read by no controller and no harness code.
   `resolveSkillVolumes` mounts a rule at `/opt/skills/<name>` exactly
   like any other skill, and the harness concatenates everything it finds
   there, so rule content does reach the prompt today — because *all*
   content does, not because it is a rule. Remove the blanket
   concatenation and the behaviour goes with it. CONTEXT.md already
   describes `skill` as "only its name and description are loaded at
   startup; the full content activates when the agent invokes it", which
   is a precise description of goose's skills extension and of nothing
   the platform currently does.

4. Nothing can measure the always-loaded budget.
   `Gateway.ContextWindow` carries the doc comment "Used to validate that
   an Agent's always-loaded rules fit within budget." No controller reads
   it, and with everything always loaded there is no smaller quantity for
   it to mean than the sum of every skill an Agent references.

## Decision

**The harness stops assembling skill content. It makes the mounted skills
discoverable by the agent runtime and lets the runtime disclose them
progressively. The harness keeps assembling exactly one thing:
always-loaded rules.**

`type` becomes a load policy, not a role — which is how CONTEXT.md
already defines it. `skill` means the runtime decides when to read it;
`rule` means it is in the prompt unconditionally.

### On-demand skills — `type: skill`

Before starting goose, the harness links goose's home-rooted skill root
at the mount, creating `~/.agents` first since the image does not ship
it:

```text
$HOME/.agents/skills ──▶ /opt/skills
                          ├── plan/SKILL.md          (ImageVolume)
                          └── javaee-to-quarkus/     (ImageVolume)
                              ├── SKILL.md
                              ├── modules/
                              └── references/
```

goose lists each skill's name and description in its system instructions,
and the agent pulls bodies and supporting files through `load_skill` as
it needs them. The harness stops putting skill bodies in the prompt.

Home-rooted rather than in the clone, because `~/.agents/skills` is
outside the worktree the harness pushes. See the alternatives for what
that buys. `/opt/skills` stays the pod's filesystem contract, and
teaching a runtime where to find it is harness work, because the harness
is the runtime-specific layer (ADR 0001, principle 5).

### Always-loaded rules — `type: rule`

No runtime feature guarantees content is in context, so this stays the
harness's job.

Rules stay mounted at `/opt/skills/<name>` with everything else. The
skill loader records which of them are rules, and the harness
concatenates those skills' `SKILL.md` into the prompt, after its own
staging rules and before the stage task.

Rules are therefore both injected and discoverable: unconditionally in
the prompt, and still under the linked root, so a rule that ships
supporting files keeps them.

> **Revised during implementation, 2026-08-17.** This section originally
> had the controller set `KONVEYOR_RULES` from each card's `spec.type`.
> A SkillCard name is not a mount directory, though: one image can carry
> several skills, each mounted at its frontmatter name, which the
> controller never reads. The loader does read it, so it records the
> rules in `/opt/skills/.konveyor-skills.json` and the harness reads that.
> Load policy still comes from `spec.type`. See ADR 0015 §8.

Rules are then the only always-loaded content, which is the quantity
`Gateway.ContextWindow`'s doc comment describes. This ADR does not
implement that check; it makes the quantity well defined.

### Anything that must run is a rule

Under progressive disclosure the agent decides what to read. A stage
skill that might not be loaded is not a driver, so an Agent whose stage
depends on one declares it `type: rule`. ADR 0007's stage/domain split
describes what a skill *is*; `type` describes when it is *loaded*. They
are different axes, and a stage skill is normally both.

Measured on #136, running the five-stage workflow in Kind against
`savitharaghunathan/coolstore#17`, this is worse than the ADR first
assumed. Two distinct failures showed up:

- With generic stage instructions ("Scan the project and gather
  migration decisions") the agent never called `load_skill` at all and
  produced a free-form `questionnaire.json` ignoring the skill's schema.
  Naming the skill in the stage instructions fixed it.
- The plan stage *did* load its skill and read every reference, and
  still wrote `PLAN.md` at the repo root instead of the
  `.konveyor/spec.md` and `.konveyor/implementation.md` the skill
  mandates — even though the skill says "MUST" and "the stage is NOT
  complete until both files are written".

So loading is not the only thing at stake: a skill's output contract was
not reliably followed even once the content was in front of the model.
Why that is has not been established, and this ADR does not claim a
mechanism. What it changes is the strength of the recommendation. Naming
the skill in the stage instructions is the cheap first move and is what
unblocked the run; `type: rule` is the durable one for anything whose
output another stage consumes.

Being a rule is also the only defence against a target repository
shadowing a mounted skill by name, since the harness reads rules from the
mount rather than through discovery.

### Rollback

`HARNESS_SKILL_MODE=inject` restores the concatenation path, matching the
`HARNESS_ACP_TEE=off` precedent from ADR 0008. The default is native. The
switch is self-contained because the mount layout does not change between
modes: `inject` concatenates everything at `/opt/skills`, as today, and
ignores the loader's rules list.

## What changes

- Harness, done in #136. Create `~/.agents` if absent and link
  `~/.agents/skills` at the skills dir before starting goose.
- Harness, done in #136. Stop putting skill bodies in the prompt, keeping
  `discoverSkills` for its path list, which feeds `hasSkills` and so
  gates the `.gitignore` write and the grounding-data fetch. The `#82`
  no-skills fallback carrying the only instruction to commit becomes
  unconditional.
- Harness. Inject the skills the loader's manifest lists as rules as a
  rules layer, ordered after the staging rules and before the stage task.
  Per the revision above, the list comes from the manifest the loader
  wrote rather than from a controller-set variable, so there is no
  sequencing constraint between the two changes.
- Controller. Carry each SkillCard's `spec.type` through to the loader,
  which is what decides a skill's load policy. `resolveSkillVolumes`
  ignored `spec.type` before this.
- Harness. After cloning, log any skill name present in both the clone's
  discovery roots (`.agents/skills`, `.goose/skills`, `.claude/skills`)
  and `/opt/skills`. The repo copy wins and that is not going to change,
  so the point is that the run says so.
- Stage instructions. Name the skill a stage depends on
  ("Load the `questionnaire` skill and follow its instructions"). Cheaper
  than retyping the card and it is what fixed the #136 run.
- Skills. Replace container-layout globs with relative paths. Six hits
  across four files: `skills/execute/SKILL.md` (17, 24),
  `skills/verify/SKILL.md` (16, 42), `skills/plan/SKILL.md` (101) and
  `skills/plan/references/migration-plan-skill.md` (55).
- Existing SkillCards. `type` defaults to `skill`, so a card authored as
  a constraint while blanket concatenation was carrying it is already
  stored as on-demand. Those need retyping before the harness change
  ships, and the change needs a release note.
- CI. Assert a mounted skill reaches a real session, driven through
  `goose serve` rather than `goose skills list`, so a goose bump that
  moves the discovery paths fails loudly rather than silently emptying
  the agent's skill list.

## Consequences

- Domain skills load on demand, so their cost tracks what the run
  actually uses, and `modules/` and `references/` become reachable with
  resolved paths. That is the difference between shipping 31 KB of
  migration knowledge and delivering it.
- What the model reads is no longer fixed before the run starts. A domain
  skill can go unread if its description does not match the work, so the
  frontmatter `description` becomes the entire selection surface and has
  to be written for retrieval rather than for humans.
- Frontmatter becomes load-bearing. A `SKILL.md` without `name` and
  `description` is invisible to goose, where today its bytes are
  concatenated regardless. Nothing validates this — neither the SkillCard
  controller nor the harness parses frontmatter — and CONTEXT.md offers
  inline markdown as a first-class SkillCard source, so such a card still
  resolves, mounts and reports Ready while contributing nothing.
  Validation belongs in the SkillCard controller and is not in this ADR.
- Two namespaces for one name. The controller dedupes mounts by SkillCard
  name, goose dedupes by frontmatter `name`. Two cards that mount
  separately but declare the same frontmatter name collapse to one entry,
  chosen by walk order, with no error from either side.
- Migration is silent. Existing cards relied on as constraints degrade to
  on-demand with no error, no condition and no spec diff. Retyping them
  is a prerequisite, not a follow-up.
- The cloned repository is outside the trust boundary. It is user-owned
  code the platform did not review, and native discovery gives it a way
  to influence the run that it did not have while the harness only read
  `/opt/skills`: a repository containing `.agents/skills`,
  `.goose/skills` or `.claude/skills` shadows a mounted skill of the same
  name, because project roots are scanned first. Deliberate or accidental
  looks identical from inside the pod. For `type: rule` this is closed —
  the harness reads rules from the mount by name and never through
  discovery. For `type: skill` it is open by construction, so the harness
  logs any name that appears in both the clone and the mount, which makes
  it visible in the run rather than silent. Anything load-bearing belongs
  in a rule.
- Discovery paths and the skills extension are goose internals, not a
  stable interface. A goose bump has to re-verify them; the kill switch
  and the CI check are the mitigation.

## How goose loads skills

Verified against block/goose v1.45.0, the version pinned in
`images/agent-base/Containerfile`.

- Skills are a platform extension named `skills`, registered in
  `crates/goose/src/agents/platform_extensions/mod.rs` with
  `default_enabled: true` and `unprefixed_tools: true`. It is a separate
  registry entry from `developer`, the builtin the harness passes to
  `goose serve`, so the two do not interact.
- The extension's `get_instructions()` appends one
  `• <name> - <description>` line per discovered skill to the session's
  system instructions, and exposes exactly one tool:
  `load_skill(name, args)`. That is progressive disclosure — descriptions
  at session start, the full `SKILL.md` on demand, supporting files via
  `load_skill(name: "skill-name/relative/path")`.
- `load_skill` returns the body prefixed by the skill's description, then
  the skill's directory, then every supporting file as an absolute path,
  then an explicit note that relative paths resolve from the skill
  directory and the shell tool does not.
- Discovery (`all_skill_dirs`, `crates/goose/src/skills/mod.rs`) scans, in
  order: `<session cwd>/.agents/skills`, `<cwd>/.goose/skills`,
  `<cwd>/.claude/skills`, then the home-rooted `~/.agents/skills`,
  `<goose config dir>/skills`, `~/.claude/skills`,
  `~/.config/agents/skills`, then installed plugin dirs. Each root is
  walked recursively and the first `SKILL.md` for a given frontmatter
  `name` wins, so project roots shadow home roots. `/opt/skills` is in
  none of them. No environment variable points goose at an arbitrary
  skills path; `GOOSE_PATH_ROOT` relocates the config root, and with it
  `<goose config dir>/skills`, but it drags data, state and plugin dirs
  along too, so it is not a way to name `/opt/skills`.
- `working_dir` for that scan is the ACP session's `cwd`, which the
  harness sets to the clone dir. It is captured once at session creation,
  so the agent changing directory mid-run does not move it.
- Nothing in goose forces a skill into context. `load_skill` is a tool
  call the model chooses to make.

## The probe

Measured in `quay.io/konveyor/agent-base:latest` (goose 1.45.0), running
as uid 1001 gid 0 with a read-only mount at `/opt/skills` standing in for
the ImageVolume, and the working directory at `/workspace/repo`, which
has no `.agents` of its own:

```text
$ goose skills list                                   # before the link
goose-doc-guide | builtin://skills/goose-doc-guide

$ mkdir -p /home/harness/.agents
$ ln -s /opt/skills /home/harness/.agents/skills
$ goose skills list                                   # after
goose-doc-guide | builtin://skills/goose-doc-guide
probe-skill     | /home/harness/.agents/skills/probe-skill
```

`~/.agents` does not exist in the image — `agent-base` creates only
`/home/harness/.migration-harness` — so the harness has to create it. It
can: `/home/harness` is group-0 writable, and the read-only mount is only
the link target.

Not verified. The probe drives `goose skills list`, while the harness runs
`goose serve --with-builtin developer` and talks ACP; the extension is
registered `default_enabled: true` independently of `--with-builtin`, so
the CLI and the session should enable the same set, but that is read from
the registry rather than observed. Also unobserved: that `load_skill`
resolves `references/` paths in a live session, which is read from
`loaded_skill_context`; and whether anything in a run calls goose's
skill-authoring tools, which would now be writing at a read-only link.

## Alternatives considered

### Keep harness assembly (status quo)

Rejected. It cannot deliver progressive disclosure, leaves supporting
files unreachable, makes `type` unimplementable, and puts the entire
skill corpus in context on every run. Its one real advantage — the prompt
is fully known before the run starts — is smaller than it looks:
`load_skill` calls are tool calls, so what the model actually read is
visible in the ACP stream the tee already publishes (ADR 0008), as it
happens rather than up front.

### Native loading for everything, no injection at all

Rejected. An always-loaded rule would become a suggestion. "Never leave
`javax.*` imports" that the model may or may not read is not a
constraint.

### A separate `/opt/rules` mount for rule content

An earlier draft had the controller mount rule-typed SkillCards at
`/opt/rules/<name>`, so the mount path itself carried the CRD field and
the harness needed no extra configuration to tell the two apart.

Rejected. Nothing links `/opt/rules` into goose's discovery roots, so a
rule-typed skill would lose `load_skill` and its supporting files would
become unreachable — the defect this ADR exists to fix, reintroduced for
exactly the skills most likely to need a guarantee, since `skills/plan`
ships `references/` and `javaee-to-quarkus` ships twelve files. It also
splits ADR 0001's single mount root, and it makes the rollback switch
non-self-contained: a harness env var cannot put the mounts back.

### Deliver rules through goose's hints mechanism

goose loads `.goosehints` and `AGENTS.md` from its config dir,
`~/.agents/`, the git root, and the cwd, with `@`-style file imports
(`crates/goose/src/hints/load_hints.rs`). A global hints file is genuinely
always-loaded and, being part of the system instructions, would survive
context compaction better than a first-turn prompt does.

Rejected for now. It moves rule content into a goose-specific config
channel, gives up the harness's ordering guarantee that its environment
rules come first and skill content cannot be read as overriding them, and
still leaves us needing the injection path for any non-goose runtime.
Worth revisiting if rules are observed getting lost to compaction on long
runs.

Worth noting separately: the same mechanism means a cloned repository's
own `AGENTS.md` or `.goosehints` is loaded into every run today. That is
read from `load_hints.rs` rather than observed. It is out of scope here,
but it is unreviewed content reaching the prompt and should be tracked.

### Link the skills root inside the clone

`<clone>/.agents/skills → /opt/skills` works, and it is what the spike
tested and what PR #136 implemented first, before moving the link to the
home root.

Rejected on blast radius, not on mechanism. Putting the link in the tree
whose commits the harness pushes means skill delivery has to be defended
in three other places: an `.agents/` entry written into the target
repository's `.gitignore`, an exclusion in the filesystem watcher, and a
guard against the repository already owning that path. Two of those
defences are softer than they look — the `.gitignore` write is non-fatal
on failure, after which a broad `git add` can stage the link, and an
existing `.agents/skills` directory in the target repository fails the
run at setup.

The home-rooted link needs none of the three and produces the same
listing, measured above. Its cost is the reverse of the last point: a
repository that ships `.agents/skills` silently shadows a platform skill
of the same name instead of failing loudly.

### Have the controller mount skills at the goose path

Mounting each ImageVolume straight at `~/.agents/skills/<name>` removes
the link. Rejected: it bakes a goose-specific convention into the
controller, which is runtime-agnostic by ADR 0001.

### Tag skills as stage/domain (issue #78)

Rejected as unnecessary here. It asks the harness to understand skill
roles in order to decide what to inject, which is exactly the knowledge
ADR 0007 took out of it. `type` already answers the only question the
harness needs to ask.

## Relationship to other ADRs

ADR 0001 decides the mount contract and this ADR keeps it: every skill,
rules included, lives at `/opt/skills/<name>/SKILL.md`, one directory, no
fan-out. One sentence of it is not true of goose, at line 106: "The agent
runtime points at `/opt/skills/` and discovers all skills regardless of
source." No runtime points there by default, which is the whole reason
the harness has to link the mount into a root the runtime does scan.

**This ADR supersedes that sentence.** It is a factual correction, not a
reversal: ADR 0001 is immutable and its text stays as written, but a
reader arriving at line 106 should follow the Status line here rather
than build against a discovery behaviour no runtime has. Nothing else in
ADR 0001 is affected — the mount path it decides is the one this ADR
keeps, and `README.md:23` documents that path rather than the discovery
claim.

ADR 0010 (Skill Content Boundary, proposed in #108) rules that a skill
must not perform "filesystem discovery that depends on container layout
conventions", citing the `ls /opt/skills/*/references/*.md` pattern, and
gives as the remedy that "the harness discovers and injects skill content
at startup". This ADR agrees with the rule and replaces the remedy.
Injection is *why* those globs exist: the harness injects `SKILL.md` and
nothing else, so a skill that ships `references/` has no supported way to
reach them and reaches for `ls`. Native loading removes the motive, so
skills can use plain relative references and stay ignorant of container
layout, which is what 0010 is asking for. #108 has since updated 0010's
"immediate changes" list to drop the globs in favour of relative paths
and to reference this ADR, so the two no longer disagree.
