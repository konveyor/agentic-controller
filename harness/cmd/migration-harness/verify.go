package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/konveyor/migration-harness/internal/logging"
)

// planPath is the migration plan the plan skill writes and the execute and
// verify skills read (catalog/skills/plan). Its `## Verification` section is
// the only part the harness reads, and it reads it for one reason: to run
// those commands itself instead of believing the handoff's account of them
// (#300).
//
// handoff.go reads what the stage SAYS about its work; this file measures it.
// A stage whose last green command was a test run wrote `- Status: passed` on
// a tree that did not compile, and three of its four claims were true — the
// false one was the one that mattered. Nothing a prompt can say rules that
// out, because the agent is the thing being checked.
const planPath = "docs/plan.md"

// Overall verdict of the harness's own verification pass.
const (
	// verificationPassed: every command the plan named ran and exited 0.
	verificationPassed = "passed"
	// verificationFailed: at least one command exited non-zero, timed out, or
	// could not be started.
	verificationFailed = "failed"
	// verificationNotRun: the harness ran nothing. Deliberately NOT an absence —
	// no plan, no Verification section, no runnable command and a disabled
	// pass all land here with a reason, because silence that renders as
	// green is the failure mode this whole file exists to close.
	verificationNotRun = "notRun"
)

// Per-command results.
const (
	verifyStepPassed   = "passed"
	verifyStepFailed   = "failed"
	verifyStepTimedOut = "timedOut"
	// verifyStepSkipped: the line is not a command the harness can run — a
	// Blackbox prose step, an unfilled template placeholder, an explicit
	// "none". Recorded rather than dropped, so the record says which parts
	// of the plan's verification were measured and which were not.
	verifyStepSkipped = "skipped"
	// verifyStepError: the command could not be started at all.
	verifyStepError = "error"
	// verifyStepAborted: the run was cancelled (SIGTERM, deadline) before
	// or during this command.
	verifyStepAborted = "aborted"
)

// verification is the verification section of the termination blob: what the
// harness ran, and what each command actually exited with. Independent of
// the handoff by construction — nothing here is read from anything the agent
// wrote except the command strings themselves.
type verification struct {
	Status string `json:"status"`
	// Reason says why, for a Status the result alone does not explain:
	// why nothing ran, or which command failed.
	Reason   string             `json:"reason,omitempty"`
	Commands []verificationStep `json:"commands,omitempty"`
}

// verificationStep is one line of the plan's Verification section and what
// came of it.
type verificationStep struct {
	// Label is the plan's own label for the line: Build, Test, Blackbox.
	Label   string `json:"label"`
	Command string `json:"command,omitempty"`
	Status  string `json:"status"`
	// ExitCode is a pointer so a passing command records an explicit 0
	// rather than having it omitted as a zero value — "exitCode absent"
	// and "exited 0" must not look the same.
	ExitCode *int    `json:"exitCode,omitempty"`
	Seconds  float64 `json:"seconds,omitempty"`
	// Reason explains a status the exit code does not: why a line was
	// skipped, why a command could not start.
	Reason string `json:"reason,omitempty"`
	// Output is a short tail of the command's combined output, carried
	// only for a command that did not pass. The full tail goes to the pod
	// log; this is the part that fits in a 4096-byte termination message.
	Output string `json:"output,omitempty"`
}

// failed reports whether the pass measured a failure. notRun is not a
// failure — it is the absence of a measurement, and the two must stay
// distinguishable all the way out to the termination blob.
func (v *verification) failed() bool { return v != nil && v.Status == verificationFailed }

// withoutOutput is the first thing the termination blob sheds when it does
// not fit: the output excerpts, whose full text is in the pod log anyway.
// Copies rather than editing in place, so the caller's record — the one the
// run's own logging already reported — is left intact.
func (v *verification) withoutOutput() *verification {
	if v == nil {
		return nil
	}
	trimmed := *v
	trimmed.Commands = make([]verificationStep, len(v.Commands))
	copy(trimmed.Commands, v.Commands)
	for i := range trimmed.Commands {
		trimmed.Commands[i].Output = ""
	}
	return &trimmed
}

// verdictOnly keeps the verdict and drops the per-command detail: the last
// thing that survives the blob's trim ladder, because a reader finding no
// verification at all cannot tell a passing run from an unchecked one.
func (v *verification) verdictOnly() *verification {
	if v == nil {
		return nil
	}
	return &verification{Status: v.Status, Reason: v.Reason}
}

