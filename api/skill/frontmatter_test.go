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

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    Frontmatter
		wantErr string
	}{
		{
			name:    "name and description",
			content: "---\nname: plan\ndescription: Plans a migration.\n---\n\n# Plan\n",
			want:    Frontmatter{Name: "plan", Description: "Plans a migration."},
		},
		{
			// Parse reports what the header says; the closed-field-set check
			// is Validate's, so a type: key survives parsing and is rejected
			// there. It never becomes load policy either way: that is the
			// SkillCard's.
			name:    "a type key does not become load policy",
			content: "---\nname: no-javax\ndescription: d\ntype: rule\n---\n",
			want:    Frontmatter{Name: "no-javax", Description: "d"},
		},
		{
			name:    "the spec's other optional fields are ignored",
			content: "---\nname: a\ndescription: d\ncompatibility: needs git\nallowed-tools: Read\n---\n",
			want:    Frontmatter{Name: "a", Description: "d"},
		},
		{
			name:    "folded description, as the shipped skills use",
			content: "---\nname: verify\ndescription: >\n  Runs the build and\n  fixes what it can.\n---\n",
			want:    Frontmatter{Name: "verify", Description: "Runs the build and fixes what it can."},
		},
		{
			name:    "unknown keys are ignored",
			content: "---\nname: a\ndescription: d\nlicense: Apache-2.0\nmetadata:\n  source: java-ee-7\n---\n",
			want:    Frontmatter{Name: "a", Description: "d"},
		},
		{
			name:    "CRLF",
			content: "---\r\nname: a\r\ndescription: d\r\n---\r\n",
			want:    Frontmatter{Name: "a", Description: "d"},
		},
		{
			name:    "a rule inside the body does not close the block early",
			content: "---\nname: a\ndescription: \"a --- b\"\n---\n\ntext\n",
			want:    Frontmatter{Name: "a", Description: "a --- b"},
		},
		{
			name:    "no frontmatter",
			content: "# Just markdown\n",
			wantErr: "does not start with ---",
		},
		{
			name:    "unterminated",
			content: "---\nname: a\ndescription: d\n",
			wantErr: "unterminated",
		},
		{
			name:    "malformed yaml",
			content: "---\nname: [unclosed\n---\n",
			wantErr: "parsing frontmatter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.content))
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %q", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Name != tt.want.Name || got.Description != tt.want.Description {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFrontmatterValidate(t *testing.T) {
	tests := []struct {
		name    string
		fm      Frontmatter
		wantErr string
	}{
		{"valid", Frontmatter{Name: "a", Description: "d"}, ""},
		{"hyphenated name", Frontmatter{Name: "pdf-processing", Description: "d"}, ""},
		{"no name", Frontmatter{Description: "d"}, "no name"},
		{"no description", Frontmatter{Name: "a"}, "no description"},

		// The spec's name rules, not ours: lowercase alphanumerics and single
		// hyphens, 1-64 characters. A name failing here fails skills-ref too.
		{"path separator", Frontmatter{Name: "a/b", Description: "d"}, "contains"},
		{"parent traversal", Frontmatter{Name: "..", Description: "d"}, "contains"},
		{"uppercase", Frontmatter{Name: "PDF-Processing", Description: "d"}, "contains"},
		{"underscore", Frontmatter{Name: "a_b", Description: "d"}, "contains"},
		{"leading hyphen", Frontmatter{Name: "-pdf", Description: "d"}, "hyphen"},
		{"trailing hyphen", Frontmatter{Name: "pdf-", Description: "d"}, "hyphen"},
		{"consecutive hyphens", Frontmatter{Name: "pdf--processing", Description: "d"}, "consecutive"},
		{"name over 64", Frontmatter{Name: strings.Repeat("a", 65), Description: "d"}, "limit is 64"},
		{"description over 1024", Frontmatter{Name: "a", Description: strings.Repeat("d", 1025)}, "limit is 1024"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fm.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestBody(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "removes the block and its blank line",
			in:   "---\nname: plan\ndescription: d\n---\n\n# Plan\n\nBody.\n",
			want: "# Plan\n\nBody.\n",
		},
		{
			name: "handles CRLF",
			in:   "---\r\nname: plan\r\n---\r\n\r\nBody.\r\n",
			want: "Body.\r\n",
		},
		{
			// A rule with no frontmatter never reaches here, but returning the
			// content unchanged is better than returning nothing.
			name: "leaves content without frontmatter alone",
			in:   "# Plan\n\nBody.\n",
			want: "# Plan\n\nBody.\n",
		},
		{
			name: "leaves an unterminated block alone",
			in:   "---\nname: plan\n\n# Plan\n",
			want: "---\nname: plan\n\n# Plan\n",
		},
		{
			// The separator inside a body is not a fence: only the leading
			// block is frontmatter, and everything after it is content.
			name: "keeps a horizontal rule in the body",
			in:   "---\nname: plan\n---\n\nOne.\n\n---\n\nTwo.\n",
			want: "One.\n\n---\n\nTwo.\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Body(tc.in); got != tc.want {
				t.Errorf("Body() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The spec's field set is closed and its reference validator errors on
// anything outside it, so a skill that passes here must also pass skills-ref.
// Accepting a stray top-level key would let us ship a SKILL.md that is not
// actually in the format ADR 0001 says we follow.
func TestValidateRejectsFieldsOutsideTheSpec(t *testing.T) {
	content := "---\nname: plan\ndescription: plans work\ntags:\n  - java\ninputs: []\n---\n\n# Plan\n"

	_, err := ParseAndValidate([]byte(content))
	if err == nil {
		t.Fatal("want an error for non-spec frontmatter fields")
	}
	// Both offenders named, so the author fixes them in one pass.
	for _, want := range []string{"inputs", "tags"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
}

// type: is the specific case ADR 0015 turns on -- it is why load policy lives
// on the SkillCard rather than in frontmatter.
func TestValidateRejectsTypeInFrontmatter(t *testing.T) {
	content := "---\nname: plan\ndescription: plans work\ntype: rule\n---\n\n# Plan\n"

	if _, err := ParseAndValidate([]byte(content)); err == nil {
		t.Fatal("want an error: type is not an Agent Skills field, load policy is on the SkillCard")
	}
}

// The spec's optional fields are not ours to reject. javaee-to-quarkus ships
// license and metadata today.
func TestValidateAcceptsTheSpecsOptionalFields(t *testing.T) {
	content := "---\nname: plan\ndescription: plans work\nlicense: Apache-2.0\n" +
		"compatibility: []\nallowed-tools: []\nmetadata:\n  domain: java\n---\n\n# Plan\n"

	if _, err := ParseAndValidate([]byte(content)); err != nil {
		t.Fatalf("spec-allowed optional fields were rejected: %v", err)
	}
}

// Every skill this repo ships has to pass the validator the loader runs at pod
// init, or a release fails in the cluster rather than in CI.
func TestShippedSkillsAreValid(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "..", "skills", "*", File))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no skills found; the glob is wrong or the layout moved")
	}
	for _, path := range matches {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fm, err := ParseAndValidate(content)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			// The spec requires the two to match, and the loader mounts a
			// skill at its frontmatter name -- so a mismatch here means the
			// directory in this repo is not where the skill lands in the pod.
			if dir := filepath.Base(filepath.Dir(path)); fm.Name != dir {
				t.Errorf("frontmatter name %q does not match directory %q", fm.Name, dir)
			}
		})
	}
}
