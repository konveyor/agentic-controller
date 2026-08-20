# Example Skills

Example agent skills for testing and development. Each skill follows
the [Agent Skills](https://agentskills.io) format: a `SKILL.md` carrying
YAML frontmatter, optionally alongside supporting files. Frontmatter is
the only skill metadata; there is no sidecar manifest.

These are deliberately outside the bundle image `make skill-build`
publishes, which carries the skills we ship. Each one that needs an image
gets its own `Containerfile`; `ejb-to-cdi/Containerfile` is the worked
example.

## Building an image for one skill

A skill image is an ordinary OCI image, so any builder will do:

```bash
podman build -t quay.io/konveyor/skills:ejb-to-cdi \
  -f skills/examples/ejb-to-cdi/Containerfile skills/examples/ejb-to-cdi
```

To check a skill before publishing it, run the same validation the pod
runs at init:

```bash
make skill-validate
# or one tree at a time
go run ./cmd/skill-loader validate skills/examples
```

## Using with SkillCard CRs

```yaml
apiVersion: konveyor.io/v1alpha1
kind: SkillCard
metadata:
  name: maven-migration
spec:
  image: quay.io/konveyor/skills:maven-migration
  displayName: Maven Migration
  version: "1.0.0"
  description: Migrates Maven POM files from Java EE to Jakarta EE.
  type: skill
  tags: [java, maven, migration]
```

An image holding several skills is referenced once per skill, each card
selecting its own directory with `subPath`.

## Skills

| Skill | Type | Description |
|-------|------|-------------|
| `maven-migration` | skill | Migrates Maven POM files from Java EE to Jakarta EE |
| `no-javax-imports` | rule | Enforces that no javax.* imports remain after migration |
| `ejb-to-cdi` | skill | Migrates EJB components to CDI managed beans |
