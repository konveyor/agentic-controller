package fixloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konveyor/migration-harness/internal/verify"
)

func TestLoadVerifyResult(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file returns nil", func(t *testing.T) {
		r := loadVerifyResult(filepath.Join(dir, "nope.json"))
		if r != nil {
			t.Error("expected nil")
		}
	})

	t.Run("valid file", func(t *testing.T) {
		vr := verify.VerifyResult{BuildOk: true, TestsPassed: 5, TestsTotal: 5}
		data, _ := json.Marshal(vr)
		path := filepath.Join(dir, "verify.json")
		os.WriteFile(path, data, 0644)

		r := loadVerifyResult(path)
		if r == nil {
			t.Fatal("expected non-nil")
		}
		if !r.BuildOk {
			t.Error("expected build_ok = true")
		}
	})
}

func TestWriteFixReport(t *testing.T) {
	runDir := t.TempDir()
	repoDir := t.TempDir()

	report := &FixLoopReport{
		MigrationType: "java-ee-to-quarkus",
		Iterations:    2,
		Status:        "success",
	}
	writeFixReport(runDir, repoDir, report, "java-ee-to-quarkus")

	data, err := os.ReadFile(filepath.Join(runDir, "fix-loop-report.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "Fix Loop Report") {
		t.Error("missing header")
	}
	if !strings.Contains(content, "success") {
		t.Error("missing success status")
	}
	if !strings.Contains(content, "**Iterations:** 2") {
		t.Error("missing iteration count")
	}

	if _, err := os.Stat(filepath.Join(repoDir, "fix-loop-report.md")); err != nil {
		t.Error("report not copied to repo dir")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 100); got != "short" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("abcdefghij", 5); got != "abcde..." {
		t.Errorf("truncate long = %q", got)
	}
}
