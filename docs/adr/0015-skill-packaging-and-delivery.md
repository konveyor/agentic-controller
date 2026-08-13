# ADR 0015: Skill Packaging and Delivery

**Status:** proposed. Supersedes two parts of ADR 0001, its *Skill and rule
packaging via skillimage* section and its rejection of runtime git sync, and
revises where ADR 0014 sources its rules list. ADR 0001's *Skill mounting:
one directory* stands and is the contract everything here preserves. One
question is left open: whether a SkillCard may demote a skill that declares
itself a rule.
**Date:** 2026-08-13
**Authors:** Fabian von Feilitzsch

> Numbering note: 0009 through 0013 merged while this was being written
> (#106, #108), 0014 is claimed by the open #135, and a second 0007 exists
> on the unmerged `skill-exec-probe` branch. 0015 is the next free number.

> Implementation note: this ADR lands on its own. A working prototype exists
> and produced the measurements quoted here, but it is held back pending
> review. File paths below describe the shape the implementation takes, not
> files already in the tree.

## Context

ADR 0001 decided skills are packaged as OCI artifacts via redhat-et/skillimage
(`skillctl build`, `skillctl push`), and that they live at
`/opt/skills/{name}/SKILL.md`. The second half held up. The first has not
earned its place.

Measured against the skillimage source at v0.7.2 rather than its docs:

- Every `SKILL.md` we ship already carries AgentSkills.io frontmatter. The
  only skillimage-specific file was `skill.yaml`, duplicating the `name` and
  `description` already in that frontmatter.
- `skillctl build` on its default profile emits a plain OCI image,
  `vnd.oci.image.layer.v1.tar+gzip` plus `vnd.oci.image.config.v1+json`
  (`pkg/oci/mediatype.go:41-46`): one layer holding a tar of the skill
  directory at its root. `FROM scratch` with one `COPY` produces the same.
- `skillctl install --target goose` is not supported.
  `internal/cli/install.go:16-22` lists claude, cursor, windsurf, opencode
  and openclaw, so the local dev story ADR 0001 advertised was never
  reachable.

So the tool built an ordinary container image from a directory and charged us
a second metadata file and a pinned binary for it.

Two requirements it does not address:

1. `SkillCardSpec` has offered `image`, `source` and `inline` since the CRD
   was scaffolded, but `skillcard_controller.go:109-131` stubs the latter two
   as deferred pending an in-cluster registry, and `spec.source` promises "the
   controller clones, builds, and pushes the OCI artifact". That puts a build
   tool and push credentials in a reconciler AGENTS.md calls stateless.
2. Nothing validates a skill. ADR 0014 makes frontmatter load-bearing, since
   a `SKILL.md` without `name` and `description` is invisible to the runtime,
   and records that "nothing validates this", with an inline SkillCard able to
   "resolve, mount and report Ready while contributing nothing".

## Decision

### 1. A skill is an AgentSkills.io directory; frontmatter is the metadata

`skill.yaml` is deleted and skillctl dropped. `SKILL.md`'s YAML frontmatter is
the only skill metadata, which is what every shipped skill already had.

### 2. A skill image is an ordinary OCI image built from a Containerfile

`FROM scratch`, one `COPY` per skill directory. Any builder works, including
Konflux. Signing, provenance and mirroring come from the same image pipeline
as everything else we ship.

### 3. One image may carry one skill or many, and the shape is detected

`SKILL.md` at the image root means a single skill. Otherwise every immediate
subdirectory holding a `SKILL.md` is a skill. No new CRD field, no manifest
inside the image, no format to version: the layout already says which it is.

This repo's skills ship as one bundle image, so `skills/` has one
Containerfile and one CI job. A third party shipping a single-skill image
works identically.

A skill is exactly one directory deep, so a directory of directories is not a
bundle and its contents are not skills. `COPY plan/ /` also copies plan's
*contents* to the root, so each destination is named explicitly or every skill
collapses into one.

### 4. Nothing is built in-cluster at reconcile time

| `spec` | delivery | who builds |
| --- | --- | --- |
| `image` | ImageVolume, staged read-only | the author, ahead of time |
| `inline` | ConfigMap the AgentRun controller creates | nobody, the bytes are in etcd |
| `source` | the loader clones at pod start | nobody |

The controller gains no builder, registry, push credentials or network egress.
`status.resolvedImage` is meaningful only for `image`.

### 4a. Git resolves at pod start, superseding ADR 0001's rejection

ADR 0001 rejected this by name:

> **pallet as runtime sync engine.** The POC runs `pallet sync .` at container
> startup to fetch skills from git repos. This adds network dependency and
> startup latency. OCI artifacts via skillimage are pre-mounted by kubelet
> before the container starts — faster, deterministic, and auditable via image
> digests.

Its air-gap section adds: "Everything must be in the image at build time.
Agent containers cannot download tools or dependencies at runtime."

Both objections are correct and are accepted rather than answered. A
git-sourced skill takes a network dependency at pod start, adds clone time to
startup, is not auditable by digest, and does not work air-gapped.

What changed is the scope, not the truth. `pallet sync` was the mechanism for
every skill on every startup, so everyone paid. Here git is one opt-in source
of three, and the two carrying curated skills are still pre-mounted with no
network. A cluster that cannot reach a git host uses those two, and the remedy
for any git skill is to build the repo into an image.

This is not a one-way door. `spec.source`, `spec.ref` and `spec.subPath`
describe *what* skill is wanted, not when it is fetched, so resolving them
controller-side later (clone once at reconcile, write a ConfigMap, mount with
no network) is an implementation change behind an unchanged API. One caveat,
since "no disruption" is easy to overclaim: for a card with no `ref`, that
move changes *when* the default branch is read, from every run to once per
resolve. Pinned cards are unaffected.

### 5. An always-on `skill-loader` init container assembles and validates

Sources stage read-only under `/opt/skills-src/{sourceName}`. The loader
assembles them into `/opt/skills`, validates the result, and exits non-zero
with the reason if anything is unusable. It runs on every pod, including one
with no skills, so there is a single pod shape and one component owning both
assembly and validation. It is a subcommand of the harness binary already in
`agent-base`, running the agent's own image, so it adds no image to build,
version, sign or mirror.

```yaml
volumes:
  skills            emptyDir {}                          -> /opt/skills                 (agent, ro)
  skill-konveyor    image: quay.io/konveyor/skills:latest -> /opt/skills-src/konveyor    (ro)
  skill-house-rules configMap: <run>-skill-house-rules    -> /opt/skills-src/house-rules (ro)

initContainers:
  - name: skill-loader
    image: <the agent's own image>
    args: ["skills", "load"]
    env:
      - name: KONVEYOR_SKILL_SOURCES
        value: >
          [{"name":"konveyor"},
           {"name":"house-rules"},
           {"name":"vendor","type":"rule",
            "git":{"url":"https://example.com/vendor.git","ref":"v2.1.0","subPath":"skills"}}]
```

Staged sources are visible only to the loader; the agent sees the assembled
root and nothing else. The inline ConfigMap is named for the run as well as
the SkillCard and owned by the run, because two runs sharing one would let the
second rewrite the owner reference and its deletion collect the ConfigMap out
from under the first.

Bundles are flattened during assembly. goose does not need this, since its
discovery walks each root recursively (ADR 0014's appendix); it keeps
`/opt/skills/{name}/SKILL.md` literally true rather than quietly widening to
`/opt/skills/**/SKILL.md` for a less forgiving runtime.

The destination stays `/opt/skills`, which no runtime reads by default. ADR
0014 does not move the mount to fix that: the harness links `~/.agents/skills`
at it, and 0014 rejects mounting ImageVolumes at the goose path because it
"bakes a goose-specific convention into the controller". `/opt/skills` is the
filesystem contract; teaching a runtime where to find it is harness work, so a
second runtime is a harness change.

The loader cannot own that link. An init container and the agent container
share only the volumes both are given, and `$HOME` is in the image:

```
init:   ln -sfn /opt/skills ~/.agents/skills   ->  skills -> /opt/skills
agent:  ls ~/.agents/                          ->  No such file or directory
```

Making it survive needs a shared volume at a goose- and image-specific path,
handing the controller the runtime knowledge 0014 refused it.

### 6. Validation lives where the bytes are

- **Loader**, at pod init: every skill from every source. The only place an
  image's or repository's frontmatter exists.
- **Controller**, at reconcile: `spec.inline` only, since that needs no
  network. An inline card with no frontmatter reports `Ready=False` with
  `InvalidSkillContent` instead of resolving clean.
- **CI**, at review time, over this repo's own skills.

ADR 0014 says validation "belongs in the SkillCard controller". For inline
that is right and is what happens. For image and git it needs a registry
client, pull secrets and egress in the reconciler, and even then proves only
that the ref resolves, not that the skill is usable.

### 7. A skill's directory is its frontmatter name; collisions are errors

ADR 0014 records "two namespaces for one name": the controller dedupes by
SkillCard name, goose by frontmatter `name`, and two cards declaring the same
frontmatter name "collapse to one entry, chosen by walk order, with no error
from either side".

The loader collapses the two instead. A skill is assembled under the name its
frontmatter declares, not the directory it arrived in, and the loader logs
when those differ. A genuine duplicate then fails the pod, naming both
origins. This replaces `resolveSkillVolumes`'s `seen[name]` first-wins dedup,
which silently dropped the loser.

### 8. A skill declares its own `type`; a SkillCard may override it

This revises ADR 0014, where `KONVEYOR_RULES` is "a comma-separated list of
SkillCard names" set by the controller from `spec.type`. That cannot work for
a bundle: one card yields several skills named by directories inside the
image, which the controller never sees.

So `type` becomes a frontmatter key and the author's answer is the default.
Frontmatter is extensible; `skills/javaee-to-quarkus/SKILL.md` already carries
non-standard `license:` and `metadata:` keys.

`SkillCardSpec.Type` keeps a job rather than going inert: a load policy the
operator imposes on every skill that source contributes. That is the tool for
promoting somebody else's bundle without editing skills you do not own, and it
works because it never has to name the skills it applies to.

For that to mean anything, `+kubebuilder:default=skill` is removed, so "the
operator chose on-demand" is distinguishable from "the operator said nothing".
A defaulted field would silently demote every bundle whose frontmatter
declares a rule, which is the degradation ADR 0014 warns about. The loader
logs any override that contradicts a skill's own declaration. As implemented
the override runs both ways; whether it should is left open below.

This also removes a hazard 0014 raises against itself, that a harness shipping
before the controller reads an unset variable and "every rule silently stops
reaching the prompt". Loader and harness are one binary in one image, so no
release ordering remains to get wrong.

### 8a. The controller declares the source list

`KONVEYOR_SKILL_SOURCES` carries every source as JSON: name, load-policy
override, and for git the URL, ref and subPath. It is the whole channel from
the CRs to the pod; there is no second env var or annotation. The loader works
from the declaration rather than from what it finds staged, so an unexpected
directory cannot become skills and a promised-but-undelivered source is an
error. With no declaration it falls back to scanning, which keeps the command
usable by hand.

### 9. The loader records what it assembled

`/opt/skills/.konveyor-skills.json` lists every skill with its name,
description, type and originating source, plus the rules list. A dotfile,
since runtimes discover by looking for `SKILL.md`. It gives the harness the
rules list without re-walking, and gives 0014's repo-shadowing check a record
to compare against rather than a second walk.

## The probe

Measured on minikube, CRI-O 1.35.0, Kubernetes v1.34.0, ImageVolume gate
enabled: the rig the exec probe used. The prototype ships a script that
re-answers all four questions on any cluster.

An image bundle of four skills, one inline ConfigMap and one git clone with a
`subPath`, together. The git source carries `type: rule` and `grill-me`
declares no type, so this is the source-level override landing on a skill the
controller never saw:

```
[ok] grill-me (rule) from acme
[ok] house-rules (rule) from house-rules
[ok] execute (skill) from konveyor
[ok] javaee-to-quarkus (skill) from konveyor
[ok] plan (skill) from konveyor
[ok] verify (skill) from konveyor
always-loaded rules: [grill-me house-rules]
```

The agent container then saw all six at `/opt/skills/{name}/SKILL.md`, exactly
one level deep, with `javaee-to-quarkus/references/` intact.

Two failure paths, both ending in pod phase `Failed` with the agent container
never started:

```
skill assembly failed: 1 unusable skill(s):
  broken: no YAML frontmatter: file does not start with ---

skill assembly failed: 1 unusable skill(s):
  duplicate skill name "plan": acme and konveyor/plan
```

Fourth: an init container's `$HOME` does not reach the agent container, quoted
under decision 5.

Not measured: a full AgentRun through the controller against a live model. The
probe drives the pod shape the controller generates and envtest covers the
controller, but the two have not been run end to end together.

## Consequences

- **Bundle skill names come from inside the image.** A SkillCard's
  `metadata.name` labels the staging directory and appears in diagnostics; it
  no longer decides where skills land.
- **Bundle versioning is coarse.** Touching one skill reships the image. A
  skill with its own cadence should be its own image, which is supported.
- **`SkillCollection` gains a physical form**: one image, one pull, one
  signature, one mirror entry.
- **Inline skills cannot ship supporting files.** A ConfigMap key cannot hold
  a path separator, so inline is a single `SKILL.md`. Anything needing
  `references/` must be an image or a git source.
- **`spec.type` changes meaning and loses its default.** A stored card
  defaulted to `skill` now pins every skill from that source to on-demand
  until cleared. Visible in the object rather than silent, but it needs a
  release note.
- **Git sources are unauthenticated and not air-gap capable.** `spec.ref` and
  `spec.subPath` allow pinning and location, but there is no credential path,
  so private repos are unsupported. The air-gap and latency cost is the price
  of 4a, bounded to `spec.source` and to the runs that use one. A git host
  outage becomes a pod start failure for those runs.
- **Frontmatter is parsed in two places.** The loader is in the harness module
  and the controller cannot import it. They can drift; the controller's copy
  is deliberately the looser, since the loader has the last word. Worth
  collapsing into a shared package if a third caller appears.
- **One inline ConfigMap per run per SkillCard**, so runs do not share them
  and one cannot be reused across a workflow's stages.
- **The assembled root is read-only to the agent.** It is an emptyDir and
  could have been writable; `readOnly` keeps skills a read surface per the
  exec-probe ADR, and settles one of 0014's unverified items strictly: goose's
  skill-authoring tools would fail rather than mutate a run's skills. Revisit
  if a run ever needs to author one.
- **Disconnected mirroring is unresolved.** skillctl's `redhat` media-type
  profile exists so `oc-mirror` can identify skill artifacts, and ADR 0001
  commits to mirroring. A plain image probably mirrors like any other image,
  but that has not been checked with `oc-mirror`.

## Open question: may a SkillCard demote a rule?

| frontmatter | `spec.type` | result today |
| --- | --- | --- |
| `skill` | unset | on-demand |
| `rule` | unset | always-loaded |
| `skill` | `rule` | always-loaded, promoted |
| `rule` | `skill` | on-demand, **demoted**, logged |

Promotion is uncontroversial: always-loading a skill that asked to be
on-demand costs context budget and nothing else.

Against demotion: ADR 0014 rejected native loading for rules because "an
always-loaded rule would become a suggestion". Demotion reaches that by
another route, since an operator can turn a constraint the author marked
mandatory into an optional one, with only an init log line as the trace.

For it: the operator owns their context budget and is accountable for the run.
A rule wrong for their domain otherwise forces a fork of content they did not
write.

The narrower option is promote-only, rejecting a card that asserts `skill`
against frontmatter asserting `rule`. This ADR ships the permissive behaviour
because it is the reversible one: narrowing later breaks only configurations
relying on demotion, whereas widening later changes the meaning of a field
people have started trusting. It should be settled before the field is
documented for users.

## Alternatives considered

**Keep skillctl.** The artifact it produces is what we want, but it is all we
use: the metadata file duplicates frontmatter, the lifecycle tags and local
store go unused, and `install --target goose` does not exist. A Containerfile
gets the same image with one fewer tool and onboards downstream.

**Require every skill to be an OCI image, building the ones that are not.**
The stub's intent, and the strongest case for it is uniformity: every skill
immutable, digest-addressable, signable and mirrorable, with one delivery path
in the pod. Rejected on cost and on how early it is to pay it. It needs a
registry, push credentials and a build tool, and makes delivery a
reconcile-time operation that can fail. Most skills are a markdown file. The
argument is deferred rather than disposed of, and may read differently once
there is a local-developer story; nothing here blocks adding it, since an
image source is already the primary path and a build step for the other two is
additive.

**Resolve git controller-side into a ConfigMap.** Clone once at reconcile,
mount like an inline skill. Removes the runtime network dependency and the
startup latency and works air-gapped after first resolve. Deferred, not
rejected: it needs egress, a git client and private-repo credentials in the
reconciler, and a ConfigMap key cannot hold a path separator, so a multi-file
skill must be tar-packed into `binaryData` under 1 MiB. Adoptable later
without an API change, per 4a.

**Run the loader only when a source needs materializing.** Saves a container
start on all-image runs, but leaves two pod shapes and no single validation
point, so the most common runs would be the ones with nothing checking them.

**Mount each ImageVolume at its final path, as today.** Simplest, and what ADR
0001 describes. Rejected because it forces one image to mean one skill: with
the path fixed by the controller, a bundle lands a level too deep and
frontmatter cannot decide a skill's directory.

**Declare the bundle shape in the CRD.** A field that can disagree with the
image it describes, for a question the layout already answers.

**Deprecate `spec.type`.** The obvious tidy-up once frontmatter carries the
policy, but the field does a job frontmatter cannot: changing how somebody
else's bundle loads without forking it.

## Documentation this invalidates

These state that a skill is always an OCI artifact. They change when the
implementation lands, not when this ADR does, since until then they describe
the system as it is:

- `CONTEXT.md:111-115`, defining skillimage and `skillctl` as the packaging
  mechanism.
- `CONTEXT.md:198-200`, "resolves to an OCI artifact from one of three
  sources". Only `image` still does.
- `CONTEXT.md:6,19`, describing the `skillimage.io/v1alpha1` formats. The CRDs
  keep those names deliberately; the skill files are AgentSkills.io.
- `README.md:23-24,36` repeat the OCI framing and `README.md:71` lists
  redhat-et/skillimage as a dependency.

## Relationship to other ADRs

**ADR 0001** decided the packaging tool, the mount contract, and that skills
are never fetched at runtime. This supersedes the first and third, keeps the
second. Its air-gap requirement is scoped rather than overturned: it holds for
image and inline, and 4a records what git gives up. 0001 is immutable, so a
reader reaching those sections should follow the Status line here.

**ADR 0014** decides how skills are *loaded*: the harness links
`~/.agents/skills` at the mount for native goose discovery, merged in #136
(`harness/cmd/migration-harness/main.go:164`), and rules are injected into the
prompt. This decides how they are packaged and delivered to that mount. Both
preserve `/opt/skills/{name}/SKILL.md`. It revises one 0014 decision, the
source of the rules list (§8), and closes four gaps 0014 defers: frontmatter
validation, the two-namespace collision, inline cards that resolve empty, and
a manifest for the repo-shadowing check. **#135 needs updating alongside
this.**

**ADR 0007 on `skill-exec-probe`** measured that `noexec` does not reach the
container under CRI-O. Nothing here depends on it either way, since skills are
a read surface and agents stage scripts to `/tmp`. Cited so this does not
reopen a settled question.

**ADR 0010** (skill content boundary) forbids skills doing filesystem
discovery that depends on container layout. Unaffected; the assembled layout
is the one 0010 assumes.
