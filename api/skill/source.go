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

package skill

// The skill source list the controller hands the loader.
//
// These types are the wire format of KONVEYOR_SKILL_SOURCES: the controller
// marshals them onto the loader's environment and the loader unmarshals them
// at pod init. They live here for the same reason frontmatter parsing does --
// the two sides are separate Go modules and this is the one both already
// import -- so a field added on one side is a compile-time fact on the other
// rather than a tag that silently stops arriving.

// Source describes one SkillCard, as the controller declares it. A SkillCard
// is one skill, so a source that resolves to more than one is an error unless
// SubPath picks which.
type Source struct {
	// Name is the SkillCard name. It labels the source in diagnostics and
	// names its staging directory. It does not name the skill: that comes
	// from the skill's own frontmatter.
	Name string `json:"name"`

	// SubPath selects one skill from a source holding several. For a git
	// source it is relative to the clone; for a mounted one, to the mount.
	SubPath string `json:"subPath,omitempty"`

	// Type is the skill's load policy, from the SkillCard. Empty means
	// "skill". Content never declares this, since the Agent Skills spec has
	// no field for it.
	Type string `json:"type,omitempty"`

	// Git is set when the source is cloned rather than mounted.
	Git *GitSource `json:"git,omitempty"`
}

// GitSource is a repository cloned at pod start.
type GitSource struct {
	URL string `json:"url"`
	// Ref is a branch, tag or commit. Empty clones the default branch, which
	// means the run is not reproducible, and the loader says so.
	Ref string `json:"ref,omitempty"`
}