const (
	// verifyOutputCapture is how much of a command's combined output is
	// held in memory: a tail, so a chatty build cannot grow the harness's
	// heap with megabytes of progress bars.
	verifyOutputCapture = 64 << 10
	// verifyOutputLog is how much of that tail reaches the pod log — the
	// durable copy, where a reviewer reads the actual compiler errors.
	verifyOutputLog = 4000
	// verifyOutputBlob is how much reaches the termination blob per failing
	// command. The kubelet truncates the whole message at 4096 bytes, so
	// this is a pointer to the log, not a substitute for it.
	verifyOutputBlob = 200
	// verifyCommandLen bounds the command text recorded in the blob.
	verifyCommandLen = 120
	// verifyReasonLen bounds a recorded reason.
	verifyReasonLen = 160
	// verifyMaxSteps caps how many Verification lines are recorded, so a plan
	// with a hundred of them cannot wedge the stage or flood the blob.
	verifyMaxSteps = 6
	// verifyCommandMaxLen is the longest value still treated as a command.
	// Past this it is prose — the Blackbox line's "if a README exists with
	// run instructions, …" is a paragraph, not something to hand a shell.
	verifyCommandMaxLen = 300
)

// DefaultVerifyTimeout bounds a single verification command. Long enough for
// a cold Maven or npm build in a fresh sandbox, short enough that a command
// waiting on stdin cannot hold the pod until its deadline.
const DefaultVerifyTimeout = 10 * time.Minute

var (
	// verificationHeading matches the plan's `## Verification` heading.
	verificationHeading = regexp.MustCompile(`(?i)^ {0,3}#{2,}\s+\**verification\**\s*:?\s*$`)
	// verificationItem matches one line of the section in the forms the
	// plan skill and models actually write: `- Build: mvn clean compile`,
	// `* **Test**: npm test`.
	verificationItem = regexp.MustCompile(`^\s*[-*]\s*\**\s*([A-Za-z][A-Za-z0-9 _/-]*?)\s*\**\s*:\s*(.*)$`)
	// sectionHeading matches a heading; its first submatch gives the level,
	// so a `###` subheading stays inside the section and a `##` ends it.
	sectionHeading = regexp.MustCompile(`^ {0,3}(#{1,6})\s+\S`)
)

// verifyProseLabels are the Verification labels that name manual steps
// rather than a command. The plan skill's Blackbox line is specified as
// "steps to start the app and verify key business flows still work" —
// English for a person, with no single exit code to record. The harness does
// not guess at it: it records the line as skipped and says why, which is
// honest, where inventing a command from prose would be the same unfounded
// claim of verification this change exists to remove.
var verifyProseLabels = map[string]bool{
	"blackbox": true,
	"manual":   true,
	"note":     true,
	"notes":    true,
	"steps":    true,
}

// verifyEmptyValues are values that name no command.
var verifyEmptyValues = map[string]bool{
	"": true, "none": true, "n/a": true, "na": true, "-": true,
	"—": true, "–": true, "skip": true, "skipped": true, "omit": true,
	"omitted": true, "tbd": true,
}

// planStep is one parsed line of the Verification section. Command is set
// only when the line names something to run; otherwise Skip says why not.
type planStep struct {
	Label   string
	Command string
	Skip    string
}

// runnable reports whether this line is a command the harness will run.
func (s planStep) runnable() bool { return s.Command != "" }

