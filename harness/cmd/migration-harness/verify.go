package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"

	"github.com/konveyor/migration-harness/internal/git"
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
	// verificationNotRun: there was nothing to measure. No plan, no
	// Verification section, a section the harness READ that names nothing
	// scorable (Blackbox only, an explicit `none`, an unfilled
	// placeholder), a disabled pass, a stage that never claimed success.
	// Deliberately NOT an absence: it carries a reason, because silence
	// that renders as green is the failure mode this whole file exists to
	// close. A section the harness could not read is not this — see
	// verificationIncomplete.
	verificationNotRun = "notRun"
	// verificationIncomplete: there WAS something to measure and the harness
	// could not finish measuring it — the run was cancelled part way, the
	// step cap left commands unrun, the tree checked was not the commit
	// being pushed, or the section's commands sat inside a fenced block that
	// is dropped before parsing, so the harness never read them (#336).
	// Distinct from notRun because the two differ in what a
	// consumer should do: nothing to verify is normal, a verification that
	// could not be completed is a gap. Neither is a pass, and under
	// enforcement this one fails the stage while notRun does not.
	verificationIncomplete = "incomplete"
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
	// why nothing ran, why it could not be completed, or which command
	// failed.
	Reason string `json:"reason,omitempty"`
	// Commit is the commit the result describes: the tip of the branch
	// being pushed, which the working tree was confirmed to match before
	// any command ran. Empty when nothing was measured. Without it the
	// record says a tree passed without saying which tree.
	Commit   string             `json:"commit,omitempty"`
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

// incomplete reports whether the pass started and could not finish.
func (v *verification) incomplete() bool { return v != nil && v.Status == verificationIncomplete }

// unmet reports whether the result is anything other than a measured pass
// over work there was something to measure: what enforcement acts on. A
// failure is obvious; an incomplete pass counts too, because under
// enforcement the stage must be verified and a partial measurement is not
// verification — six passing commands with a seventh left unrun is exactly
// the gap. notRun does not count: a run with no plan must not fail for
// having nothing to check.
func (v *verification) unmet() bool { return v.failed() || v.incomplete() }

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
	// Commit comes with it: a verdict that does not say which commit it
	// describes is most of the way back to the claim this change removes.
	return &verification{Status: v.Status, Reason: v.Reason, Commit: v.Commit}
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
	// verifyMaxSteps caps how many Verification lines are run, so a plan with
	// a hundred of them cannot wedge the stage or flood the blob. Set above
	// the handful a real plan lists (Build, Test, Lint, Typecheck, Format,
	// Blackbox is already six) because exceeding it now costs the run its
	// pass: a dropped line makes the verdict incomplete rather than being
	// quietly ignored.
	verifyMaxSteps = 12
	// verifyCommandMaxLen is the longest value still treated as a command.
	// Past this it is prose — the Blackbox line's "if a README exists with
	// run instructions, …" is a paragraph, not something to hand a shell.
	verifyCommandMaxLen = 300
)

// DefaultVerifyTimeout bounds a single verification command. Long enough for
// a cold Maven or npm build in a fresh sandbox, short enough that a command
// waiting on stdin cannot hold the pod until its deadline.
const DefaultVerifyTimeout = 10 * time.Minute

