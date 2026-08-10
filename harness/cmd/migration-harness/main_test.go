package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/konveyor/migration-harness/internal/config"
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

func TestParseHubTokenID(t *testing.T) {
	tests := []struct {
		name       string
		hubTokenID string
		wantID     uint
		wantOK     bool
	}{
		{"empty", "", 0, false},
		{"valid", "42", 42, true},
		{"non-numeric", "abc", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{HubTokenID: tt.hubTokenID}
			id, ok := parseHubTokenID(cfg)
			if id != tt.wantID || ok != tt.wantOK {
				t.Errorf("parseHubTokenID() = (%d, %v), want (%d, %v)", id, ok, tt.wantID, tt.wantOK)
			}
		})
	}
}

func TestIsIntermediateWorkflowStage(t *testing.T) {
	tests := []struct {
		name               string
		workflowStage      string
		workflowStageCount string
		want               bool
	}{
		{"standalone run", "", "", false},
		{"last stage 3/3", "3", "3", false},
		{"intermediate 1/3", "1", "3", true},
		{"intermediate 1/2", "1", "2", true},
		{"intermediate 2/3", "2", "3", true},
		{"single stage 1/1", "1", "1", false},
		{"stage only", "1", "", false},
		{"count only", "", "3", false},
		{"stage zero", "0", "3", false},
		{"count zero", "1", "0", false},
		{"non-numeric stage", "abc", "3", false},
		{"non-numeric count", "1", "xyz", false},
		{"stage exceeds count", "5", "3", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				WorkflowStage:      tt.workflowStage,
				WorkflowStageCount: tt.workflowStageCount,
			}
			if got := isIntermediateWorkflowStage(cfg); got != tt.want {
				t.Errorf("isIntermediateWorkflowStage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTokenRevocationDecision(t *testing.T) {
	tests := []struct {
		name               string
		hubTokenID         string
		workflowStage      string
		workflowStageCount string
		stageSucceeded     bool
		wantRevoke         bool
	}{
		{"no token ID", "", "", "", false, false},
		{"standalone success", "1", "", "", true, true},
		{"standalone failure", "1", "", "", false, true},
		{"last stage success", "1", "3", "3", true, true},
		{"last stage failure", "1", "3", "3", false, true},
		{"intermediate success — defer to next stage", "1", "1", "3", true, false},
		{"intermediate failure — revoke (#109)", "1", "1", "3", false, true},
		{"single-stage success", "1", "1", "1", true, true},
		{"single-stage failure", "1", "1", "1", false, true},
		{"non-numeric token ID", "abc", "", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				HubTokenID:         tt.hubTokenID,
				WorkflowStage:      tt.workflowStage,
				WorkflowStageCount: tt.workflowStageCount,
			}
			_, hasToken := parseHubTokenID(cfg)
			if !hasToken {
				if tt.wantRevoke {
					t.Error("expected revocation but no token ID available")
				}
				return
			}
			intermediate := isIntermediateWorkflowStage(cfg)
			shouldRevoke := !(intermediate && tt.stageSucceeded)
			if shouldRevoke != tt.wantRevoke {
				t.Errorf("revocation decision = %v, want %v (intermediate=%v, stageSucceeded=%v)",
					shouldRevoke, tt.wantRevoke, intermediate, tt.stageSucceeded)
			}
		})
	}
}