// runPlanVerification runs the commands in the plan's `## Verification`
// section against the tree as it stands and records each exit code. It never
// returns nil: a pass that could not happen is recorded as verificationNotRun with
// the reason, because the caller puts this straight into the termination
// blob and an absent verification section there would read as "fine".
//
// The commands run in cloneDir — the agent's own workspace, on the tree
// about to be pushed — through `sh -c`, with the harness process environment
// inherited. That is the same toolchain, HOME and Maven settings the agent's
// own shell had: the harness is the process that started the runtime in this
// container, so the runtime's environment is this process's environment, less
// the Hub credentials the harness cleared before the agent ever ran.
func runPlanVerification(ctx context.Context, cloneDir string, timeout time.Duration, red *redactor) *verification {
	if timeout <= 0 {
		timeout = DefaultVerifyTimeout
	}
	logging.Header("Verification")

	content, err := os.ReadFile(filepath.Join(cloneDir, planPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return notRun("%s not found — no verification commands to run", planPath)
		}
		return notRun("read %s: %v", planPath, red.redact(err.Error()))
	}
	steps, found := parsePlanVerification(string(content))
	if !found {
		return notRun("%s has no ## Verification section", planPath)
	}
	if len(steps) == 0 {
		return notRun("the ## Verification section of %s is empty", planPath)
	}

	v := &verification{Status: verificationPassed}
	ran := 0
	for _, step := range steps {
		if !step.runnable() {
			logging.Info("verification: %s skipped — %s", step.Label, step.Skip)
			v.Commands = append(v.Commands, verificationStep{
				Label:  step.Label,
				Status: verifyStepSkipped,
				Reason: clipVerify(red.redact(step.Skip), verifyReasonLen),
			})
			continue
		}
		if ctx.Err() != nil {
			logging.Warn("verification: %s not run — the run was cancelled", step.Label)
			v.Commands = append(v.Commands, verificationStep{
				Label:   step.Label,
				Command: clipVerify(red.redact(step.Command), verifyCommandLen),
				Status:  verifyStepAborted,
				Reason:  "run cancelled before this command",
			})
			continue
		}
		v.Commands = append(v.Commands, runVerifyStep(ctx, cloneDir, timeout, step, red))
		ran++
	}

	// Every line was prose, a placeholder or an explicit "none": the plan
	// named nothing the harness could measure. Not a pass.
	if ran == 0 {
		v.Status, v.Reason = verificationNotRun, "the ## Verification section names no command to run"
		logging.Warn("verification: %s", v.Reason)
		return v
	}
	for _, c := range v.Commands {
		switch c.Status {
		case verifyStepFailed, verifyStepTimedOut, verifyStepError:
			v.Status = verificationFailed
			if v.Reason == "" {
				v.Reason = fmt.Sprintf("%s %s: %s", c.Label, c.Status, c.Command)
			}
		}
	}
	if v.failed() {
		logging.Err("verification FAILED — %s", v.Reason)
		logging.Err("the pushed tree does not satisfy the plan's own verification commands, " +
			"whatever the handoff says about them")
	} else {
		logging.Ok("verification passed — %d command(s) from %s re-run by the harness", ran, planPath)
	}
	v.Reason = clipVerify(v.Reason, verifyReasonLen)
	return v
}

// runVerifyStep runs one command and records what it exited with.
func runVerifyStep(ctx context.Context, dir string, timeout time.Duration, step planStep, red *redactor) verificationStep {
	recorded := verificationStep{
		Label:   step.Label,
		Command: clipVerify(red.redact(step.Command), verifyCommandLen),
	}
	logging.Info("verification: running %s — %s (timeout %s)", step.Label, red.redact(step.Command), timeout)

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out := &tailBuffer{limit: verifyOutputCapture}
	cmd := exec.CommandContext(cmdCtx, "/bin/sh", "-c", step.Command)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	// A verification command is not interactive; without this a command
	// that reads stdin blocks until the timeout instead of seeing EOF.
	cmd.Stdin = nil
	cmd.Stdout = out
	cmd.Stderr = out
	// Kill the whole process group, and give the children a moment after
	// the signal: `sh -c "npm run build"` is a shell whose child does the
	// work, and killing only the shell leaves the build running in the pod
	// for as long as it likes.
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = 5 * time.Second

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	recorded.Seconds = elapsed.Round(time.Millisecond).Seconds()
	output := red.redact(out.String())

	var exitErr *exec.ExitError
	switch {
	case errors.Is(cmdCtx.Err(), context.DeadlineExceeded):
		recorded.Status = verifyStepTimedOut
		recorded.Reason = fmt.Sprintf("no exit after %s", timeout)
		logging.Err("verification: %s TIMED OUT after %s — %s", step.Label, timeout, recorded.Command)
	case ctx.Err() != nil:
		recorded.Status = verifyStepAborted
		recorded.Reason = "run cancelled during this command"
		logging.Warn("verification: %s aborted — the run was cancelled", step.Label)
	case errors.As(err, &exitErr):
		code := exitErr.ExitCode()
		recorded.Status, recorded.ExitCode = verifyStepFailed, &code
		logging.Err("verification: %s FAILED — exit %d after %s — %s", step.Label, code, elapsed.Round(time.Second), recorded.Command)
	case err != nil:
		recorded.Status = verifyStepError
		recorded.Reason = clipVerify(red.redact(err.Error()), verifyReasonLen)
		logging.Err("verification: %s could not run — %s", step.Label, recorded.Reason)
	default:
		code := 0
		recorded.Status, recorded.ExitCode = verifyStepPassed, &code
		logging.Ok("verification: %s passed — exit 0 after %s — %s", step.Label, elapsed.Round(time.Second), recorded.Command)
	}
	if recorded.Status != verifyStepPassed && output != "" {
		// The pod log is the durable copy and the only place the actual
		// compiler errors survive the pod; the blob gets a pointer to it.
		logging.Info("verification: %s output (last %d bytes):\n%s",
			step.Label, len(output), clipVerify(output, verifyOutputLog))
		recorded.Output = clipVerify(lastLines(output), verifyOutputBlob)
	}
	return recorded
}

