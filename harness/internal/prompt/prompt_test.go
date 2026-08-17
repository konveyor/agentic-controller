package prompt

import (
	"strings"
	"testing"
)

func fullLayers() Layers {
	return Layers{
		AgentPrompt:   "AGENT PROMPT",
		WorkflowGuide: "WORKFLOW GUIDE",
		StageTask:     "STAGE TASK",
	}
}

func TestBuildPutsStagingRulesFirst(t *testing.T) {
	got := Build(fullLayers())

	if !strings.Contains(got, "Write ephemeral files to /tmp") {
		t.Fatalf("staging rules missing from prompt:\n%s", got)
	}

	rules := strings.Index(got, "Working Environment")
	for _, later := range []string{"AGENT PROMPT", "WORKFLOW GUIDE", "STAGE TASK"} {
		if strings.Index(got, later) < rules {
			t.Errorf("%q appears before the staging rules; rules must come first", later)
		}
	}
}

func TestBuildOrdersLayersLeastToMostSpecific(t *testing.T) {
	got := Build(fullLayers())

	order := []string{"AGENT PROMPT", "WORKFLOW GUIDE", "STAGE TASK"}
	for i := 1; i < len(order); i++ {
		if strings.Index(got, order[i]) < strings.Index(got, order[i-1]) {
			t.Errorf("%q should come after %q", order[i], order[i-1])
		}
	}
}

func TestBuildOmitsEmptyLayers(t *testing.T) {
	got := Build(Layers{})

	for _, header := range []string{"## Workflow Guide", "## Stage Task"} {
		if strings.Contains(got, header) {
			t.Errorf("empty layer produced %q header", header)
		}
	}
	if !strings.Contains(got, "## Working Guidelines") {
		t.Error("working guidelines should always be included")
	}
}

func TestBuildIncludesCommitGuideline(t *testing.T) {
	got := Build(Layers{AgentPrompt: "AGENT PROMPT"})

	if !strings.Contains(got, "## Working Guidelines") {
		t.Fatalf("working guidelines missing:\n%s", got)
	}
	if !strings.Contains(got, "Commit your changes to git") {
		t.Error("working guidelines should tell the agent to commit")
	}
}

func TestBuildEndsWithExactlyOneNewline(t *testing.T) {
	cases := map[string]Layers{
		"with stage task":    fullLayers(),
		"without stage task": {},
	}
	for name, layers := range cases {
		t.Run(name, func(t *testing.T) {
			got := Build(layers)
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("prompt does not end with a newline: %q", tail(got))
			}
			if strings.HasSuffix(got, "\n\n") {
				t.Errorf("prompt ends with more than one newline: %q", tail(got))
			}
		})
	}
}

func tail(s string) string {
	if len(s) < 20 {
		return s
	}
	return s[len(s)-20:]
}
