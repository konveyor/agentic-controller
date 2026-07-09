package verify

import (
	"os"
	"strings"
	"testing"
)

func TestWriteVerifyReport(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/report.md"

	r := &VerifyResult{
		BuildOk:     true,
		TestsPassed: 42,
		TestsTotal:  42,
		Errors:      nil,
		Summary:     "All tests pass",
	}
	writeVerifyReport(path, r, "java-ee-to-quarkus")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)

	checks := []string{
		"Verification Report",
		"SUCCESS",
		"42/42",
		"All tests pass",
	}
	for _, c := range checks {
		if !strings.Contains(content, c) {
			t.Errorf("missing %q in report", c)
		}
	}
}

func TestWriteVerifyReportWithErrors(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/report.md"

	r := &VerifyResult{
		BuildOk:     false,
		TestsPassed: 10,
		TestsTotal:  15,
		Errors: []VerifyError{
			{File: "Foo.java", Line: 42, Message: "cannot find symbol"},
		},
		FixAttempts:  2,
		FixesApplied: "fixed import",
	}
	writeVerifyReport(path, r, "java-ee-to-quarkus")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "FAILED") {
		t.Error("missing FAILED status")
	}
	if !strings.Contains(content, "Foo.java:42") {
		t.Error("missing error location")
	}
	if !strings.Contains(content, "cannot find symbol") {
		t.Error("missing error message")
	}
	if !strings.Contains(content, "Fix iterations: 2") {
		t.Error("missing fix attempts")
	}
}
