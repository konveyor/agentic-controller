// Package prompt assembles the prompt sent to the agent for a stage.
//
// The prompt is layered: environment rules the harness imposes, then the
// Agent's standing prompt, then playbook context, then the skill, then the
// stage task. Later layers are more specific; the environment rules come first
// because they constrain everything after them.
package prompt

import "strings"

// stagingRules apply to every agent and skill. The working directory is the git
// worktree whose commits the harness pushes on exit, so ephemeral files left
// there are ambiguous at best, and get swept in if the agent stages broadly.
const stagingRules = `## Working Environment

Your working directory is a git repository, and you decide what gets committed.
The harness pushes your commits to the user's branch when the stage ends.

Write ephemeral files to /tmp, never into the repository working tree:
  - scripts you need to run: write them to /tmp, make them executable there,
    and run them from there
  - scratch notes, plans, logs, and intermediate output

Only files the user actually asked for belong in the repository. Keep the
worktree clean: anything left there is ambiguous, and gets swept into your
commits if you stage broadly. This applies even when a skill's instructions do
not say where to put something.
`

// Layers are the context layers composed into a stage prompt, ordered from
// least to most specific. Any of them may be empty except Skill.
type Layers struct {
	// Agent is the Agent's standing prompt.
	Agent string
	// PlaybookContext is the playbook's ambient guide.
	PlaybookContext string
	// Skill is the content discovered from the mounted SkillCards.
	Skill string
	// StageTask is the task for this stage.
	StageTask string
}

// Build composes the stage prompt. The harness's environment rules always come
// first, so a skill cannot be read as overriding them.
func Build(l Layers) string {
	var b strings.Builder

	b.WriteString(stagingRules)
	b.WriteString("\n")

	if l.Agent != "" {
		b.WriteString(l.Agent)
		b.WriteString("\n\n")
	}

	if l.PlaybookContext != "" {
		b.WriteString("## Migration Context\n\n")
		b.WriteString(l.PlaybookContext)
		b.WriteString("\n\n")
	}

	// Skills are optional (#82): with none mounted the agent still needs to be
	// told to commit, since nothing else in the prompt says so.
	if l.Skill != "" {
		b.WriteString("## Skill Instructions\n\n")
		b.WriteString(l.Skill)
		b.WriteString("\n\n")
	} else {
		b.WriteString("## Working Guidelines\n\n")
		b.WriteString("Commit your changes to git with a descriptive message when your work is complete.\n\n")
	}

	if l.StageTask != "" {
		b.WriteString("## Stage Task\n\n")
		b.WriteString(l.StageTask)
	}

	return b.String()
}
