// Package skills assembles the pod's skill directory from the sources the
// controller staged, and validates what it assembled.
//
// A skill is an Agent Skills directory: a SKILL.md carrying YAML frontmatter,
// optionally alongside supporting files. Parsing and validating that format is
// not this package's job -- api/skill owns it, so that the controller checking
// spec.inline and the loader checking a mounted image agree on what a valid
// skill is (ADR 0015). This package decides what to assemble and from where.
package skills

import (
	"os"

	"github.com/konveyor/agentic-controller/api/skill"
	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// SkillFile is the file that makes a directory a skill.
const SkillFile = skill.File

// Load policies, spelled as the SkillCard CRD spells them so a source's type
// and a card's spec.type cannot drift apart.
const (
	// TypeSkill is loaded on demand by the agent runtime.
	TypeSkill = string(konveyoriov1alpha1.SkillCardTypeSkill)
	// TypeRule is injected into every prompt by the harness.
	TypeRule = string(konveyoriov1alpha1.SkillCardTypeRule)
)

// readFrontmatter loads and validates the frontmatter of a SKILL.md on disk.
func readFrontmatter(path string) (skill.Frontmatter, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return skill.Frontmatter{}, err
	}
	return skill.ParseAndValidate(content)
}
