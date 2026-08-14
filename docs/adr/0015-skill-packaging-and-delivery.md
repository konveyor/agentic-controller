# ADR 0015: Skill Packaging and Delivery

**Status:** proposed. Supersedes two parts of ADR 0001, its *Skill and rule
packaging via skillimage* section and its rejection of runtime git sync, and
revises where ADR 0014 sources its rules list. ADR 0001's *Skill mounting:
one directory* stands and is the contract everything here preserves. Two
questions about SkillCollection are left open at the end.
**Date:** 2026-08-13
**Authors:** Fabian von Feilitzsch

> Numbering note: 0009 through 0013 merged while this was being written
> (#106, #108), 0014 is claimed by the open #138, and a second 0007 exists
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

### 3. An image may hold several skills; a SkillCard still means one

An image is free to contain several skill directories. That is a fact about
an artifact, not a concept the domain needs a name for, and this ADR
deliberately does not introduce one. `SKILL.md` at the image root means the
image is one skill; otherwise every immediate subdirectory holding a
`SKILL.md` is one. The layout says which, so there is no new CRD field, no
manifest inside the image and no format to version.

A **SkillCard is always one skill.** Against an image holding several, it
selects with `subPath`, the same field a git source already uses:

```yaml
spec: {image: quay.io/konveyor/skills:latest, subPath: plan, type: rule}
spec: {image: quay.io/konveyor/skills:latest, subPath: javaee-to-quarkus}
```

A card that resolves to more than one skill without a `subPath` is an error
naming what it found, rather than quietly becoming several skills.

This is what lets us publish one image for all of our skills while still
referencing them individually, and it is why `type` can stay a per-skill
field on the CRD (decision 8) instead of moving into skill content.

**Packaging and grouping are independent axes**, which is why an image
holding several skills is not a SkillCollection by another name:

- **Packaging** is how many skills ride in one artifact. It governs what
  gets built, signed, mirrored, pulled and versioned, and is owned by
  whoever builds the image.
- **Grouping** is which skills an Agent gets from one reference. It is
  cluster configuration, owned by whoever runs the cluster. This is what
  `SkillCollection` already means in CONTEXT.md.

They vary independently both ways: a collection can list skills from three
vendors' images, and an Agent can want one skill out of a four-skill image.
Merging them would force lockstep versioning on skills that do not need it,
let a vendor's packaging decide an operator's grouping, make "two of these
four" impossible without forking the image, and force one load policy across
a whole source.

Selecting a single layer would be another way in, but is not available:
`ImageVolumeSource` exposes only `reference` and `pullPolicy`, and mounts the
flattened filesystem.

Mechanical note: `COPY plan/ /` copies plan's *contents* to the root, so each
destination is named explicitly or every skill collapses into one.

### 4. Nothing is built in-cluster at reconcile time

| `spec` | selects with | delivery | who builds |
| --- | --- | --- | --- |
| `image` | `subPath` | ImageVolume, staged read-only | the author, ahead of time |
| `inline` | n/a, one skill | ConfigMap the AgentRun controller creates | nobody, the bytes are in etcd |
| `source` | `ref`, `subPath` | the loader clones at pod start | nobody |

The controller gains no builder, registry, push credentials or network egress.

`status.resolvedImage` is meaningful only for `image`, so a reader cannot tell
"not resolved yet" from "resolved, just not to an image". `status.deliveryMode`
carries `image`, `inline` or `source` so the resolved state is observable
without inferring it from an empty field.

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

Two cards selecting different skills out of one image share its pull, and
stage under their own names:

```yaml
volumes:
  skills            emptyDir {}                          -> /opt/skills                 (agent, ro)
  skill-plan        image: quay.io/konveyor/skills:latest -> /opt/skills-src/plan        (ro)
  skill-javaee      image: quay.io/konveyor/skills:latest -> /opt/skills-src/javaee      (ro)
  skill-house-rules configMap: <run>-skill-house-rules    -> /opt/skills-src/house-rules (ro)

initContainers:
  - name: skill-loader
    image: <the agent's own image>
    args: ["skills", "load"]
    env:
      - name: KONVEYOR_SKILL_SOURCES
        value: >
          [{"name":"plan","subPath":"plan","type":"rule"},
           {"name":"javaee","subPath":"javaee-to-quarkus"},
           {"name":"house-rules"},
           {"name":"vendor","type":"rule",
            "git":{"url":"https://example.com/vendor.git","ref":"v2.1.0","subPath":"skills/x"}}]
```

That env var is the whole channel from the CRs to the pod: name, `type`, the
`subPath` selecting one skill, and for git the URL and ref. No skill
configuration reaches the pod any other way. The loader works from the
declaration rather than from what it finds staged, so an unexpected directory
cannot become skills and a promised-but-undelivered source is an error. With
no declaration it falls back to scanning, which keeps the command usable by
hand.

Staged sources are visible only to the loader; the agent sees the assembled
root and nothing else. A skill selected from inside an image is flattened, so
`/opt/skills/{name}/SKILL.md` stays literally true rather than widening to
`/opt/skills/**/SKILL.md` for a runtime less forgiving than goose, whose
discovery walks each root recursively. The inline ConfigMap is named for the
run as well as the SkillCard and owned by the run, because two runs sharing
one would let the second rewrite the owner reference and its deletion collect
the ConfigMap out from under the first.

The loader also writes `/opt/skills/.konveyor-skills.json`, recording each
skill's name, description, type and source plus the rules list. A dotfile,
since runtimes discover by looking for `SKILL.md`. It saves the harness a walk
and gives ADR 0014's repo-shadowing check a record to compare against.

No runtime reads `/opt/skills` by default, and ADR 0014 does not move the
mount to fix that: the harness links `~/.agents/skills` at it, and 0014
rejects mounting ImageVolumes at the goose path because that "bakes a
goose-specific convention into the controller". The loader cannot own that
link either, since an init container and the agent container share only the
volumes both are given and `$HOME` is in the image (measured: the link exists
in init, and the agent gets "No such file or directory"). So teaching a
runtime where to find the mount stays harness work, and a second runtime is a
harness change.

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

**The rules are the Agent Skills spec's, not ours.** `name` is 1-64
characters, lowercase alphanumerics and single hyphens, no leading or
trailing hyphen, and must match its directory name; `description` is 1-1024
and non-empty. The prototype's validator is looser than that, allowing
uppercase, `_` and `.`, and will be tightened to match. The directory rule is
satisfied by construction, since decision 7 assembles each skill under its
own frontmatter name.

**One implementation, four callers.** The controller, the loader, CI and an
author checking a skill before publishing all apply the same rules, so they
belong in a shared package rather than the two copies the prototype has. The
plumbing is real: `harness/` is a separate Go module, so the package needs a
home both modules can depend on. That is worth doing rather than deferring,
because "the looser of the two" drifting is the validation gap ADR 0014
flags as load-bearing.

The fourth caller is worth naming as intended scope: now that anyone can
build a skill image with a plain Containerfile, the low barrier to building
one raises the value of an easy way to check it. A standalone
`konveyor-skills validate <dir>`, installable with `go install`, gives an
external author the same answer the loader would give them at pod init,
before they publish rather than after.

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

### 8. `type` stays on the CRD; skill content never declares it

An earlier draft moved `type` into `SKILL.md` frontmatter, because a card
covering a whole multi-skill image could not say which of its skills was a
rule. Decision 3 removes that pressure: a SkillCard selects one skill with
`subPath`, so `spec.type` is per-skill again and needs no new home.

Putting it in frontmatter would also have broken the standard this ADR
adopts. The Agent Skills spec defines a closed set of top-level frontmatter
fields, and its reference validator treats anything else as an error:

```python
# skills-ref/src/skills_ref/validator.py
extra_fields = set(metadata.keys()) - ALLOWED_FIELDS
if extra_fields:
    errors.append(f"Unexpected fields in frontmatter: ...")
```

`ALLOWED_FIELDS` is `name`, `description`, `license`, `compatibility`,
`allowed-tools`, `metadata`. A top-level `type:` therefore fails
`skills-ref validate`, so decision 1 and decision 6 would have contradicted
each other: we would validate skills against a spec our own skills violate.
The only compliant home is a key under `metadata`, which every other client
ignores. Correcting an error in the earlier draft: it cited `license` and
`metadata` as precedent for non-standard keys, but both are standard fields,
so there was no precedent.

Keeping `type` on the CRD also keeps it validated by the API server,
observable in `kubectl get skillcards`, and settable without editing content
somebody else owns. `+kubebuilder:default=skill` stays, since a card is one
skill and defaulting says nothing surprising.

This still revises ADR 0014's `KONVEYOR_RULES`, which is "a comma-separated
list of SkillCard names, matching the mount directory under `/opt/skills`".
The mount directory is the skill's frontmatter name (decision 7), and for a
single-skill image nothing outside the image knows that name, so the
controller cannot compose the list. It forwards each source's `type` and the
loader resolves names to it, which also removes a hazard 0014 raises against
itself: a harness shipping before the controller would read an unset variable
and "every rule silently stops reaching the prompt". Loader and harness are
one binary in one image, so no release ordering remains to get wrong.

## The probe

Measured on minikube, CRI-O 1.35.0, Kubernetes v1.34.0, ImageVolume gate
enabled: the rig the exec probe used. The prototype ships a script that
re-answers all four questions on any cluster.

A four-skill image, one inline ConfigMap and one git clone with a `subPath`,
together. The git source's card carries `type: rule`, so its skill is
always-loaded while the image's are not:

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

Both failure paths end in pod phase `Failed` with the agent container never
started: a skill with no frontmatter, and a name collision, which reports
`duplicate skill name "plan": acme and konveyor/plan`. The fourth measurement,
that an init container's `$HOME` does not reach the agent container, is under
decision 5.

Not measured: a full AgentRun through the controller against a live model. The
probe drives the pod shape the controller generates and envtest covers the
controller, but the two have not been run end to end together.

## Consequences

- **A skill's directory comes from its own content.** A SkillCard's
  `metadata.name` labels the staging directory and appears in diagnostics; it
  does not decide where the skill lands.
- **Co-packaged skills version together.** Touching one reships the image. A
  skill with its own cadence should be its own image, which is supported.
- **A card must know the layout of the image it points into.** `subPath` is a
  string the CRD cannot check, so a typo surfaces at pod init rather than at
  apply time. Auto-enumeration, in the open questions, is what would remove
  the need to type it.
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

## Open questions

**Should SkillCollection be the type users write, with SkillCards
generated?** Today a user authors SkillCards and may group them. The inverse
may be better: point a collection at a source, and the controller creates one
SkillCard per skill in it, owned by the collection, so even a single-skill
source has one authoring path. A card then becomes mostly a generated record
of one skill, observable and individually referenceable, with hand-written
cards still valid for taking one skill out of a source without the rest.

That dissolves a smaller question rather than answering it: collection
entries today may carry an `image` or `source` inline instead of a
`skillCardRef`, so the fields defining a skill live in two places. It is
already the documented intent, and the code disagrees with the docs.
`CONTEXT.md:21` says the controller "creates SkillCard CRs for git-sourced
entries"; `skillcollection_controller.go:83` says an image ref in a
collection is self-contained and needs none.

**Enumerating a source needs no registry client in the controller.** The
objection is that reading an image means pull secrets and egress in the
reconciler, which decision 4 rules out. It does not survive contact: the
kubelet must pull the image to run the skill anyway, so the credentials, the
ImageVolume gate and the mount semantics have to work regardless. A
short-lived pod mounting the source exactly as the loader does can enumerate
it, asking nothing new of the controller process, and the loader already
knows how to walk a source, so it is another mode of the same binary.

Deferred. Most of what remains is ordinary: how the pod reports back, not
re-enumerating every reconcile, deterministic child names, owner references,
pruning. The one needing a real answer is where per-skill `type` lives, since
content does not declare it (decision 8) and an edit to a generated card
would not survive the next reconcile. Naming types on the collection is the
likely shape, with enumeration supplying the set and the collection the
policy.

## Alternatives considered

**Keep skillctl.** The artifact it produces is what we want, but it is all we
use: the metadata file duplicates frontmatter, the lifecycle tags and local
store go unused, and `install --target goose` does not exist. A Containerfile
gets the same image with one fewer tool and onboards downstream.

**Require every skill to be an OCI image, building the ones that are not.**
The stub's intent, and uniformity is the strongest case for it: every skill
immutable, digest-addressable, signable and mirrorable, one delivery path in
the pod. Rejected on cost and on how early it is to pay it, since it needs a
registry, push credentials and a build tool, and makes delivery a
reconcile-time operation that can fail, all to wrap a markdown file. Deferred
rather than disposed of, and nothing here blocks it: an image source is
already the primary path and a build step for the other two is additive.

**Resolve git controller-side into a ConfigMap.** Clone once at reconcile,
mount like an inline skill. Removes the runtime network dependency and works
air-gapped after first resolve. Deferred: it needs egress, a git client and
private-repo credentials in the reconciler, and a ConfigMap key cannot hold a
path separator, so a multi-file skill must be tar-packed into `binaryData`
under 1 MiB. Adoptable later without an API change, per 4a.

**Run the loader only when a source needs materializing.** Saves a container
start on all-image runs, but leaves two pod shapes and no single validation
point, so the most common runs would be the ones with nothing checking them.

**Mount each ImageVolume at its final path, as today.** Simplest, and what ADR
0001 describes. Rejected because it forces one image to mean one skill: with
the path fixed by the controller, a multi-skill image lands a level too deep
and frontmatter cannot decide a skill's directory.

**Declare in the CRD whether an image holds one skill or several.** A field
that can disagree with the image it describes, for a question the layout
already answers.

**Move `type` into `SKILL.md` frontmatter.** An earlier draft did this, so a
card covering a whole multi-skill image could still say which of its skills
were rules. Rejected twice over: `subPath` makes a card mean one skill again,
so the pressure is gone, and the Agent Skills reference validator errors on
any top-level field outside its allowed set, so our own skills would fail the
standard decision 1 adopts. See decision 8.

**Fold multi-skill images into SkillCollection.** Raised in review: a
collection already means "a group of skills", so an image holding several
could be a collection source with one load policy across it. Rejected as a
merge of two independent axes (decision 3), and because a uniform per-source
policy cannot express a mixed set. It is right that "bundle" should not be a
domain term, which this ADR now avoids, and that resolving one source into
many skills is a real job for a collection, which the open questions take up.

## Documentation this invalidates

These state that a skill is always an OCI artifact:

- `CONTEXT.md:111-115`, defining skillimage and `skillctl` as the packaging
  mechanism.
- `CONTEXT.md:198-200`, "resolves to an OCI artifact from one of three
  sources". Only `image` still does.
- `CONTEXT.md:6,19`, describing the `skillimage.io/v1alpha1` formats. The CRDs
  keep those names deliberately; the skill files are AgentSkills.io.
- `README.md:23-24,36` repeat the OCI framing and `README.md:71` lists
  redhat-et/skillimage as a dependency.

The CONTEXT.md entries are updated in this PR, since the glossary is meant to
be the canonical definition and leaving it contradicting an accepted decision
is worse than it briefly running ahead of the code. The README changes land
with the implementation, being descriptions of behaviour rather than
definitions of terms.

One divergence found while writing this is not caused by it and is worth
recording either way: `CONTEXT.md:21` says the controller "creates SkillCard
CRs for git-sourced entries", and no controller does. See the open questions.

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
a manifest for the repo-shadowing check. **#138 needs updating alongside
this.**

**ADR 0007 on `skill-exec-probe`** measured that `noexec` does not reach the
container under CRI-O. Nothing here depends on it either way, since skills are
a read surface and agents stage scripts to `/tmp`. Cited so this does not
reopen a settled question.

**ADR 0010** (skill content boundary) forbids skills doing filesystem
discovery that depends on container layout. Unaffected; the assembled layout
is the one 0010 assumes.
