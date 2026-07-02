# Example Skills

Example agent skills for testing and development. Each skill follows
the [Agent Skills](https://agentskills.io) format with a `SKILL.md`
prompt file and a `skill.yaml` metadata file.

## Building OCI artifacts

Use `skillctl` to build and push skills as OCI images:

```bash
# Build a skill
skillctl build skills/examples/maven-migration/

# Push to a registry
skillctl push quay.io/konveyor/skills/maven-migration:1.0.0

# Install locally (for development)
skillctl install quay.io/konveyor/skills/maven-migration:1.0.0 --target opencode
```

## Using with SkillCard CRs

```yaml
apiVersion: konveyor.io/v1alpha1
kind: SkillCard
metadata:
  name: maven-migration
spec:
  image: quay.io/konveyor/skills/maven-migration:1.0.0
  displayName: Maven Migration
  version: "1.0.0"
  description: Migrates Maven POM files from Java EE to Jakarta EE.
  type: skill
  tags: [java, maven, migration]
```

## Skills

| Skill | Type | Description |
|-------|------|-------------|
| `maven-migration` | skill | Migrates Maven POM files from Java EE to Jakarta EE |
| `no-javax-imports` | rule | Enforces that no javax.* imports remain after migration |