// DefaultVerifyTotalTimeout bounds the pass as a whole: enough for the
// handful of commands a real plan lists, each with a cold build's worth of
// time, and well short of verifyMaxSteps × DefaultVerifyTimeout — which is
// the two hours a plan with twelve slow commands would otherwise spend after
// the agent's turn, with the final push and the termination blob behind it.
const DefaultVerifyTotalTimeout = 30 * time.Minute

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
	// verifySentence matches an English sentence boundary in a value: a
	// letter, then `.`, `!` or `?`, then either a capitalised next word or
	// the end of the value. A shell command does not normally end a word
	// with a full stop — `go test ./...` and `find . -name x` put the dot
	// where no letter precedes it — while a sentence always does. See
	// verifyLooksLikeProse.
	verifySentence = regexp.MustCompile(`[A-Za-z][.!?]["')]?(\s+[A-Z]|$)`)
	// verifyQuoted matches a single- or double-quoted span, whose contents
	// are an argument the shell passes along rather than anything the
	// sentence test should read: `echo 'Build passed.'` is a command.
	verifyQuoted = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	// verifyShellOperator matches shell syntax that settles the question on
	// its own. No English sentence carries `&&`, `||` or a command
	// substitution, so a value holding one is shell whatever its
	// punctuation looks like.
	verifyShellOperator = regexp.MustCompile(`&&|\|\||\$\(`)
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
func runPlanVerification(ctx context.Context, repo *gogit.Repository, cloneDir, branch string, timeout, total time.Duration, red *redactor) *verification {
	if timeout <= 0 {
		timeout = DefaultVerifyTimeout
	}
	// timeout bounds one command; total bounds the pass. Without the
	// second, a plan naming verifyMaxSteps slow commands adds that many
	// timeouts between the watcher stopping and the final push — hours,
	// on a pod whose own deadline then kills the harness before it pushes
	// or writes the termination blob, which is a worse outcome than the
	// unverified green this file exists to prevent. A budget shorter than
	// one command's timeout is honoured as configured: it cuts the pass
	// short, and the commands it left unrun are recorded as a gap that
	// names the budget, so a mis-set one is loud rather than silent.
	if total <= 0 {
		total = DefaultVerifyTotalTimeout
	}
	logging.Header("Verification")

	content, err := os.ReadFile(filepath.Join(cloneDir, planPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return notRun("%s not found — no verification commands to run", planPath)
		}
		return notRun("read %s: %v", planPath, red.redact(err.Error()))
	}
	parsed := parsePlanVerification(string(content))
	if !parsed.Found {
		return notRun("%s has no ## Verification section", planPath)
	}
	if !parsed.runnable() {
		// Nothing to run. Whether that is "there was nothing to measure"
		// or "there was, and the harness could not read it" is the whole
		// difference between a verdict that never fails a stage and one
		// that does under enforcement, so the two cases are separated
		// here rather than collapsed into notRun.
		switch {
		case parsed.Truncated:
			// A section whose commands all sit past the cap DID name
			// commands to run, and the harness ran none of them: that is
			// a gap, not an absent measurement, and notRun here would be
			// the step cap quietly passing the stage again — the thing
			// truncatedGap exists to stop, one branch earlier than it was
			// wired in.
			return incomplete(red, "%s", truncatedGap(parsed.Truncated))
		case parsed.Fence != "":
			// The #336 case. A real plan wrote its six commands inside a
			// ```bash block with prose around them; the fence is dropped
			// before parsing (deliberately — see parsePlanVerification),
			// so the section parsed as naming nothing, and notRun let the
			// stage keep its green on a tree that did not compile. The
			// harness cannot run those commands and will not guess at
			// them, but it must not call a section it could not read an
			// absent one.
			logging.Err("verification: write the plan's Verification commands as `- Label: command` list "+
				"items, not inside a %s block — the harness does not read fenced blocks", parsed.Fence)
			return incomplete(red, "the ## Verification section of %s names no command outside a fenced %s "+
				"block, which the harness does not read — nothing was measured", planPath, parsed.Fence)
		case len(parsed.Steps) == 0:
			return notRun("the ## Verification section of %s is empty", planPath)
		}
		// Read, and genuinely nothing to score: a Blackbox-only section,
		// an explicit `none`, an unfilled placeholder.
		return notRun("the ## Verification section of %s names no command to run", planPath)
	}

	// There is work to measure, so from here on anything short of
	// measuring all of it is incomplete, never notRun and never a pass.
	target, mismatch := verifyTarget(repo, branch)
	if mismatch != "" {
		return incomplete(red, "%s", mismatch)
	}

	// Measured before the commands run, so artefacts this pass creates
	// cannot be mistaken for the agent's uncommitted work.
	gap, err := worktreeGap(repo, target)
	if err != nil {
		return incomplete(red, "inspect the working tree: %v", err)
	}
	if gap != "" {
		logging.Warn("verification: %s", gap)
	}

	// The pass gets its own deadline, separate from each command's. A
	// command cut short by it records as a gap, which settles the verdict
	// to incomplete — the budget bounds the work, not the honesty about
	// it, exactly as the step cap does.
	passCtx, cancelPass := context.WithTimeout(ctx, total)
	defer cancelPass()

	v := &verification{Status: verificationPassed, Commit: target}
	logging.Info("verification: checking commit %s, the tip of branch %s (%s per command, %s for the pass)",
		shortSHA(target), branch, timeout, total)
	for _, step := range parsed.Steps {
		if !step.runnable() {
			logging.Info("verification: %s skipped — %s", step.Label, step.Skip)
			v.Commands = append(v.Commands, verificationStep{
				Label:  step.Label,
				Status: verifyStepSkipped,
				Reason: clipVerify(red.redact(step.Skip), verifyReasonLen),
			})
			continue
		}
		if passCtx.Err() != nil {
			stopped := passStopped(passCtx, total)
			logging.Warn("verification: %s not run — %s", step.Label, stopped)
			v.Commands = append(v.Commands, verificationStep{
				Label:   step.Label,
				Command: clipVerify(red.redact(step.Command), verifyCommandLen),
				Status:  verifyStepAborted,
				Reason:  stopped + " before this command",
			})
			continue
		}
		v.Commands = append(v.Commands, runVerifyStep(passCtx, cloneDir, timeout, total, step, red))
	}

	v.settle(red, gap, truncatedGap(parsed.Truncated))
	return v
}

