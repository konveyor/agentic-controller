package skill

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSkillDir lays out one assembled skill the way the loader would.
func writeSkillDir(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: does " + name + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, File), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadManifestTreatsAMissingFileAsNoSkills(t *testing.T) {
	m, err := ReadManifest(t.TempDir())
	if err != nil {
		t.Fatalf("a missing manifest should not be an error: %v", err)
	}
	if len(m.Skills) != 0 || len(m.Rules) != 0 {
		t.Errorf("manifest = %+v, want empty", m)
	}
}

func TestReadManifestRejectsAnUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(dir); err == nil {
		t.Fatal("want an error for an unparseable manifest")
	}
}

func TestRuleContentFailsOnAMissingRule(t *testing.T) {
	dir := t.TempDir()
	if _, err := RuleContent(dir, &Manifest{Rules: []string{"gone"}}); err == nil {
		t.Fatal("want an error when a manifest rule has no SKILL.md")
	}
}

func TestRuleContentPreservesManifestOrder(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"alpha", "beta"} {
		writeSkillDir(t, filepath.Join(dir, n), n)
	}
	rules, err := RuleContent(dir, &Manifest{Rules: []string{"beta", "alpha"}})
	if err != nil {
		t.Fatalf("rule content: %v", err)
	}
	if len(rules) != 2 || rules[0].Name != "beta" || rules[1].Name != "alpha" {
		t.Errorf("rules = %+v, want manifest order", rules)
	}
}
