package main

import (
	"testing"

	"github.com/konveyor/migration-harness/internal/skills"
)

// Unset and empty both mean "nothing declared", which falls back to scanning
// the staging directory. A pod with no skills is an ordinary pod (#82), so
// neither can be an error.
func TestParseSourcesTreatsEmptyAsUndeclared(t *testing.T) {
	got, err := parseSources("")
	if err != nil {
		t.Fatalf("empty should not be an error: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestParseSourcesReadsTheDeclaredList(t *testing.T) {
	raw := `[{"name":"skill-plan","subPath":"plan","type":"rule"},
	         {"name":"house","git":{"url":"https://example.com/acme.git","ref":"v1"}}]`

	got, err := parseSources(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sources, want 2", len(got))
	}

	if got[0].Name != "skill-plan" || got[0].SubPath != "plan" {
		t.Errorf("first source = %+v", got[0])
	}
	// Load policy travels with the source, because it comes from the
	// SkillCard rather than from the content.
	if got[0].Type != skills.TypeRule {
		t.Errorf("type = %q, want %q", got[0].Type, skills.TypeRule)
	}
	if got[1].Git == nil || got[1].Git.URL != "https://example.com/acme.git" {
		t.Fatalf("second source lost its git config: %+v", got[1])
	}
	// Without the ref a clone is not reproducible, so it has to survive.
	if got[1].Git.Ref != "v1" {
		t.Errorf("ref = %q, want v1", got[1].Git.Ref)
	}
}

// A malformed list means the controller and the loader disagree about what is
// mounted. Scanning instead would start an agent with the wrong skills, which
// is worse than not starting.
func TestParseSourcesRejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"not json":        "{not json",
		"not an array":    `{"name":"plan"}`,
		"nameless entry":  `[{"subPath":"plan"}]`,
		"git with no url": `[{"name":"house","git":{"ref":"v1"}}]`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSources(raw); err == nil {
				t.Errorf("want an error for %q", raw)
			}
		})
	}
}