// passStopped says why the pass stopped before or during a command: the run
// was cancelled, or the pass used up its whole-pass budget. Both are gaps
// rather than measurements, and they settle the same way, but they are not
// the same gap and the record says which.
func passStopped(passCtx context.Context, total time.Duration) string {
	if errors.Is(passCtx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("the verification pass ran out of its %s budget", total)
	}
	return "the run was cancelled"
}

// truncatedGap renders the step cap as a gap, or "" when it did not bite.
func truncatedGap(truncated bool) string {
	if !truncated {
		return ""
	}
	return fmt.Sprintf("the ## Verification section lists more than %d commands; the rest were not run", verifyMaxSteps)
}

// settle decides the overall verdict from the recorded steps, in strict
// precedence: a measured failure, then an incomplete measurement, then a
// pass. The precedence is the point. Before this the verdict started at
// passed and only a failed step moved it, so a command the harness never
// got to — because the run was cancelled under it, or because the step cap
// dropped it — left a `passed` verdict covering work nobody checked. That
// is the same unearned green the whole file exists to remove, with the
// harness making the claim instead of the agent.
func (v *verification) settle(red *redactor, extra ...string) {
	var gaps []string
	for _, g := range extra {
		if g != "" {
			gaps = append(gaps, g)
		}
	}
	ran := 0
	for _, c := range v.Commands {
		switch c.Status {
		case verifyStepPassed:
			ran++
		case verifyStepFailed, verifyStepTimedOut, verifyStepError:
			ran++
			if !v.failed() {
				v.Status = verificationFailed
				v.Reason = fmt.Sprintf("%s %s: %s", c.Label, c.Status, c.Command)
			}
		case verifyStepAborted:
			gaps = append(gaps, fmt.Sprintf("%s was not measured (%s)", c.Label, c.Reason))
		}
	}
	switch {
	case v.failed():
		// A real failure outranks a gap: it is the most actionable thing
		// the pass found, and the gap is noted in the step records.
		logging.Err("verification FAILED — %s", v.Reason)
		logging.Err("the pushed tree does not satisfy the plan's own verification commands, " +
			"whatever the handoff says about them")
	case len(gaps) > 0:
		v.Status = verificationIncomplete
		v.Reason = strings.Join(gaps, "; ")
		logging.Err("verification INCOMPLETE — %s", v.Reason)
		logging.Err("%d command(s) passed, but this is not a verified pass: part of the plan's "+
			"verification was never measured", ran)
	case ran == 0:
		// Defensive: the runnable check above means this should not happen.
		v.Status = verificationIncomplete
		v.Reason = "no verification command produced a result"
		logging.Err("verification INCOMPLETE — %s", v.Reason)
	default:
		logging.Ok("verification passed — %d command(s) from %s re-run by the harness on commit %s",
			ran, planPath, shortSHA(v.Commit))
	}
	v.Reason = clipVerify(red.redact(v.Reason), verifyReasonLen)
}

