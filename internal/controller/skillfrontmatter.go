/*
Copyright 2026.

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

package controller

import (
	"fmt"
	"strings"

	"github.com/konveyor/agentic-controller/api/skill"
)

// Frontmatter validation for inline SkillCards.
//
// This deliberately covers only the inline case. Image and git sources are
// validated by the skill loader at pod init, which is the only place their
// bytes exist. Both call the same api/skill validator, so a skill's verdict
// does not depend on which side of the boundary it was checked on (ADR 0015).

// validateInlineSkill reports why inline content would not work as a skill.
//
// The controller can check this one source without a network call, because the
// bytes are already in the CR. Catching it here means a broken inline card is
// not-Ready immediately, rather than failing every pod that references it.
func validateInlineSkill(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("inline content is empty")
	}
	if _, err := skill.ParseAndValidate([]byte(content)); err != nil {
		return fmt.Errorf("inline content is not a valid skill: %w", err)
	}
	return nil
}
