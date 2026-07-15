package goose

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func recipesDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "recipes")
}

func TestParseRecipe(t *testing.T) {
	for _, name := range []string{"execute.yaml", "verify.yaml", "fix.yaml"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(recipesDir(), name)
			r, err := ParseRecipe(path)
			if err != nil {
				t.Fatalf("ParseRecipe(%s): %v", name, err)
			}
			if r.Title == "" {
				t.Error("expected non-empty title")
			}
			if len(r.Parameters) == 0 {
				t.Error("expected at least one parameter")
			}
			if r.Instruction == "" {
				t.Error("expected non-empty instructions")
			}
			if r.Prompt == "" {
				t.Error("expected non-empty prompt")
			}
			t.Logf("%s: %d params, %d extensions", r.Title, len(r.Parameters), len(r.Extensions))
		})
	}
}

func TestRenderRecipe(t *testing.T) {
	path := filepath.Join(recipesDir(), "fix.yaml")
	r, err := ParseRecipe(path)
	if err != nil {
		t.Fatalf("ParseRecipe: %v", err)
	}

	params := map[string]string{
		"repo":                     "/workspace/coolstore",
		"verification_report_path": "/workspace/coolstore/verification-report.md",
		"migration_type":           "java-ee-to-quarkus",
		"error_file":               "src/main/java/com/redhat/coolstore/service/OrderService.java",
		"error_message":            "cannot find symbol: class EntityManager",
	}

	instructions, prompt, err := r.Render(params)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if instructions == "" || prompt == "" {
		t.Fatal("expected non-empty rendered output")
	}

	// Verify templates were substituted
	if strings.Contains(instructions, "{{ repo }}") || strings.Contains(instructions, "{{repo}}") {
		t.Error("unsubstituted {{ repo }} in instructions")
	}
	if !strings.Contains(instructions, "/workspace/coolstore") {
		t.Error("expected rendered repo path in instructions")
	}
	if !strings.Contains(prompt, "cannot find symbol") {
		t.Error("expected error message in prompt")
	}

	t.Logf("Instructions length: %d bytes", len(instructions))
	t.Logf("Prompt length: %d bytes", len(prompt))
}

func TestRenderMissingParam(t *testing.T) {
	path := filepath.Join(recipesDir(), "fix.yaml")
	r, err := ParseRecipe(path)
	if err != nil {
		t.Fatalf("ParseRecipe: %v", err)
	}

	// Missing error_file and error_message
	params := map[string]string{
		"repo":                     "/workspace/coolstore",
		"verification_report_path": "/run/verify.md",
		"migration_type":           "java-ee-to-quarkus",
	}

	_, _, err = r.Render(params)
	if err == nil {
		t.Fatal("expected error for missing params")
	}
	t.Logf("Got expected error: %v", err)
}

func TestBuildACPPrompt(t *testing.T) {
	path := filepath.Join(recipesDir(), "fix.yaml")
	r, err := ParseRecipe(path)
	if err != nil {
		t.Fatalf("ParseRecipe: %v", err)
	}

	full, err := r.BuildACPPrompt("do this", "now please")
	if err != nil {
		t.Fatalf("BuildACPPrompt: %v", err)
	}
	if !strings.Contains(full, "do this") || !strings.Contains(full, "now please") {
		t.Error("expected instructions and prompt in output")
	}
	if !strings.Contains(full, "fixed") || !strings.Contains(full, "boolean") {
		t.Errorf("expected JSON schema with 'fixed' and 'boolean' in output, got: %s", full[len(full)-200:])
	}
	t.Logf("Full ACP prompt: %d bytes", len(full))
}
