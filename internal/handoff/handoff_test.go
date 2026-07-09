package handoff

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konveyor/migration-harness/internal/execute"
	"github.com/konveyor/migration-harness/internal/plan"
	"github.com/konveyor/migration-harness/internal/verify"
)

func TestWriteSession(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("test-id", "Migrate to Quarkus", "https://github.com/org/repo.git",
		"konveyor-migrate", "gemini-2.5-pro", "gcp_vertex_ai")
	s.Status = "completed"
	s.Pipeline.Execute = &ExecuteStatus{
		StepStatus:     StepStatus{Status: "completed"},
		ItemsSucceeded: 10,
		ItemsFailed:    1,
	}

	err := WriteSession(dir, s)
	if err != nil {
		t.Fatalf("WriteSession: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".konveyor", "session.json"))
	if err != nil {
		t.Fatalf("read session.json: %v", err)
	}

	var loaded Session
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if loaded.SchemaVersion != "1.0" {
		t.Errorf("schema_version = %q", loaded.SchemaVersion)
	}
	if loaded.SessionID != "test-id" {
		t.Errorf("session_id = %q", loaded.SessionID)
	}
	if loaded.Status != "completed" {
		t.Errorf("status = %q", loaded.Status)
	}
	if loaded.Pipeline.Execute.ItemsSucceeded != 10 {
		t.Errorf("items_succeeded = %d", loaded.Pipeline.Execute.ItemsSucceeded)
	}
}

func TestWriteHandoffSuccess(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("id", "Migrate to Quarkus", "https://github.com/org/repo.git",
		"branch", "model", "provider")
	s.Status = "completed"

	p := &plan.Plan{
		MigrationType: "java-ee-to-quarkus",
		Items: []plan.PlanItem{
			{N: 1, Path: "pom.xml"},
			{N: 2, Path: "Foo.java"},
		},
	}
	items := []execute.ItemResult{
		{N: 1, Path: "pom.xml", Action: "migrate", Status: "ok"},
		{N: 2, Path: "Foo.java", Action: "migrate", Status: "ok"},
	}
	vr := &verify.VerifyResult{BuildOk: true, TestsPassed: 5, TestsTotal: 5}

	err := WriteHandoff(dir, s, p, items, vr)
	if err != nil {
		t.Fatalf("WriteHandoff: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".konveyor", "handoff.md"))
	if err != nil {
		t.Fatalf("read handoff.md: %v", err)
	}
	content := string(data)

	checks := []string{
		"Migration Handoff",
		"Migrate to Quarkus",
		"Completed",
		"2 of 2 items migrated",
		"Build passes",
		"pom.xml",
	}
	for _, c := range checks {
		if !strings.Contains(content, c) {
			t.Errorf("missing %q in handoff.md", c)
		}
	}
}

func TestWriteHandoffPartial(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("id", "Migrate", "repo", "branch", "model", "provider")
	s.Status = "partial"

	p := &plan.Plan{Items: []plan.PlanItem{
		{N: 1, Path: "a.java"},
		{N: 2, Path: "b.java"},
	}}
	items := []execute.ItemResult{
		{N: 1, Path: "a.java", Action: "migrate", Status: "ok"},
		{N: 2, Path: "b.java", Action: "migrate", Status: "failed", ErrorLog: "compilation error"},
	}

	err := WriteHandoff(dir, s, p, items, nil)
	if err != nil {
		t.Fatalf("WriteHandoff: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".konveyor", "handoff.md"))
	content := string(data)

	if !strings.Contains(content, "1 of 2 items migrated") {
		t.Error("missing partial count")
	}
	if !strings.Contains(content, "Manual Attention") {
		t.Error("missing manual attention section")
	}
	if !strings.Contains(content, "compilation error") {
		t.Error("missing error in manual attention")
	}
}

func TestCountResults(t *testing.T) {
	items := []execute.ItemResult{
		{Status: "ok"},
		{Status: "ok"},
		{Status: "failed"},
		{Status: "skipped"},
	}
	ok, failed, skipped := countResults(items)
	if ok != 2 {
		t.Errorf("ok = %d, want 2", ok)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
}
