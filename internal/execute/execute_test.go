package execute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExistingResult(t *testing.T) {
	dir := t.TempDir()

	t.Run("no file returns nil", func(t *testing.T) {
		r := loadExistingResult(filepath.Join(dir, "nonexistent.json"))
		if r != nil {
			t.Error("expected nil for nonexistent file")
		}
	})

	t.Run("valid file returns result", func(t *testing.T) {
		result := ItemResult{N: 1, Path: "pom.xml", Status: "ok"}
		data, _ := json.Marshal(result)
		path := filepath.Join(dir, "item-001.json")
		os.WriteFile(path, data, 0644)

		r := loadExistingResult(path)
		if r == nil {
			t.Fatal("expected non-nil result")
		}
		if r.Status != "ok" {
			t.Errorf("status = %q, want ok", r.Status)
		}
	})
}

func TestWriteItemResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item-001.json")
	r := &ItemResult{N: 1, Path: "test.java", Status: "ok", Lesson: "javax -> jakarta"}
	writeItemResult(path, r)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var loaded ItemResult
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if loaded.Lesson != "javax -> jakarta" {
		t.Errorf("lesson = %q", loaded.Lesson)
	}
}

func TestInitExecutionLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "execution-log.md")
	initExecutionLog(path, "java-ee-to-quarkus")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Execution Log") {
		t.Error("missing header")
	}
	if !strings.Contains(content, "java-ee-to-quarkus") {
		t.Error("missing migration type")
	}
}

func TestAppendExecutionLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "execution-log.md")
	os.WriteFile(path, []byte("# Log\n\n"), 0644)

	r := &ItemResult{
		N:            1,
		Path:         "pom.xml",
		Action:       "migrate",
		Status:       "ok",
		Lesson:       "updated deps",
		FilesTouched: []string{"pom.xml"},
	}
	appendExecutionLog(path, r)

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "Step #1") {
		t.Error("missing step header")
	}
	if !strings.Contains(content, "updated deps") {
		t.Error("missing lesson")
	}
	if !strings.Contains(content, "pom.xml") {
		t.Error("missing file touched")
	}
}

func TestSummaryJSON(t *testing.T) {
	s := Summary{Ok: 10, Failed: 1, Skipped: 2}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var loaded Summary
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if loaded.Ok != 10 || loaded.Failed != 1 || loaded.Skipped != 2 {
		t.Errorf("summary = %+v", loaded)
	}
}
