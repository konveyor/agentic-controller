package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverSkills_NoSkills(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HARNESS_SKILLS_DIR", dir)

	content, paths, err := discoverSkills()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty content, got: %q", content)
	}
	if len(paths) != 0 {
		t.Errorf("expected no paths, got: %v", paths)
	}
}

func TestDiscoverSkills_WithSkills(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HARNESS_SKILLS_DIR", dir)

	skillDir := filepath.Join(dir, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("do the thing"), 0o644); err != nil {
		t.Fatal(err)
	}

	content, paths, err := discoverSkills()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "do the thing" {
		t.Errorf("expected skill content, got: %q", content)
	}
	if len(paths) != 1 {
		t.Errorf("expected 1 path, got: %v", paths)
	}
}

func TestDiscoverSkills_EmptySkillFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HARNESS_SKILLS_DIR", dir)

	skillDir := filepath.Join(dir, "empty-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	content, paths, err := discoverSkills()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty content, got: %q", content)
	}
	if len(paths) != 1 {
		t.Errorf("expected 1 path (skill is mounted), got: %v", paths)
	}
}

func TestBuildPrompt_NoSkills(t *testing.T) {
	t.Setenv("KONVEYOR_PROMPT", "hello")
	t.Setenv("KONVEYOR_PLAYBOOK_INSTRUCTIONS", "")
	t.Setenv("KONVEYOR_INSTRUCTIONS", "do work")

	prompt := buildPrompt("")
	expected := "hello\n\n## Working Guidelines\n\nCommit your changes to git with a descriptive message when your work is complete.\n\n## Stage Task\n\ndo work"
	if prompt != expected {
		t.Errorf("unexpected prompt:\n%s", prompt)
	}
}

func TestBuildPrompt_WithSkills(t *testing.T) {
	t.Setenv("KONVEYOR_PROMPT", "hello")
	t.Setenv("KONVEYOR_PLAYBOOK_INSTRUCTIONS", "")
	t.Setenv("KONVEYOR_INSTRUCTIONS", "do work")

	prompt := buildPrompt("skill content here")
	if got := prompt; got != "hello\n\n## Skill Instructions\n\nskill content here\n\n## Stage Task\n\ndo work" {
		t.Errorf("unexpected prompt:\n%s", got)
	}
}