// parsePlanVerification returns the plan's Verification lines, classified
// into commands to run and lines to skip. found is false when the document
// has no such section at all — distinct from a section with nothing in it.
//
// Fenced code blocks are dropped first (handoffProse), for the same reason
// the handoff reader drops them: a plan that quotes the plan template back
// inside a fence carries a second `## Verification` heading and a second set
// of `- Build:` lines, and reading those would have the harness run the
// template's own `<build command>` placeholder.
func parsePlanVerification(content string) (steps []planStep, found bool) {
	inSection := false
	for _, line := range handoffProse(content) {
		if m := sectionHeading.FindStringSubmatch(line); m != nil {
			switch {
			case verificationHeading.MatchString(line):
				// A second Verification heading restarts the section:
				// later wins, as it does for the handoff's verdict.
				inSection, found, steps = true, true, nil
			case len(m[1]) <= 2:
				inSection = false
			}
			continue
		}
		if !inSection || len(steps) >= verifyMaxSteps {
			continue
		}
		m := verificationItem.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		steps = append(steps, classifyVerifyItem(m[1], m[2]))
	}
	return steps, found
}

// classifyVerifyItem decides whether one Verification line is a command the
// harness runs, and when it is not, records why.
func classifyVerifyItem(label, value string) planStep {
	step := planStep{Label: strings.Join(strings.Fields(label), " ")}
	value = strings.TrimSpace(value)
	// `- Build: `mvn clean compile`` — the backticks are Markdown, not shell.
	if len(value) > 1 && strings.HasPrefix(value, "`") && strings.HasSuffix(value, "`") {
		value = strings.TrimSpace(strings.Trim(value, "`"))
	}
	switch {
	case verifyProseLabels[strings.ToLower(step.Label)]:
		step.Skip = "manual steps, not a command the harness can run or score"
	case verifyEmptyValues[strings.ToLower(value)]:
		step.Skip = "the plan names no command here"
	case strings.HasPrefix(value, "<"):
		// The plan skill's template is literally `<build command, e.g. mvn
		// clean compile>`; an agent that copied it left a placeholder.
		step.Skip = "unfilled placeholder in the plan, not a command"
	case len([]rune(value)) > verifyCommandMaxLen:
		step.Skip = "prose, not a command (too long to be one)"
	default:
		step.Command = value
	}
	return step
}

// notRun builds the recorded "nothing was measured" result. It carries a
// reason always: a verification that did not happen must never be mistaken
// for one that passed.
func notRun(format string, args ...any) *verification {
	reason := clipVerify(fmt.Sprintf(format, args...), verifyReasonLen)
	logging.Warn("verification not run: %s", reason)
	return &verification{Status: verificationNotRun, Reason: reason}
}

// clipVerify bounds s to n runes with an ellipsis.
func clipVerify(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n-1])) + "…"
	}
	return s
}

// lastLines returns the tail of output with blank lines dropped, so the
// short excerpt in the blob is the error rather than the trailing newlines
// a build writes after it.
func lastLines(output string) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, strings.TrimRight(line, "\r"))
		}
	}
	joined := strings.Join(kept, " | ")
	// Keep the END of the output: a compiler's summary line sits last.
	if r := []rune(joined); len(r) > verifyOutputBlob {
		return "…" + string(r[len(r)-verifyOutputBlob+1:])
	}
	return joined
}

// tailBuffer keeps the last limit bytes written to it and discards the rest,
// so a command that prints a gigabyte of progress output costs a fixed
// amount of memory.
type tailBuffer struct {
	limit int
	buf   []byte
	// dropped records that the head was discarded, so the excerpt is
	// marked as a tail rather than presented as the whole output.
	dropped bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > t.limit {
		p, t.dropped = p[len(p)-t.limit:], true
	}
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.limit; over > 0 {
		t.buf, t.dropped = t.buf[over:], true
	}
	return n, nil
}

func (t *tailBuffer) String() string {
	if t.dropped {
		return "…" + string(t.buf)
	}
	return string(t.buf)
}
