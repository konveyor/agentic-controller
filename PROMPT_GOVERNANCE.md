# Agentic Skill Governance

This document defines the change-management lifecycle for the **skills** the
agentic controller ships: the `SKILL.md` instruction sets under
[`catalog/skills/`](catalog/skills/) that agents load and execute at run time.
It is evidence for ISO/IEC 42001 **A.6.1.3 (Processes for responsible AI
system design and development)**, whose guidance covers change control and
approvals for AI systems: skills are the instruction set defining agent
behavior, so they are treated as governed source code - version-controlled,
peer-reviewed, and validated by CI before distribution. The review
enforcement also evidences A.3.2, and the CI validation A.6.2.4 and A.6.2.5.

## Scope - what is governed

- [`catalog/skills/`](catalog/skills/) - the shipped skills, one `SKILL.md`
  per skill, assembled into the skills bundle by `make skill-build` and
  published as the skills image.
- [`catalog/examples/`](catalog/examples/) - reference skills documenting the
  format; they shape what users copy.

The curated orchestration that selects which agents, workflows and skillcards
ship (the operator's `roles/tackle/templates/agentic/defaults/`) is governed
in konveyor/operator. The prompt content itself is governed here.

`api/skill/` (frontmatter parsing, manifest, assembly) is regular source code
and follows the repo's normal review flow, but changes to which frontmatter
fields are accepted are governance-relevant and worth calling out in a PR.

## Roles

- **Skill owners** - required approvers for any change under `catalog/`,
  assigned via [`.github/CODEOWNERS`](.github/CODEOWNERS):
  @djzager @fabianvf @ibolton336 @hhpatel14.

## Lifecycle of a skill change

1. **Branch & edit** the `SKILL.md` asset(s).
2. **Validate locally**: `make skill-validate` (frontmatter against the
   closed Agent Skills field set) and `make skill-build` (the bundle
   assembles).
3. **Complete the prompt-injection threat-model checklist** (below) in the PR.
4. **Open a PR** with DCO sign-off (`git commit -s`). CODEOWNERS routes it to
   a skill owner; the [skills
   workflow](.github/workflows/skills.yml) runs validation automatically.
5. **Merge** only after required review + green CI.

## CI gates

The [Validate and Build Skills
workflow](.github/workflows/skills.yml), triggered on catalog changes, runs:

- **Frontmatter validation** - `make skill-validate`: every skill parses, and
  frontmatter stays within the closed Agent Skills field set (ADR 0015).
- **Bundle build** - `make skill-build`: the catalog assembles into the
  bundle the skills image is built from.

Planned follow-ups, not yet in place: a version registry for the catalog with
a CI check that a PR touching a skill bumps its version, and a semantic
regression suite (scripted model responses against a baseline) so wording
changes cannot silently alter agent behavior.

## Prompt-injection threat-model checklist

Complete for every skill change that adds or alters instructions, especially
anything changing how the agent treats analyzed-repo content:

- [ ] **Untrusted content** - analysis results, repo code and file contents
      are data the agent reasons about. Confirm the skill keeps treating them
      as data and cannot be read as instructions overriding the skill's
      contract.
- [ ] **Capability scope** - a skill change can widen what an agent may do
      (file writes, commands, network). Confirm any new capability is
      intentional and minimal.
- [ ] **Output handling** - confirm the skill's expectations about agent
      output (where results are written, what gets committed) stay within the
      run's sandbox and the write-back path the workflow allows.
- [ ] **Regression** - existing validation still passes, and the change is
      called out in the PR description.

## Branch protection (repo admin action - not in code)

A repository admin must, for `main`:

1. Require review from Code Owners so `catalog/**` edits need a skill owner.
2. Require the skills workflow checks on `catalog/**` changes.
