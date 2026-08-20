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

// A rule's content has to actually reach the prompt: that is the whole point
// of the type. If it does not, the rule is silently not applied (ADR 0014).
func TestBuildIncludesRuleBodies(t *testing.T) {
	got := Build(Layers{
		AgentPrompt: "agent",
		Rules: []Rule{
			{Name: "house-style", Body: "Never edit generated files.\n"},
			{Name: "commit-policy", Body: "One commit per stage."},
		},
		StageTask: "do the thing",
	})

	for _, want := range []string{
		"house-style", "Never edit generated files.",
		"commit-policy", "One commit per stage.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q:\n%s", want, got)
		}
	}
}

// Order carries meaning here. The harness's own environment rules bind first,
// then the skills' rules, and only then anything task-specific -- so a later
// layer cannot read as relaxing an earlier one.
func TestBuildPlacesRulesAfterStagingAndBeforeTask(t *testing.T) {
	got := Build(Layers{
		AgentPrompt:   "agent prompt",
		Rules:         []Rule{{Name: "house-style", Body: "rule body"}},
		WorkflowGuide: "workflow guide",
		StageTask:     "stage task",
	})

	staging := strings.Index(got, "Working Environment")
	rule := strings.Index(got, "rule body")
	guide := strings.Index(got, "workflow guide")
	task := strings.Index(got, "stage task")

	if !(staging < rule && rule < guide && guide < task) {
		t.Errorf("layers are out of order: staging=%d rule=%d guide=%d task=%d\n%s",
			staging, rule, guide, task, got)
	}
}

// No rules means no empty heading: a run with only on-demand skills should not
// be told it has rules to follow.
func TestBuildOmitsTheRulesSectionWhenThereAreNone(t *testing.T) {
	got := Build(Layers{AgentPrompt: "agent", StageTask: "task"})
	if strings.Contains(got, "## Rules") {
		t.Errorf("empty rules section rendered:\n%s", got)
	}
}