// verifyTarget returns the commit the result describes: the tip of the branch
// Push will send, confirmed to be the commit the working tree is on. A
// non-empty mismatch means the harness cannot identify or reach that commit
// at all, and says why.
//
// git.Push sends refs/heads/<branch> whatever HEAD points at (#281), so the
// working tree in cloneDir is not necessarily the tree that reaches the
// remote. An agent that commits a broken migration onto the branch and then
// checks out a passing base commit would otherwise have the harness run its
// commands against the base, pass them, and push the broken branch — a
// verified-looking green over a tree nothing checked. That case is refused
// outright: a different commit is not a partial measurement of this one.
//
// Differences between the working tree and the commit are handled separately,
// by worktreeGap: there the commands still run, because a failure on a tree
// that is a superset of the commit is a real failure, while a pass is not a
// pass for the commit.
func verifyTarget(repo *gogit.Repository, branch string) (commit, mismatch string) {
	if repo == nil || branch == "" {
		return "", "the harness could not identify the commit being pushed"
	}
	tip, err := git.BranchTipSHA(repo, branch)
	if err != nil {
		return "", fmt.Sprintf("resolve branch %s: %v", branch, err)
	}
	head, err := git.HeadSHA(repo)
	if err != nil {
		return "", fmt.Sprintf("resolve HEAD: %v", err)
	}
	if head != tip {
		return "", fmt.Sprintf("the working tree is at %s but branch %s points at %s — "+
			"the harness cannot verify the commit being pushed", shortSHA(head), branch, shortSHA(tip))
	}
	return tip, ""
}

// verifyIgnoredPaths are the two stage-bookkeeping files whose uncommitted
// state cannot change what a verification command measures. Everything else
// counts, by exact path — any other file under .konveyor/ included.
//
//   - analysis.json the harness writes into the workspace itself, before the
//     agent runs, and never commits.
//   - handoff.md the agent appends to as its last act, which the verify
//     skill makes mandatory ("You MUST append your ## Verify section ...")
//     and which therefore lands AFTER the commit of the work. #241 reads it
//     straight from the worktree for that reason. Counting it would make
//     every clean verify stage report `incomplete` over a file no build
//     compiles — the harness refusing its own pass for doing as it asked.
//
// Neither is an input to a build, which is what separates them from the
// untracked source file this check exists to catch.
var verifyIgnoredPaths = map[string]bool{
	".konveyor/analysis.json": true,
	handoffPath:               true,
}

// worktreeGap reports what the working tree holds that the commit being
// pushed does not: tracked files edited but not committed, and untracked
// files git is not ignoring. Empty when the tree is the commit.
//
// The untracked half is the one that is easy to miss. A command that passes
// in the working directory can be compiling a source file the agent created
// and never added, so the commit that reaches the remote does not contain it
// and does not build — a pass measured against inputs the branch will not
// have. The harness does not try to tell a stray source file from anything
// else untracked: git already excludes what the project ignores, so what is
// left is uncommitted input, and a pass over it is not a pass for the commit.
func worktreeGap(repo *gogit.Repository, commit string) (string, error) {
	paths, err := git.UncommittedPaths(repo)
	if err != nil {
		return "", err
	}
	kept := make([]string, 0, len(paths))
	for _, path := range paths {
		if !verifyIgnoredPaths[path] {
			kept = append(kept, path)
		}
	}
	if len(kept) == 0 {
		return "", nil
	}
	shown := kept
	if len(shown) > 3 {
		shown = shown[:3]
	}
	more := ""
	if len(kept) > len(shown) {
		more = fmt.Sprintf(", and %d more", len(kept)-len(shown))
	}
	return fmt.Sprintf("%d path(s) in the working tree are not in commit %s (%s%s), so the commands "+
		"did not run on the tree being pushed", len(kept), shortSHA(commit), strings.Join(shown, ", "), more), nil
}

