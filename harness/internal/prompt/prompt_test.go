package prompt

import (
	"strings"
	"testing"
)

func TestBuildPutsStagingRulesFirst(t *testing.T) {
	got := Build(Layers{
		Agent:           "AGENT PROMPT",
		PlaybookContext: "PLAYBOOK CONTEXT",
		Skill:           "SKILL BODY",
		StageTask:       "STAGE TASK",
	})

	if !strings.Contains(got, "Write ephemeral files to /tmp") {
		t.Fatalf("staging rules missing from prompt:\n%s", got)
	}

	// Order matters as much as presence: the rules have to precede the skill,
	// so a skill that says where to write cannot read as overriding them.
	rules := strings.Index(got, "Working Environment")
	for _, later := range []string{"AGENT PROMPT", "PLAYBOOK CONTEXT", "SKILL BODY", "STAGE TASK"} {
		if strings.Index(got, later) < rules {
			t.Errorf("%q appears before the staging rules; rules must come first", later)
		}
	}
}

func TestBuildOrdersLayersLeastToMostSpecific(t *testing.T) {
	got := Build(Layers{
		Agent:           "AGENT PROMPT",
		PlaybookContext: "PLAYBOOK CONTEXT",
		Skill:           "SKILL BODY",
		StageTask:       "STAGE TASK",
	})

	order := []string{"AGENT PROMPT", "PLAYBOOK CONTEXT", "SKILL BODY", "STAGE TASK"}
	for i := 1; i < len(order); i++ {
		if strings.Index(got, order[i]) < strings.Index(got, order[i-1]) {
			t.Errorf("%q should come after %q", order[i], order[i-1])
		}
	}
}

func TestBuildOmitsEmptyLayers(t *testing.T) {
	got := Build(Layers{Skill: "SKILL BODY"})

	for _, header := range []string{"## Migration Context", "## Stage Task"} {
		if strings.Contains(got, header) {
			t.Errorf("empty layer produced %q header", header)
		}
	}
	if !strings.Contains(got, "## Skill Instructions") {
		t.Error("skill content should always be included")
	}
}

// Skills are optional (#82). With none mounted the agent still needs to be told
// to commit, since nothing else in the prompt says so.
func TestBuildFallsBackWhenNoSkills(t *testing.T) {
	got := Build(Layers{Agent: "AGENT PROMPT"})

	if strings.Contains(got, "## Skill Instructions") {
		t.Error("empty skill should not produce a Skill Instructions header")
	}
	if !strings.Contains(got, "## Working Guidelines") {
		t.Fatalf("no skills should fall back to Working Guidelines:\n%s", got)
	}
	if !strings.Contains(got, "Commit your changes to git") {
		t.Error("fallback should tell the agent to commit")
	}
}
