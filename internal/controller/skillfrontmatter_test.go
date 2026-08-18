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
	"strings"
	"testing"
)

func TestValidateInlineSkill(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "valid",
			content: "---\nname: house-rules\ndescription: never leave javax imports\n---\n\nrules here\n",
		},
		{
			name:    "folded description",
			content: "---\nname: a\ndescription: >\n  spans\n  lines\n---\n",
		},
		{
			// license is in the spec's field set, so it is accepted.
			name:    "the spec's optional fields are accepted",
			content: "---\nname: a\ndescription: d\nlicense: Apache-2.0\n---\n",
		},
		{
			name:    "empty",
			content: "   \n",
			wantErr: "empty",
		},
		{
			// The case ADR 0014 flags: without frontmatter the card resolves,
			// mounts and reports Ready while contributing nothing.
			name:    "no frontmatter",
			content: "# Just some markdown\n\nDo the thing.\n",
			wantErr: "no YAML frontmatter",
		},
		{
			name:    "unterminated frontmatter",
			content: "---\nname: a\ndescription: d\n",
			wantErr: "no closing ---",
		},
		{
			name:    "no name",
			content: "---\ndescription: d\n---\n",
			wantErr: "no name",
		},
		{
			name:    "no description makes the skill unselectable",
			content: "---\nname: a\n---\n",
			wantErr: "no description",
		},
		{
			// Load policy is spec.type on the card. A type: key here is not an
			// Agent Skills field at all, so it fails as one.
			name:    "type in frontmatter",
			content: "---\nname: a\ndescription: d\ntype: rule\n---\n",
			wantErr: "does not define",
		},
		{
			name:    "malformed yaml",
			content: "---\nname: [unclosed\n---\n",
			wantErr: "parsing frontmatter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInlineSkill(tt.content)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %q", tt.wantErr, err)
			}
		})
	}
}