// shortSHA abbreviates a commit for a log line or a reason.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "unknown"
	}
	return sha
}

// runVerifyStep runs one command and records what it exited with.
func runVerifyStep(ctx context.Context, dir string, timeout, total time.Duration, step planStep, red *redactor) verificationStep {
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
	case ctx.Err() != nil:
		// The pass itself stopped — cancelled, or out of its whole-pass
		// budget. Checked BEFORE the command's own deadline, because
		// cmdCtx inherits whichever deadline is earlier: a pass budget
		// that ran out under a command would otherwise be recorded as
		// that command timing out, and settle counts a timeout as a
		// measured failure. The harness would be failing a tree over a
		// clock it ran out of itself.
		stopped := passStopped(ctx, total)
		recorded.Status = verifyStepAborted
		recorded.Reason = stopped + " during this command"
		logging.Warn("verification: %s aborted — %s", step.Label, stopped)
	case errors.Is(cmdCtx.Err(), context.DeadlineExceeded):
		recorded.Status = verifyStepTimedOut
		recorded.Reason = fmt.Sprintf("no exit after %s", timeout)
		logging.Err("verification: %s TIMED OUT after %s — %s", step.Label, timeout, recorded.Command)
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
		// The TAIL, not the head: out is already a 64 KiB tail, and for
		// the #300 case most of that is npm progress with the 45 errors
		// at the end. Clipping from the front kept the progress and
		// dropped the errors from the one copy that outlives the pod.
		logging.Info("verification: %s output (tail of %d captured bytes):\n%s",
			step.Label, len(output), clipVerifyTail(output, verifyOutputLog))
		recorded.Output = clipVerify(lastLines(output), verifyOutputBlob)
	}
	return recorded
}

// planVerification is what the parser made of the plan's `## Verification`
// section.
type planVerification struct {
	// Steps are the section's lines, classified into commands to run and
	// lines to skip.
	Steps []planStep
	// Found is false when the document has no such section at all —
	// distinct from a section with nothing in it.
	Found bool
	// Truncated records that verifyMaxSteps left lines of the section
	// unread.
	Truncated bool
	// Fence is the opening fence line of the first fenced block inside the
	// section, or "" when it had none. The block's contents are dropped
	// before parsing and stay dropped; this field is only how the caller
	// tells a section that was read and had nothing to score from one
	// whose content never reached the parser. See runPlanVerification.
	Fence string
}

// runnable reports whether the section named at least one command to run.
func (p planVerification) runnable() bool { return slices.ContainsFunc(p.Steps, planStep.runnable) }

