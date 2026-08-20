/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package skill

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ManifestFile records what the loader assembled, written into the skills
// root. A dotfile rather than a sibling directory: the root is shared with the
// agent container, and runtimes discover skills by looking for SKILL.md, so a
// dotfile is inert to them.
//
// It is the one contract that spans images. The loader writes it from the
// controller's image and the harness reads it from the agent's, so it is kept
// small and additive on purpose.
const ManifestFile = ".konveyor-skills.json"

// Loaded is one assembled skill.
type Loaded struct {
	// Name is the frontmatter name, and also the directory it was assembled
	// into.
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	// Source is the staging directory it came from, the SkillCard name.
	Source string `json:"source"`
	// SourcePath is where inside that source it was found. Empty for a
	// single-skill source, the subdirectory name for one out of a bundle.
	SourcePath string `json:"sourcePath,omitempty"`
}

// Manifest is what the loader wrote.
type Manifest struct {
	Skills []Loaded `json:"skills"`
	// Rules lists the names of skills with type: rule, in assembly order. The
	// harness injects these into every prompt.
	Rules []string `json:"rules"`
}

// Rule is one always-loaded skill, ready to place in a prompt.
type Rule struct {
	Name string
	Body string
}

// ReadManifest loads what the loader recorded for this pod. A missing file
// means no skills were staged, which is an ordinary run rather than an error
// (#82), so the harness must not refuse to start over it.
func ReadManifest(skillsDir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(skillsDir, ManifestFile))
	if err != nil {
		if os.IsNotExist(err) {
			return &Manifest{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", ManifestFile, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ManifestFile, err)
	}
	return &m, nil
}

// RuleContent returns the body of every always-loaded rule, in manifest order.
//
// No runtime feature guarantees content reaches the model, so a rule is
// injected rather than left for the agent to load (ADR 0014). The body is read
// from the assembled root, which is the same content the runtime would serve.
func RuleContent(skillsDir string, m *Manifest) ([]Rule, error) {
	out := make([]Rule, 0, len(m.Rules))
	for _, name := range m.Rules {
		body, err := os.ReadFile(filepath.Join(skillsDir, name, File))
		if err != nil {
			return nil, fmt.Errorf("reading rule %q: %w", name, err)
		}
		out = append(out, Rule{Name: name, Body: Body(string(body))})
	}
	return out, nil
}
