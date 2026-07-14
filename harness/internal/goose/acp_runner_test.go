package goose

import (
	"encoding/json"
	"testing"
)

func TestExtractStructuredOutput_DirectJSON(t *testing.T) {
	chunks := []string{`{"status": "ok", "files_touched": ["pom.xml"]}`}
	result, err := extractStructuredOutput(chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", parsed["status"])
	}
}

func TestExtractStructuredOutput_CodeBlock(t *testing.T) {
	chunks := []string{
		"Here's the result:\n\n```json\n",
		`{"fixed": true, "summary": "added import"}`,
		"\n```\n\nDone!",
	}
	result, err := extractStructuredOutput(chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["fixed"] != true {
		t.Errorf("expected fixed=true, got %v", parsed["fixed"])
	}
}

func TestExtractStructuredOutput_EmbeddedJSON(t *testing.T) {
	chunks := []string{
		"I've completed the migration. ",
		`The result is: {"build_ok": true, "tests_passed": 5, "tests_total": 5, "errors": [], "summary": "all good"}`,
		" Let me know if you need anything else.",
	}
	result, err := extractStructuredOutput(chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["build_ok"] != true {
		t.Errorf("expected build_ok=true, got %v", parsed["build_ok"])
	}
}

func TestExtractStructuredOutput_MultiChunkJSON(t *testing.T) {
	chunks := []string{
		`{"n": 1, `,
		`"path": "pom.xml", `,
		`"status": "ok", `,
		`"files_touched": ["pom.xml"], `,
		`"lesson": "", `,
		`"error_log": ""}`,
	}
	result, err := extractStructuredOutput(chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", parsed["status"])
	}
}

func TestExtractStructuredOutput_NoJSON(t *testing.T) {
	chunks := []string{"I completed the task successfully."}
	_, err := extractStructuredOutput(chunks)
	if err == nil {
		t.Error("expected error for non-JSON response")
	}
}

func TestExtractStructuredOutput_Empty(t *testing.T) {
	_, err := extractStructuredOutput(nil)
	if err == nil {
		t.Error("expected error for empty chunks")
	}
}
