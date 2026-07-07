# Example Skills

Example agent skills for testing and development. Each skill follows
the [Agent Skills](https://agentskills.io) format with a `SKILL.md`
prompt file and a `skill.yaml` metadata file.

## Building OCI artifacts

Use `skillctl` to build and push skills as OCI images:

```bash
# Build all skills
make skill-build

# Build and push all skills to quay.io/konveyor/skills
make skill-push

# Install locally (for development)
skillctl install quay.io/konveyor/skills:maven-migration --target opencode
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

## Skills

| Skill | Type | Description |
|-------|------|-------------|
| `maven-migration` | skill | Migrates Maven POM files from Java EE to Jakarta EE |
| `no-javax-imports` | rule | Enforces that no javax.* imports remain after migration |
| `ejb-to-cdi` | skill | Migrates EJB components to CDI managed beans |
