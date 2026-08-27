package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konveyor/migration-harness/internal/config"
	"github.com/konveyor/migration-harness/internal/hub"
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

func TestResolveFromHub_NoRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/applications/42" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42,"name":"missing-repo","repository":null}`))
	}))
	defer server.Close()

	cfg := &config.Config{AppID: "42"}
	_, err := resolveFromHub(cfg, hub.NewClient(server.URL, "test-token"))
	if err == nil {
		t.Fatal("expected an error for an application without a source repository")
	}
	if !strings.Contains(err.Error(), `application "missing-repo" has no source repository configured`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSymlinkSkillsDir(t *testing.T) {
	homeDir := t.TempDir()
	skillsSrc := t.TempDir()

	if err := symlinkSkillsDir(homeDir, skillsSrc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	link := filepath.Join(homeDir, ".agents", "skills")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", link, err)
	}
	if target != skillsSrc {
		t.Errorf("symlink target = %q, want %q", target, skillsSrc)
	}
}

func TestSymlinkSkillsDir_AlreadyExistsDir(t *testing.T) {
	homeDir := t.TempDir()
	skillsSrc := t.TempDir()

	if err := os.MkdirAll(filepath.Join(homeDir, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := symlinkSkillsDir(homeDir, skillsSrc)
	if err == nil {
		t.Fatal("expected error when .agents/skills already exists as a directory")
	}
	if !strings.Contains(err.Error(), "not a symlink") {
		t.Errorf("error should mention 'not a symlink', got: %v", err)
	}
}

func TestSymlinkSkillsDir_Idempotent(t *testing.T) {
	homeDir := t.TempDir()
	skillsSrc := t.TempDir()

	if err := symlinkSkillsDir(homeDir, skillsSrc); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := symlinkSkillsDir(homeDir, skillsSrc); err != nil {
		t.Fatalf("second call (same target) should be idempotent: %v", err)
	}

	link := filepath.Join(homeDir, ".agents", "skills")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", link, err)
	}
	if target != skillsSrc {
		t.Errorf("symlink target = %q, want %q", target, skillsSrc)
	}
}

func TestSymlinkSkillsDir_RelinksOnDifferentTarget(t *testing.T) {
	homeDir := t.TempDir()
	oldSrc := t.TempDir()
	newSrc := t.TempDir()

	if err := symlinkSkillsDir(homeDir, oldSrc); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := symlinkSkillsDir(homeDir, newSrc); err != nil {
		t.Fatalf("second call (different target): %v", err)
	}

	link := filepath.Join(homeDir, ".agents", "skills")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", link, err)
	}
	if target != newSrc {
		t.Errorf("symlink target = %q, want %q", target, newSrc)
	}
}

func TestSymlinkSkillsDir_ResolvesRelativePath(t *testing.T) {
	parent := t.TempDir()
	homeDir := filepath.Join(parent, "home")
	skillsSrc := filepath.Join(parent, "skills")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
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

	if err := symlinkSkillsDir(homeDir, relPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	link := filepath.Join(homeDir, ".agents", "skills")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", link, err)
	}
	if !filepath.IsAbs(target) {
		t.Errorf("symlink target should be absolute, got %q", target)
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

func TestFetchAndWriteAnalysis(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/applications/42/analysis/insights" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id": 1, "description": "sample insight"}]`))
	}))
	defer server.Close()

	workDir := t.TempDir()
	hubClient := hub.NewClient(server.URL, "token")
	err := fetchAndWriteAnalysis(hubClient, "42", workDir)
	if err != nil {
		t.Fatalf("fetchAndWriteAnalysis failed: %v", err)
	}

	analysisFile := filepath.Join(workDir, ".konveyor", "analysis.json")
	data, err := os.ReadFile(analysisFile)
	if err != nil {
		t.Fatalf("reading analysis.json failed: %v", err)
	}
	if !strings.Contains(string(data), "sample insight") {
		t.Errorf("unexpected content: %s", string(data))
	}
}