// parsePlanVerification reads the plan's Verification section.
//
// Fenced code blocks are dropped first (handoffProse), for the same reason
// the handoff reader drops them: a plan that quotes the plan template back
// inside a fence carries a second `## Verification` heading and a second set
// of `- Build:` lines, and reading those would have the harness run the
// template's own `<build command>` placeholder. That stays — but the parser
// records that a block was there (Fence), because a section whose commands
// were all inside one is not a section that named no commands (#336).
func parsePlanVerification(content string) planVerification {
	parsed := planVerification{}
	inSection := false
	// The indentation of the prose line whose sub-bullets we are inside,
	// and its label. -1 when we are not inside one. See proseChild.
	proseIndent, proseParent := -1, ""
	for _, entry := range handoffProseLines(content) {
		if entry.Fence {
			// A dropped block. Its contents are not read; the first one
			// that belongs to the section itself is remembered so the
			// verdict can say the section's content was unreadable rather
			// than absent.
			//
			// A block nested under an open prose line is NOT one of those.
			// The plan skill specifies Blackbox as "steps to start the app
			// and verify key business flows still work", and a model that
			// illustrates a step with a fenced snippet is still writing
			// manual steps — the indentation says so, exactly as it does
			// for a nested item (see proseChild). Recording that fence made
			// a manual-only section report incomplete and, under
			// enforcement, fail the stage for containing a code block.
			if inSection && parsed.Fence == "" && !nestedUnderProse(entry.Text, proseIndent) {
				parsed.Fence = strings.TrimSpace(entry.Text)
			}
			continue
		}
		line := entry.Text
		if m := sectionHeading.FindStringSubmatch(line); m != nil {
			// Any heading closes an open manual list. A deeper one keeps us
			// in the section, but nothing under it belongs to a list that
			// ended above it — and a sub-bullet that stays nested is a
			// skipped command, so leaving the block open reads as a pass.
			proseIndent, proseParent = -1, ""
			switch {
			case verificationHeading.MatchString(line):
				// A second Verification heading restarts the section:
				// later wins, as it does for the handoff's verdict.
				inSection = true
				parsed = planVerification{Found: true}
			case len(m[1]) <= 2:
				inSection = false
			}
			continue
		}
		if !inSection {
			continue
		}
		m := verificationItem.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// The cap bounds the work, not the honesty about it: a dropped
		// line is reported so the verdict can say the section was only
		// partly measured, instead of passing on the prefix it ran.
		if len(parsed.Steps) >= verifyMaxSteps {
			parsed.Truncated = true
			continue
		}
		step := classifyVerifyItem(m[1], m[2])
		indent := itemIndent(line)
		switch {
		case proseIndent >= 0 && indent > proseIndent:
			// Nested under a prose line, so it belongs to that line and
			// the block stays open for its siblings.
			step = proseChild(step.Label, proseParent)
		case verifyProseLabels[strings.ToLower(step.Label)]:
			proseIndent, proseParent = indent, step.Label
		default:
			proseIndent, proseParent = -1, ""
		}
		parsed.Steps = append(parsed.Steps, step)
	}
	return parsed
}

// proseChild is a Verification line nested under a prose one. The plan skill
// specifies Blackbox as "steps to start the app and verify key business flows
// still work", and a model that writes those steps as sub-bullets gives each
// one a label of its own:
//
//	## Verification
//	- Blackbox:
//	  - Start the app: mvn quarkus:dev
//	  - Check the endpoint: curl -sf localhost:8080/q/health
//
// Read on its own, `Start the app` is a command — and `mvn quarkus:dev` never
// exits, so the harness would spend a whole timeout on it and record a
// measured failure for a plan whose every scorable command passed. The
// nesting is the only thing that says those lines are part of the prose above
// them, so the parser is the only place that can tell.
func proseChild(label, parent string) planStep {
	return planStep{
		Label: label,
		Skip:  fmt.Sprintf("nested under %s — part of its manual steps, not a command the harness can run or score", parent),
	}
}

// nestedUnderProse reports whether a line sits inside the sub-block of an
// open prose list item: the same indentation test the parser applies to a
// nested item, applied to a fenced block. proseIndent is -1 when no prose
// line is open, and then nothing is nested.
func nestedUnderProse(line string, proseIndent int) bool {
	return proseIndent >= 0 && itemIndent(line) > proseIndent
}

