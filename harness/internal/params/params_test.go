package params

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeParamsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "params.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write params file: %v", err)
	}
	return path
}

func TestLoadMissingFileReturnsZeroValue(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Workflow) != 0 || len(f.Agent) != 0 || f.Execution != (Execution{}) {
		t.Errorf("expected zero-value File, got %+v", f)
	}
}

func TestLoadMalformedJSONErrors(t *testing.T) {
	path := writeParamsFile(t, "{not json")

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestLoadParsesThreeSections(t *testing.T) {
	path := writeParamsFile(t, `{
		"workflow": {"application_name": "coolstore"},
		"agent": {"source_url": "https://github.com/example/app", "dry_run": true},
		"execution": {"mode": "auto", "maxTurns": 200}
	}`)

	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Workflow["application_name"] != "coolstore" {
		t.Errorf("Workflow[application_name] = %v", f.Workflow["application_name"])
	}
	if f.Agent["source_url"] != "https://github.com/example/app" {
		t.Errorf("Agent[source_url] = %v", f.Agent["source_url"])
	}
	if f.Agent["dry_run"] != true {
		t.Errorf("Agent[dry_run] = %v", f.Agent["dry_run"])
	}
	if f.Execution.Mode != "auto" {
		t.Errorf("Execution.Mode = %v", f.Execution.Mode)
	}
	if f.Execution.MaxTurns != 200 {
		t.Errorf("Execution.MaxTurns = %v", f.Execution.MaxTurns)
	}
}

func TestFileMaxTurns(t *testing.T) {
	cases := []struct {
		name      string
		execution Execution
		wantN     int
		wantOK    bool
	}{
		{"present and positive", Execution{MaxTurns: 500}, 500, true},
		{"absent", Execution{Mode: "auto"}, 0, false},
		{"zero", Execution{MaxTurns: 0}, 0, false},
		{"negative", Execution{MaxTurns: -1}, 0, false},
		{"zero-value execution", Execution{}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, ok := File{Execution: c.execution}.MaxTurns()
			if n != c.wantN || ok != c.wantOK {
				t.Errorf("MaxTurns() = (%d, %v), want (%d, %v)", n, ok, c.wantN, c.wantOK)
			}
		})
	}
}

func TestRenderSectionEmptyWhenNoValues(t *testing.T) {
	if got := RenderSection(File{}); got != "" {
		t.Errorf("RenderSection(empty) = %q, want empty string", got)
	}
}

func TestRenderSectionFormatsWorkflowAndAgent(t *testing.T) {
	f := File{
		Workflow: map[string]any{"application_name": "coolstore", "target_framework": "quarkus"},
		Agent:    map[string]any{"source_url": "https://github.com/example/app", "dry_run": true},
	}

	got := RenderSection(f)

	want := "### Workflow\n" +
		"- application_name: coolstore\n" +
		"- target_framework: quarkus\n" +
		"\n" +
		"### Agent\n" +
		"- dry_run: true\n" +
		"- source_url: https://github.com/example/app"
	if got != want {
		t.Errorf("RenderSection() =\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderSectionOmitsEmptySection(t *testing.T) {
	got := RenderSection(File{Agent: map[string]any{"foo": "bar"}})

	if strings.Contains(got, "### Workflow") {
		t.Errorf("expected no Workflow section, got:\n%s", got)
	}
	if !strings.Contains(got, "### Agent") {
		t.Errorf("expected Agent section, got:\n%s", got)
	}
}

func TestRenderSectionFormatsWholeNumbersWithoutDecimal(t *testing.T) {
	got := RenderSection(File{Agent: map[string]any{"max_fix_iterations": float64(5)}})

	if !strings.Contains(got, "- max_fix_iterations: 5\n") && !strings.HasSuffix(got, "- max_fix_iterations: 5") {
		t.Errorf("expected integer formatting, got:\n%s", got)
	}
	if strings.Contains(got, "5.") {
		t.Errorf("expected no decimal point for whole number, got:\n%s", got)
	}
}

func TestReserveFractionIsAFractionOfOne(t *testing.T) {
	if ReserveFraction <= 0 || ReserveFraction >= 1 {
		t.Fatalf("ReserveFraction = %v, want a value strictly between 0 and 1", ReserveFraction)
	}
}

func TestNativeTurnLimit(t *testing.T) {
	cases := []struct {
		maxTurns int
		want     int
	}{
		{maxTurns: 200, want: 170},
		{maxTurns: 3, want: 2},
		{maxTurns: 1, want: 1}, // clamped to at least 1
		{maxTurns: 0, want: 0}, // unset stays unset
		{maxTurns: -5, want: 0},
	}
	for _, c := range cases {
		if got := NativeTurnLimit(c.maxTurns); got != c.want {
			t.Errorf("NativeTurnLimit(%d) = %d, want %d", c.maxTurns, got, c.want)
		}
	}
}
