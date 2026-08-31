# Creating SkillCard images

This guide covers authoring a skill, packaging it as an OCI image, publishing
it, and referencing it from a `SkillCard` and `SkillCollection`.

The implementation follows [ADR 0001](adr/0001-agentic-platform-crd-architecture.md),
[ADR 0007](adr/0007-harness-thin-runner-and-skillcard-skills.md), and the
packaging decisions in [ADR 0015](adr/0015-skill-packaging-and-delivery.md).

## Current skill format

A skill is an [Agent Skills](https://agentskills.io) directory. Its required
file is `SKILL.md`, beginning with YAML frontmatter containing `name` and
`description`:

```text
my-skill/
├── SKILL.md
├── references/       optional supporting material
├── scripts/          optional executable helpers
└── assets/           optional templates or other assets
```

Example:

```markdown
---
name: maven-migration
description: Migrates Maven POM files from Java EE to Jakarta EE.
---

# Maven Migration

Inspect every POM, replace the relevant `javax.*` dependencies with their
Jakarta equivalents, and run the project's build to verify the result.
```

The Agent Skills frontmatter field set is closed. Use `license`,
`compatibility`, `metadata`, and `allowed-tools` only when needed; do not add
`type` to `SKILL.md`.

### `skill` versus `rule`

The load policy belongs to the `SkillCard` CR, not the skill content:

- `type: skill` is on-demand. The runtime starts with the name and
  description and reads the full content when it invokes the skill.
- `type: rule` is always-loaded. The harness includes its full content in
  every agent prompt.

The default is `skill`. Keeping `type` on the CR preserves a valid
Agent Skills `SKILL.md` and lets the same image be used as either a skill or a
rule by different consumers.

### No `skill.yaml` or `skillctl` is required

Older skillimage documentation described a second `skill.yaml` metadata file
and the `skillctl` CLI. This repository no longer uses that contract:
`SKILL.md` frontmatter is the only skill metadata, and a skill image is an
ordinary OCI image. Do not add a sidecar `skill.yaml`; see ADR 0015 for the
decision and rationale.

## Build an image

### One skill per image

For a single-skill image, put `SKILL.md` at the image root:

```Dockerfile
FROM scratch
COPY . /
```

The repository's worked example is
`catalog/examples/ejb-to-cdi/Containerfile`. Build it from its directory:

```bash
podman build \
  -t quay.io/<org>/ejb-to-cdi:0.1.0 \
  -f catalog/examples/ejb-to-cdi/Containerfile \
  catalog/examples/ejb-to-cdi
```

### Several skills per image

An image may contain several immediate skill directories. The shipped bundle
uses this layout:

```text
catalog/skills/
├── plan/SKILL.md
├── execute/SKILL.md
├── verify/SKILL.md
└── javaee-to-quarkus/SKILL.md
```

Its `catalog/Containerfile` is:

```Dockerfile
FROM scratch
COPY skills/ /
```

Build the bundle with:

```bash
make skill-build \
  CONTAINER_TOOL=podman \
  SKILL_IMAGE=quay.io/<org>/skills:dev
```

Whether an image has one skill or many is determined by its filesystem:
`SKILL.md` at the image root means one skill; otherwise immediate
subdirectories containing `SKILL.md` are discovered as individual skills.

## Validate, tag, and publish

Validate before building or publishing. The validator applies the same
frontmatter rules used by the controller's loader:

```bash
go run ./cmd/skill-loader validate catalog/examples/ejb-to-cdi
make skill-validate
```

For an external skill author, the validator can also be installed:

```bash
go install github.com/konveyor/agentic-controller/cmd/skill-loader@latest
skill-loader validate ./my-skill
```

Publish a single-skill image with an immutable version tag, then optionally
move a convenience tag to the same image:

```bash
podman login quay.io
podman push quay.io/<org>/ejb-to-cdi:0.1.0
podman tag quay.io/<org>/ejb-to-cdi:0.1.0 quay.io/<org>/ejb-to-cdi:latest
podman push quay.io/<org>/ejb-to-cdi:latest
```

For production references, prefer a digest
(`image: quay.io/<org>/ejb-to-cdi@sha256:...`) when the deployment process
has resolved one. A tag is convenient for development but can change
underneath a run.

## Create a SkillCard

A `SkillCard` represents exactly one skill. For a single-skill image, omit
`subPath`:

```yaml
apiVersion: konveyor.io/v1alpha1
kind: SkillCard
metadata:
  name: ejb-to-cdi
spec:
  image: quay.io/<org>/ejb-to-cdi:0.1.0
  displayName: EJB to CDI
  version: "0.1.0"
  description: Migrates EJB components to CDI managed beans.
  type: skill
  tags:
    - java
    - migration
```

For a bundle image, select one immediate directory with `subPath`:

```yaml
apiVersion: konveyor.io/v1alpha1
kind: SkillCard
metadata:
  name: plan
spec:
  image: quay.io/<org>/skills:dev
  subPath: plan
  displayName: Migration Planning
  version: "1.0.0"
  description: Analyzes a project and produces a structured migration plan.
  type: rule
```

The loader validates the image at pod initialization and assembles the
selected skill at `/opt/skills/<frontmatter-name>/SKILL.md`. A bundle without
`subPath` is ambiguous and is rejected rather than silently becoming several
skills.

## Create a SkillCollection

A `SkillCollection` groups existing SkillCards for an Agent. Grouping is
separate from packaging: cards can come from different images, and several
cards can select different directories from one image.

```yaml
apiVersion: konveyor.io/v1alpha1
kind: SkillCollection
metadata:
  name: java-migration-skills
spec:
  version: "1.0.0"
  skills:
    - name: plan
      skillCardRef: plan
    - name: ejb-to-cdi
      skillCardRef: ejb-to-cdi
```

Reference the collection from an `Agent`:

```yaml
spec:
  skillCollections:
    - ref: java-migration-skills
```

The collection does not build or publish images. The author builds the OCI
image first; the controller only resolves and delivers it.

## Test locally

The repository's end-to-end harness setup builds the Java agent and the
`plan`, `execute`, and `verify` skill images, loads them into Kind, and
applies a workflow using them:

```bash
export HUB_TOKEN=<scoped-hub-token>
hack/harness-test/setup.sh
```

The script expects a running Kind cluster from `make e2e-setup`, Podman (or
`CONTAINER_TOOL=docker`), Google credentials, and a configured Hub. Watch the
result with:

```bash
kubectl get agentworkflowrun -w
kubectl get pods
kubectl logs -f <stage-pod> -c agent
```

For a quick image-volume check on a Kubernetes 1.33+ cluster, mount the
published image into a diagnostic container and confirm its source shape:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: inspect-skill-image
spec:
  restartPolicy: Never
  containers:
    - name: inspect
      image: registry.access.redhat.com/ubi10/ubi-minimal:latest
      command: ["sh", "-c", "find /mnt/skill -name SKILL.md -print"]
      volumeMounts:
        - name: skill
          mountPath: /mnt/skill
          readOnly: true
  volumes:
    - name: skill
      image:
        reference: quay.io/<org>/ejb-to-cdi:0.1.0
        pullPolicy: IfNotPresent
```

The controller's loader performs the complete check: it validates
frontmatter, handles bundle `subPath` selection, copies each selected skill
under its frontmatter name, and makes the final runtime path
`/opt/skills/<name>/SKILL.md`.

## Existing examples

- [`catalog/examples/maven-migration`](../catalog/examples/maven-migration/SKILL.md)
  — a single-file migration skill.
- [`catalog/examples/ejb-to-cdi`](../catalog/examples/ejb-to-cdi/SKILL.md)
  — a single-skill image with a `Containerfile`.
- [`catalog/examples/no-javax-imports`](../catalog/examples/no-javax-imports/SKILL.md)
  — an always-loaded rule when referenced with `type: rule`.
- [`catalog/skills`](../catalog/skills) — the multi-skill bundle shipped by
  this repository.
