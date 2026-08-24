// Package prompt assembles the prompt sent to the agent for a stage.
//
// The prompt is layered: environment rules the harness imposes, then the
// Agent's standing prompt, then the workflow guide, then the resolved
// parameter values, then the skill, then the stage task. Later layers are
// more specific; the environment rules come first because they constrain
// everything after them.
package prompt

import (
	"strings"

	"github.com/konveyor/agentic-controller/api/skill"
)

// stagingRules apply to every agent and skill. The working directory is the git
// worktree whose commits the harness pushes on exit, so ephemeral files left
// there are ambiguous at best, and get swept in if the agent stages broadly.
const stagingRules = `## Working Environment

Your working directory is a git repository. Anything you commit is pushed to the
user's branch when the stage ends.

Write ephemeral files to /tmp, never into the repository working tree:
  - scripts you need to run: write them to /tmp, make them executable there,
    and run them from there
  - scratch notes, plans, logs, and intermediate output

Only files the user actually asked for belong in the repository. Keep the
worktree clean: anything left there is ambiguous, and gets swept into your
commits if you stage broadly. This applies even when a skill's instructions do
not say where to put something.
`

// Rule is one always-loaded skill, injected into every prompt. An alias, not a
// copy: it is what skill.RuleContent hands back, and the harness already
// imports that package to read the loader's manifest.
type Rule = skill.Rule

// Layers are the context layers composed into a stage prompt, ordered from
// least to most specific.
type Layers struct {
	// AgentPrompt is the Agent's standing prompt.
	AgentPrompt string
	// Rules are the always-loaded skills, in the order the loader assembled
	// them. On-demand skills are not here; the runtime discovers those and the
	// agent reads them if it judges them relevant (ADR 0014).
	Rules []Rule
	// WorkflowGuide is the workflow's ambient guide.
	WorkflowGuide string
	// Parameters is the pre-rendered body of the "## Parameters" section
	// (workflow and agent values as text, per ADR 0009). Empty when there
	// are no parameter values to show.
	Parameters string
	// StageTask is the task for this stage.
	StageTask string
	// AskUser: the session mounts the harness's ask_user tool, so the
	// guidelines tell the model to use it (and not to ask in prose).
	AskUser bool
}

// askUserGuideline is appended to the working guidelines when the ask_user
// tool is mounted. It has to pre-empt the model's habit of asking in prose:
// nothing reads an assistant message until the run is over, and a turn that
// ends on a question is a finished run.
const askUserGuideline = "A human may be watching this run. When you need a decision only a human can make — " +
	"a missing prerequisite, an ambiguous requirement, a choice between approaches, anything destructive — " +
	"call the `ask_user` tool and wait for its answer. Do not ask questions in prose: nobody reads your " +
	"messages until the run is over, and ending your turn on a question ends the run. " +
	"A human may also redirect you mid-turn; if a redirect asks you to pause, check in, " +
	"report status, or wait, call `ask_user` with your status and the choices you see, and " +
	"do not continue until it answers. " +
	"If `ask_user` reports that nobody answered, say what you needed and stop.\n\n"

// Build composes the stage prompt. The harness's environment rules always come
// first, so a skill cannot be read as overriding them.
func Build(l Layers) string {
	var b strings.Builder

	b.WriteString(stagingRules)
	b.WriteString("\n")

	if l.AgentPrompt != "" {
		b.WriteString(l.AgentPrompt)
		b.WriteString("\n\n")
	}

	// After the environment rules and before anything task-specific, so a
	// later layer cannot read as relaxing them.
	if len(l.Rules) > 0 {
		b.WriteString("## Rules\n\n")
		b.WriteString("These always apply. They are not optional and not task-specific.\n\n")
		for _, r := range l.Rules {
			b.WriteString("### " + r.Name + "\n\n")
			b.WriteString(strings.TrimRight(r.Body, "\n"))
			b.WriteString("\n\n")
		}
	}

	if l.WorkflowGuide != "" {
		b.WriteString("## Workflow Guide\n\n")
		b.WriteString(l.WorkflowGuide)
		b.WriteString("\n\n")
	}

	if l.Parameters != "" {
		b.WriteString("## Parameters\n\n")
		b.WriteString(l.Parameters)
		b.WriteString("\n\n")
	}

	b.WriteString("## Working Guidelines\n\n")
	b.WriteString("Commit your changes to git with a descriptive message when your work is complete.\n\n")
	if l.AskUser {
		b.WriteString(askUserGuideline)
	}

	if l.StageTask != "" {
		b.WriteString("## Stage Task\n\n")
		b.WriteString(l.StageTask)
	}

	// Normalise the ending: sections above end with either "\n\n" or, for
	// StageTask, no newline at all. Always finish with exactly one.
	return strings.TrimRight(b.String(), "\n") + "\n"
}
