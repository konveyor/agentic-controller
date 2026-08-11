package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konveyor/migration-harness/internal/config"
)

func TestDiscoverSkills_NoSkills(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HARNESS_SKILLS_DIR", dir)

	paths, err := discoverSkills()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
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

	paths, err := discoverSkills()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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

	paths, err := discoverSkills()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 1 {
		t.Errorf("expected 1 path (skill is mounted), got: %v", paths)
	}
}

func TestSymlinkSkillsDir(t *testing.T) {
	cloneDir := t.TempDir()
	skillsSrc := t.TempDir()

	if err := symlinkSkillsDir(cloneDir, skillsSrc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	link := filepath.Join(cloneDir, ".agents", "skills")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", link, err)
	}
	if target != skillsSrc {
		t.Errorf("symlink target = %q, want %q", target, skillsSrc)
	}
}

func TestSymlinkSkillsDir_AlreadyExists(t *testing.T) {
	cloneDir := t.TempDir()
	skillsSrc := t.TempDir()

	if err := os.MkdirAll(filepath.Join(cloneDir, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := symlinkSkillsDir(cloneDir, skillsSrc)
	if err == nil {
		t.Fatal("expected error when .agents/skills already exists")
	}
}

func TestSymlinkSkillsDir_RejectsSymlinkedAgentsDir(t *testing.T) {
	cloneDir := t.TempDir()
	outside := t.TempDir()
	skillsSrc := t.TempDir()

	if err := os.Symlink(outside, filepath.Join(cloneDir, ".agents")); err != nil {
		t.Fatal(err)
	}

	err := symlinkSkillsDir(cloneDir, skillsSrc)
	if err == nil {
		t.Fatal("expected error when .agents is a symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink, got: %v", err)
	}
}

func TestSymlinkSkillsDir_ResolvesRelativePath(t *testing.T) {
	parent := t.TempDir()
	cloneDir := filepath.Join(parent, "repo")
	skillsSrc := filepath.Join(parent, "skills")
	if err := os.MkdirAll(cloneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillsSrc, 0o755); err != nil {
		t.Fatal(err)
	}

	relPath, err := filepath.Rel(parent, skillsSrc)
	if err != nil {
		t.Fatal(err)
	}

	oldWd, _ := os.Getwd()
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWd)

	if err := symlinkSkillsDir(cloneDir, relPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	link := filepath.Join(cloneDir, ".agents", "skills")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", link, err)
	}
	if !filepath.IsAbs(target) {
		t.Errorf("symlink target should be absolute, got %q", target)
	}
}

func TestShouldRevokeToken(t *testing.T) {
	tests := []struct {
		name               string
		hubTokenID         string
		workflowStage      string
		workflowStageCount string
		want               bool
	}{
		{
			name: "no token ID — skip revocation",
			want: false,
		},
		{
			name:       "standalone run — revoke",
			hubTokenID: "1",
			want:       true,
		},
		{
			name:               "last workflow stage — revoke",
			hubTokenID:         "1",
			workflowStage:      "3",
			workflowStageCount: "3",
			want:               true,
		},
		{
			name:               "intermediate workflow stage — skip",
			hubTokenID:         "1",
			workflowStage:      "1",
			workflowStageCount: "3",
			want:               false,
		},
		{
			name:               "first of two stages — skip",
			hubTokenID:         "1",
			workflowStage:      "1",
			workflowStageCount: "2",
			want:               false,
		},
		{
			name:               "single-stage workflow — revoke",
			hubTokenID:         "1",
			workflowStage:      "1",
			workflowStageCount: "1",
			want:               true,
		},
		{
			name:               "stage set but count missing — skip",
			hubTokenID:         "1",
			workflowStage:      "1",
			workflowStageCount: "",
			want:               false,
		},
		{
			name:               "count set but stage missing — skip",
			hubTokenID:         "1",
			workflowStage:      "",
			workflowStageCount: "3",
			want:               false,
		},
		{
			name:               "stage exceeds count — skip",
			hubTokenID:         "1",
			workflowStage:      "5",
			workflowStageCount: "3",
			want:               false,
		},
		{
			name:               "non-numeric stage — skip",
			hubTokenID:         "1",
			workflowStage:      "abc",
			workflowStageCount: "3",
			want:               false,
		},
		{
			name:               "non-numeric count — skip",
			hubTokenID:         "1",
			workflowStage:      "1",
			workflowStageCount: "xyz",
			want:               false,
		},
		{
			name:               "stage zero — skip",
			hubTokenID:         "1",
			workflowStage:      "0",
			workflowStageCount: "3",
			want:               false,
		},
		{
			name:               "equal non-numeric values — skip",
			hubTokenID:         "1",
			workflowStage:      "abc",
			workflowStageCount: "abc",
			want:               false,
		},
		{
			name:               "equal zero values — skip",
			hubTokenID:         "1",
			workflowStage:      "0",
			workflowStageCount: "0",
			want:               false,
		},
		{
			name:               "non-numeric token ID — skip",
			hubTokenID:         "abc",
			workflowStage:      "",
			workflowStageCount: "",
			want:               false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				HubTokenID:         tt.hubTokenID,
				WorkflowStage:      tt.workflowStage,
				WorkflowStageCount: tt.workflowStageCount,
			}
			_, got := shouldRevokeToken(cfg)
			if got != tt.want {
				t.Errorf("shouldRevokeToken() = %v, want %v", got, tt.want)
			}
		})
	}
}
