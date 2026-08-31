---
adr: "0019"
title: "Skill Catalog Repository Layout"
description: "Defines the separation between shipped catalog skills, maintainer workflow skills, and single-skill packaging examples."
status: proposed
status_note: "Revises the repository layout adopted by ADR 0015 while preserving its skill packaging contract."
date: "2026-08-25"
last_updated: "2026-08-31"
authors:
  - "David Zager"
last_reviewed: "2026-08-31"
implementation_status: in-sync
review_note: "The catalog/ layout and maintainer skills/ split are implemented. The ADR remains proposed pending formal acceptance."
---

# ADR 0019: Skill Catalog Repository Layout

**Update (2026-08-31):** The `catalog/` and maintainer `skills/` split is
present in the repository, including the catalog build context and updated
validation/e2e paths. The ADR remains proposed pending formal acceptance.

> Numbering note: 0016 is the Gateway interim-interface ADR; 0017 is the
> ask-user ADR; 0018 is the execution-fields ADR. This catalog-layout ADR
> uses the next available number, 0019.

## Context

ADR 0015 established how skills are packaged and delivered, and the
implementation put every skill under a single top-level `skills/`
directory:

- **Shipped skills** — `plan`, `execute`, `verify`, `javaee-to-quarkus` —
  built into the bundle image `quay.io/konveyor/skills` and pulled by
  SkillCards at run time.
- **Maintainer workflow skills** — `grill-me`, `grill-with-docs`,
  `submit-pr` — used by agents working *on this repository*. These are
  exposed to the local agent tooling through the `.opencode/skills` and
  `.agents/skills` symlinks, both pointing at `../skills`.
- **Packaging examples** — `examples/ejb-to-cdi`, `examples/maven-migration`,
  `examples/no-javax-imports` — worked examples of the *single-skill* image
  shape (`SKILL.md` at the image root, one `Containerfile` each) plus loader
  and e2e fixtures. Deliberately not shipped.

Three problems follow from mixing these under one directory:

1. **The bundle needs a denylist-by-comment.** `skills/Containerfile` had to
   `COPY` each shipped skill by name, with a comment explaining that
   `grill-me`/`grill-with-docs` were "deliberately absent." Whether a skill
   ships was encoded in a hand-maintained list, not in where the skill
   lives. A new shipped skill that someone forgets to add to the list
   silently does not ship; a maintainer skill added to the wrong place would
   silently leak into the product image.

2. **The dev-tool symlink exposes product skills.** Because
   `.opencode/skills → ../skills`, the local agent sees `plan`, `execute`,
   `verify`, and `javaee-to-quarkus` as if they were maintainer tools,
   alongside `grill-me` and `submit-pr`. The two audiences — end users of
   the shipped bundle and agents working on this repo — share one namespace.

3. **"example" and "shipped" are indistinguishable by location.** The
   examples are real migration-skill content, sitting next to the skills we
   actually ship, separated only by an `examples/` subdirectory and a README
   sentence.

## Decision

Introduce a top-level `catalog/` directory that owns everything about the
skills this project *packages*, and reserve the top-level `skills/`
directory for the repository's own maintainer workflow skills.

```
catalog/
  Containerfile          # bundle image; build context is catalog/
  skills/                # the shipped bundle — one directory per skill
    plan/  execute/  verify/  javaee-to-quarkus/
  examples/              # worked examples of the single-skill shape (not shipped)
    ejb-to-cdi/{SKILL.md,Containerfile}  maven-migration/  no-javax-imports/
skills/                  # maintainer workflow skills — what .opencode/skills points at
  grill-me/  grill-with-docs/  submit-pr/
```

Consequences of the layout:

- **Membership in the bundle is by location, not by allowlist.** The
  Containerfile's build context is `catalog/`, and it copies the *contents*
  of `catalog/skills/` to the image root with a single `COPY skills/ /`.
  Everything under `catalog/skills/` ships; nothing else can. `catalog/`
  also contains `examples/`, but that is outside `catalog/skills/`, so the
  single `COPY` cannot reach it — examples still never ship, without a
  denylist.

- **The dev-tool symlinks resolve to maintainer skills only.**
  `.opencode/skills` and `.agents/skills` continue to point at `../skills`,
  which now holds only `grill-me`, `grill-with-docs`, and `submit-pr`. The
  local agent no longer sees product or example skills.

- **`make skill-validate` validates all three trees explicitly.**
  `SKILL_TREES = catalog/skills catalog/examples skills`, so a broken
  frontmatter in any of the shipped skills, the examples, or the maintainer
  skills fails the check.

- **e2e and integration scripts read the new paths.**
  `hack/harness-test/setup.sh` builds per-skill images from
  `catalog/skills/`, and `hack/setup-e2e.sh` builds the example single-skill
  images from `catalog/examples/`.

The packaging contract itself is untouched: skills are still AgentSkills.io
directories, the bundle is still `FROM scratch`, the loader still detects
bundle-vs-single by the presence of `SKILL.md` at the image root, and
SkillCards still select a skill from the bundle with `subPath`.

## Consequences

- Directory moves are the bulk of the change: `catalog/skills/{plan,execute,
  verify,javaee-to-quarkus}`, `catalog/Containerfile`, and
  `catalog/examples/` are relocations of existing files, so history follows
  via `git mv`.
- References that named the old paths are updated: the `Makefile`
  (`skill-build` build context, `SKILL_TREES`), the versioned publish
  workflow (`image-build-push.yml` skills job context/containerfile),
  `hack/harness-test/setup.sh`, `hack/setup-e2e.sh`, and the examples
  README/Containerfile comments.
- ADR 0014 and ADR 0007 quote old `skills/plan` paths in prose. They are
  immutable history and are left unchanged; a reader following those paths
  should read them as `catalog/skills/plan` under this ADR.
- `changes/unreleased/44-skill-packaging-and-delivery.yaml` (ADR 0015's
  changelog fragment) also names `skills/examples`; it describes the state
  as of that change and is left as-is.
- No user-facing SkillCard changes: the shipped bundle image name
  (`quay.io/konveyor/skills`) and the `subPath` values the sample SkillCards
  use are the same, because only the *source* directory moved, not the image
  root layout the loader sees.