// itemIndent is the visual indentation of a list item, counting a tab as four
// columns so a tab-indented sub-bullet nests like a space-indented one.
func itemIndent(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
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
	switch first := verifyFirstWord(value); {
	case verifyProseLabels[strings.ToLower(step.Label)]:
		step.Skip = "manual steps, not a command the harness can run or score"
	case verifyEmptyValues[strings.ToLower(value)]:
		step.Skip = "the plan names no command here"
	// first != "": the empty string is an empty VALUE, but a value whose
	// first word is all punctuation — `. ./scripts/env.sh && npm test`
	// sources a file — is a command.
	case first != "" && verifyEmptyValues[first]:
		// A real plan wrote `**Runtime verification**: skipped — this
		// migration changes only the query library API surface, not
		// application behavior.` The sentence that follows the word does
		// not make the word mean something else, and before this the whole
		// 132-rune sentence went to `sh -c`, which recorded a measured
		// failure reading `/bin/sh: skipped: command not found` (#336).
		// The value says what it says; the rest is the plan's reasoning.
		step.Skip = fmt.Sprintf("the plan says %q here, not a command", first)
	case strings.HasPrefix(value, "<"):
		// The plan skill's template is literally `<build command, e.g. mvn
		// clean compile>`; an agent that copied it left a placeholder.
		step.Skip = "unfilled placeholder in the plan, not a command"
	case len([]rune(value)) > verifyCommandMaxLen:
		step.Skip = "prose, not a command (too long to be one)"
	case verifyLooksLikeProse(value):
		step.Skip = "prose, not a command (reads as a sentence)"
	default:
		step.Command = value
	}
	return step
}

// verifyFirstWord is the value's first word, lowercased and stripped of
// trailing punctuation, so `skipped —`, `Skipped:` and `skipped` all read
// as the same word.
func verifyFirstWord(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(strings.Trim(fields[0], ".,;:!?`\"'*"))
}

// verifyLooksLikeProse reports whether a Verification value is an English
// sentence rather than a command.
//
// The length guard above catches a paragraph; this catches the short
// sentence that fits under it. The test is a sentence boundary
// (verifySentence) in a value of more than one word: a command is a program
// and its arguments, and does not end a word with a full stop or run two
// capitalised sentences together.
//
// It is a heuristic, and the two ways it can be wrong are not symmetric. A
// line of prose misread as a command runs, exits non-zero and verdicts the
// stage `failed` — bogus evidence, loudly. A COMMAND misread as prose is
// skipped, and a section whose other commands pass then verdicts `passed`
// over a check that never ran: unearned green, which is the failure mode
// this whole file exists to remove. So the test fires only on unmistakable
// prose, and anything that carries a mark of shell is a command:
//
//   - a shell operator anywhere in the value (verifyShellOperator), and
//   - punctuation inside a quoted argument, which is a string being passed
//     to a program, not sentence structure. `test -f required-artifact &&
//     echo 'Build passed.'` is a real check, and skipping it let a missing
//     artefact pass.
func verifyLooksLikeProse(value string) bool {
	if !strings.ContainsAny(value, " \t") {
		return false
	}
	if verifyShellOperator.MatchString(value) {
		return false
	}
	return verifySentence.MatchString(verifyQuoted.ReplaceAllString(value, " "))
}

// notRun builds the recorded "nothing was measured" result. It carries a
// reason always: a verification that did not happen must never be mistaken
// for one that passed.
func notRun(format string, args ...any) *verification {
	reason := clipVerify(fmt.Sprintf(format, args...), verifyReasonLen)
	logging.Warn("verification not run: %s", reason)
	return &verification{Status: verificationNotRun, Reason: reason}
}

// incomplete builds the recorded "started and could not finish" result:
// there was something to measure and the harness cannot claim it did.
//
// Takes the redactor because its reasons quote git: a branch that will not
// resolve, a worktree that cannot be read. Those errors can carry the remote
// URL, and the clone's remote holds a token until StripCredentials runs — and
// verification.reason is the one field the termination blob keeps to the last
// rung of its trim ladder, so an unredacted one travels all the way out.
func incomplete(red *redactor, format string, args ...any) *verification {
	reason := clipVerify(red.redact(fmt.Sprintf(format, args...)), verifyReasonLen)
	logging.Err("verification INCOMPLETE — %s", reason)
	return &verification{Status: verificationIncomplete, Reason: reason}
}

// clipVerify bounds s to n runes with an ellipsis.
func clipVerify(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n-1])) + "…"
	}
	return s
}

// clipVerifyTail bounds s to its LAST n runes: what clipVerify does, from the
// other end, for output whose interesting part is where it stopped.
func clipVerifyTail(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return "…" + strings.TrimSpace(string(r[len(r)-n+1:]))
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
