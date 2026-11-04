package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// verifyBranch is the branch the fixtures push: the harness verifies the
// tip of the branch being pushed, not HEAD.
const verifyBranch = "migration-target"

// planFromSkill is the Verification section as the plan skill specifies it
// (catalog/skills/plan/SKILL.md) and as a real plan writes it.
const planFromSkill = `# Migration Plan

## Summary
- Files affected: 20

## Steps

### Step 1: migrate useQuery
- Phase: queries
- File: client/src/queries/applications.ts

## Verification
- Build: npm run build -w client
- Test: npm run test -w client
- Blackbox: start the app with npm run start:dev and check the applications table loads

## Notes
Nothing special.
`

// writePlan builds a workspace the harness will verify: a git repository on
// verifyBranch whose committed tree holds the plan, so the working tree is
// the commit being pushed. The tests that care about the commit check
// deliberately break one of those properties.
func writePlan(t *testing.T, content string) (string, *gogit.Repository) {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	commitPlan(t, dir, repo, content)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(verifyBranch), Create: true}); err != nil {
		t.Fatalf("checkout %s: %v", verifyBranch, err)
	}
	return dir, repo
}

// commitPlan writes docs/plan.md and commits it.
func commitPlan(t *testing.T, dir string, repo *gogit.Repository, content string) plumbing.Hash {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, planPath), []byte(content), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if _, err := wt.Add(planPath); err != nil {
		t.Fatalf("git add: %v", err)
	}
	sha, err := wt.Commit("plan", &gogit.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("git commit: %v", err)
	}
	return sha
}

// runPlan is runPlanVerification over a fixture built by writePlan, with the
// default pass budget.
func runPlan(ctx context.Context, dir string, repo *gogit.Repository, timeout time.Duration, red *redactor) *verification {
	return runPlanVerification(ctx, repo, dir, verifyBranch, timeout, 0, red)
}

// runPlanBudget is runPlan with an explicit budget for the whole pass.
func runPlanBudget(ctx context.Context, dir string, repo *gogit.Repository, timeout, total time.Duration, red *redactor) *verification {
	return runPlanVerification(ctx, repo, dir, verifyBranch, timeout, total, red)
}

func TestParsePlanVerification(t *testing.T) {
	cases := []struct {
		name          string
		content       string
		wantFound     bool
		wantTruncated bool
		wantSteps     []planStep
	}{
		{
			name:      "the plan skill's own Verification section",
			content:   planFromSkill,
			wantFound: true,
			wantSteps: []planStep{
				{Label: "Build", Command: "npm run build -w client"},
				{Label: "Test", Command: "npm run test -w client"},
				{Label: "Blackbox", Skip: "manual steps, not a command the harness can run or score"},
			},
		},
		{
			// #300's whole point: no section must not render as a pass.
			name:      "no Verification section at all",
			content:   "# Plan\n\n## Steps\n\n### Step 1\n- File: a.java\n",
			wantFound: false,
		},
		{
			name:      "a Verification heading with nothing under it",
			content:   "## Verification\n\n## Notes\nnone\n",
			wantFound: true,
		},
		{
			name:      "bold and backticked items are read as commands",
			content:   "## Verification\n* **Build**: `mvn -q clean compile`\n- *Test*: `mvn test`\n",
			wantFound: true,
			wantSteps: []planStep{
				{Label: "Build", Command: "mvn -q clean compile"},
				{Label: "Test", Command: "mvn test"},
			},
		},
		{
			// The plan skill's template is literally `<build command, …>`;
			// handing that to a shell would invent a failure.
			name:      "unfilled template placeholders are skipped, not run",
			content:   "## Verification\n- Build: <build command, e.g. mvn clean compile>\n- Test: none\n",
			wantFound: true,
			wantSteps: []planStep{
				{Label: "Build", Skip: "unfilled placeholder in the plan, not a command"},
				{Label: "Test", Skip: "the plan names no command here"},
			},
		},
		{
			name:      "a deeper heading stays inside the section",
			content:   "## Verification\n- Build: make\n### Extra\n- Test: make test\n## Notes\n- Build: never\n",
			wantFound: true,
			wantSteps: []planStep{
				{Label: "Build", Command: "make"},
				{Label: "Test", Command: "make test"},
			},
		},
		{
			// handoffProse drops fences first, so a plan quoting the
			// template back cannot supply commands it never meant.
			name:      "a fenced template excerpt supplies nothing",
			content:   "## Verification\n- Build: make\n\n```markdown\n## Verification\n- Build: <build command>\n```\n",
			wantFound: true,
			wantSteps: []planStep{{Label: "Build", Command: "make"}},
		},
		{
			name:      "a prose value too long to be a command is skipped",
			content:   "## Verification\n- Smoke: " + strings.Repeat("run the app and click around a lot ", 12) + "\n",
			wantFound: true,
			wantSteps: []planStep{{Label: "Smoke", Skip: "prose, not a command (too long to be one)"}},
		},
		{
			// The cap stays, but it must SAY it dropped lines: a prefix the
			// harness ran is not verification of the whole section.
			name:          "the section is capped, and the cap is reported",
			content:       "## Verification\n" + strings.Repeat("- Build: make\n", verifyMaxSteps+5),
			wantFound:     true,
			wantTruncated: true,
			wantSteps: func() []planStep {
				steps := make([]planStep, verifyMaxSteps)
				for i := range steps {
					steps[i] = planStep{Label: "Build", Command: "make"}
				}
				return steps
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			steps, truncated, found := parsePlanVerification(tc.content)
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v", found, tc.wantFound)
			}
			if truncated != tc.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tc.wantTruncated)
			}
			if len(steps) != len(tc.wantSteps) {
				t.Fatalf("got %d steps %+v, want %d", len(steps), steps, len(tc.wantSteps))
			}
			for i, want := range tc.wantSteps {
				if steps[i] != want {
					t.Errorf("step %d = %+v, want %+v", i, steps[i], want)
				}
			}
		})
	}
}

func TestRunPlanVerificationRecordsExitCodes(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: exit 0\n- Test: exit 7\n- Blackbox: click around\n")

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status != verificationFailed {
		t.Fatalf("status = %q, want %q (one command exited 7)", v.Status, verificationFailed)
	}
	if !strings.Contains(v.Reason, "Test") {
		t.Errorf("reason = %q, want it to name the command that failed", v.Reason)
	}
	if len(v.Commands) != 3 {
		t.Fatalf("got %d recorded commands, want 3", len(v.Commands))
	}
	if got := v.Commands[0]; got.Status != verifyStepPassed || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Errorf("Build = %+v, want passed with an explicit exit 0", got)
	}
	if got := v.Commands[1]; got.Status != verifyStepFailed || got.ExitCode == nil || *got.ExitCode != 7 {
		t.Errorf("Test = %+v, want failed with exit 7", got)
	}
	if got := v.Commands[2]; got.Status != verifyStepSkipped || got.Reason == "" {
		t.Errorf("Blackbox = %+v, want skipped with a reason", got)
	}
}

// The failure #300 reported: the agent's own commands were green, and the
// one it never re-ran was not. The harness runs what the plan names.
func TestRunPlanVerificationCatchesTheCompileItNeverRan(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: echo 'error TS2339: Property _isPending does not exist' >&2; exit 2\n")

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status != verificationFailed {
		t.Fatalf("status = %q, want %q", v.Status, verificationFailed)
	}
	if !strings.Contains(v.Commands[0].Output, "TS2339") {
		t.Errorf("output excerpt = %q, want the compiler error it printed", v.Commands[0].Output)
	}
}

func TestRunPlanVerificationPasses(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: true\n- Test: true\n")

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status != verificationPassed {
		t.Fatalf("status = %q, want %q", v.Status, verificationPassed)
	}
	if v.Reason != "" {
		t.Errorf("reason = %q, want empty for a clean pass", v.Reason)
	}
	for _, c := range v.Commands {
		if c.Output != "" {
			t.Errorf("%s carried output %q — only a command that did not pass should", c.Label, c.Output)
		}
	}
}

// Every "nothing ran" path must say so, and must not look like a pass.
func TestRunPlanVerificationNotRun(t *testing.T) {
	requireShell(t)
	cases := []struct {
		name       string
		setup      func(t *testing.T) (string, *gogit.Repository)
		wantReason string
	}{
		{
			name: "no plan at all",
			setup: func(t *testing.T) (string, *gogit.Repository) {
				return writePlan(t, "# Plan\n")
			},
			wantReason: "no ## Verification section",
		},
		{
			name: "no docs/plan.md in the workspace",
			setup: func(t *testing.T) (string, *gogit.Repository) {
				dir, repo := writePlan(t, "# Plan\n")
				if err := os.Remove(filepath.Join(dir, planPath)); err != nil {
					t.Fatalf("remove plan: %v", err)
				}
				return dir, repo
			},
			wantReason: "not found",
		},
		{
			name: "a Verification section with nothing in it",
			setup: func(t *testing.T) (string, *gogit.Repository) {
				return writePlan(t, "## Verification\n\n## Notes\n")
			},
			wantReason: "is empty",
		},
		{
			name: "a Verification section naming only manual steps",
			setup: func(t *testing.T) (string, *gogit.Repository) {
				return writePlan(t, "## Verification\n- Blackbox: start the app and click around\n- Test: none\n")
			},
			wantReason: "names no command to run",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, repo := tc.setup(t)
			v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())
			if v == nil {
				t.Fatal("verification is nil — an unmeasured run must still be recorded")
			}
			if v.Status != verificationNotRun {
				t.Fatalf("status = %q, want %q — silence must not read as green", v.Status, verificationNotRun)
			}
			if v.failed() {
				t.Error("notRun must not report as failed — it is an absent measurement, not a bad one")
			}
			if !strings.Contains(v.Reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to mention %q", v.Reason, tc.wantReason)
			}
		})
	}
}

func TestRunPlanVerificationTimesOut(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: sleep 30\n")

	start := time.Now()
	v := runPlan(context.Background(), dir, repo, 300*time.Millisecond, newRedactor())
	elapsed := time.Since(start)

	if elapsed > 15*time.Second {
		t.Fatalf("took %s — the per-command timeout did not bound the command", elapsed)
	}
	if v.Status != verificationFailed {
		t.Fatalf("status = %q, want %q — a command that never exits is not a pass", v.Status, verificationFailed)
	}
	if got := v.Commands[0]; got.Status != verifyStepTimedOut {
		t.Errorf("Build = %+v, want %q", got, verifyStepTimedOut)
	}
	if v.Commands[0].ExitCode != nil {
		t.Errorf("exitCode = %d, want none recorded for a command that never exited", *v.Commands[0].ExitCode)
	}
}

// A timeout must take the work down with the shell, not just the shell.
func TestRunPlanVerificationTimeoutKillsTheChild(t *testing.T) {
	requireShell(t)
	if runtime.GOOS == "windows" {
		t.Skip("process groups are a unix concept")
	}
	marker := filepath.Join(t.TempDir(), "still-running")
	// The shell backgrounds a child that would touch the marker well after
	// the deadline; killing only `sh` would leave it to do so.
	dir, repo := writePlan(t, "## Verification\n- Build: sh -c 'sleep 2; touch "+marker+"' & wait\n")

	runPlan(context.Background(), dir, repo, 200*time.Millisecond, newRedactor())

	time.Sleep(3 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the backgrounded child survived the timeout — the process group was not killed")
	}
}

func TestRunPlanVerificationAbortedRunIsNotAFailure(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: true\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	v := runPlan(ctx, dir, repo, time.Minute, newRedactor())

	if v.failed() {
		t.Errorf("status = %q — a cancelled run did not measure a failure, it measured nothing", v.Status)
	}
	if !v.incomplete() {
		t.Errorf("status = %q, want %q — nothing was measured, so this is not a pass", v.Status, verificationIncomplete)
	}
	if got := v.Commands[0]; got.Status != verifyStepAborted {
		t.Errorf("Build = %+v, want %q", got, verifyStepAborted)
	}
}

func TestRunPlanVerificationRedactsSecrets(t *testing.T) {
	requireShell(t)
	const token = "ghp_sup3rs3cret"
	dir, repo := writePlan(t, "## Verification\n- Build: echo "+token+"; exit 1\n")

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor(token))

	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), token) {
		t.Errorf("the recorded verification leaked a secret: %s", blob)
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := &tailBuffer{limit: 10}
	for _, chunk := range []string{"aaaaa", "bbbbb", "ccccc"} {
		if n, err := b.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if got := b.String(); got != "…bbbbbccccc" {
		t.Errorf("String() = %q, want the last 10 bytes marked as a tail", got)
	}

	// One write larger than the whole limit is also bounded.
	big := &tailBuffer{limit: 4}
	if _, err := big.Write([]byte(strings.Repeat("z", 1000))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := big.String(); got != "…zzzz" {
		t.Errorf("String() = %q, want 4 bytes", got)
	}
}

// The blob must shed output before verdicts, and the verdict last of all:
// a reader who finds no verification cannot tell a checked run from an
// unchecked one.
func TestTerminationLogTrimsVerificationOutputBeforeTheVerdict(t *testing.T) {
	code := 1
	term := terminationBlob{
		ExitCode: 0,
		Outcome:  outcomeSucceeded.String(),
		Verification: &verification{
			Status: verificationFailed,
			Reason: "Build failed: npm run build",
		},
	}
	for i := 0; i < verifyMaxSteps; i++ {
		term.Verification.Commands = append(term.Verification.Commands, verificationStep{
			Label:    "Build",
			Command:  strings.Repeat("c", verifyCommandLen),
			Status:   verifyStepFailed,
			ExitCode: &code,
			Output:   strings.Repeat("o", verifyOutputBlob),
		})
	}
	term.StopReason = strings.Repeat("s", 2000)

	path := filepath.Join(t.TempDir(), "termination-log")
	writeTerminationLog(path, term)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read termination log: %v", err)
	}
	if len(data) > maxTerminationLogBytes {
		t.Fatalf("wrote %d bytes, over the kubelet's %d-byte ceiling", len(data), maxTerminationLogBytes)
	}
	var got terminationBlob
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("termination log is not valid JSON: %v\n%s", err, data)
	}
	if got.Verification == nil || got.Verification.Status != verificationFailed {
		t.Fatalf("verification verdict did not survive trimming: %s", data)
	}
	for _, c := range got.Verification.Commands {
		if c.Output != "" {
			t.Errorf("output excerpts should be shed first, got %q", c.Output)
		}
	}

	// The caller's own copy must be untouched — the run already logged it.
	if term.Verification.Commands[0].Output == "" {
		t.Error("writeTerminationLog mutated the caller's verification record")
	}
}

// requireShell skips on a platform with no /bin/sh to run commands with.
func requireShell(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
}

// A pass must name the commit it describes, and that commit must be the
// one the final push sends.
func TestRunPlanVerificationRecordsTheCommitBeingPushed(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: true\n")
	tip, err := repo.Reference(plumbing.NewBranchReferenceName(verifyBranch), true)
	if err != nil {
		t.Fatalf("resolve %s: %v", verifyBranch, err)
	}

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status != verificationPassed {
		t.Fatalf("status = %q, want %q", v.Status, verificationPassed)
	}
	if v.Commit != tip.Hash().String() {
		t.Errorf("commit = %q, want the tip of %s (%s)", v.Commit, verifyBranch, tip.Hash())
	}
}

// git.Push sends refs/heads/<branch> even when HEAD is elsewhere (#281). An
// agent can commit a broken migration onto the branch and then check out a
// passing commit; the harness must not report a pass for a tree that is not
// the one being pushed.
func TestRunPlanVerificationRefusesWhenHEADIsNotTheBranchBeingPushed(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: true\n")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	// The branch moves on to a commit whose plan would fail…
	broken := commitPlan(t, dir, repo, "## Verification\n- Build: exit 1\n")
	// …and the agent checks the passing commit back out, leaving HEAD
	// detached at it while the branch still points at the broken one.
	base, err := repo.CommitObject(broken)
	if err != nil {
		t.Fatalf("commit object: %v", err)
	}
	parent, err := base.Parent(0)
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{Hash: parent.Hash, Force: true}); err != nil {
		t.Fatalf("detach at %s: %v", parent.Hash, err)
	}

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status == verificationPassed {
		t.Fatalf("status = %q — the checked tree is not the commit being pushed, so this is not a pass", v.Status)
	}
	if !v.incomplete() {
		t.Errorf("status = %q, want %q", v.Status, verificationIncomplete)
	}
	if !strings.Contains(v.Reason, verifyBranch) {
		t.Errorf("reason = %q, want it to name the branch and both commits", v.Reason)
	}
	if v.Commit != "" {
		t.Errorf("commit = %q, want empty — nothing was verified", v.Commit)
	}
}

// A tracked file edited but not committed means the tree the commands saw is
// not the tree the push sends.
func TestRunPlanVerificationIncompleteWhenTrackedFilesDifferFromTheCommit(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: true\n")
	if err := os.WriteFile(filepath.Join(dir, planPath), []byte("## Verification\n- Build: true\n# edited\n"), 0o644); err != nil {
		t.Fatalf("dirty the plan: %v", err)
	}

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if !v.incomplete() {
		t.Fatalf("status = %q, want %q — the working tree is not the committed tree", v.Status, verificationIncomplete)
	}
	if !strings.Contains(v.Reason, planPath) {
		t.Errorf("reason = %q, want it to name the differing path", v.Reason)
	}
}

// The reviewer's case: a source file the agent created and never committed
// makes the build pass in the working directory, while a clean checkout of
// the pushed commit does not have the file and fails. The pass must not
// stand — the commands did not run on the tree being pushed.
func TestRunPlanVerificationIncompleteWhenAnUntrackedSourceFileMakesItPass(t *testing.T) {
	requireShell(t)
	// Build succeeds only while Helper.java is present, and Helper.java is
	// never committed, so the commit being pushed does not compile.
	dir, repo := writePlan(t, "## Verification\n- Build: test -f Helper.java\n")
	if err := os.WriteFile(filepath.Join(dir, "Helper.java"), []byte("class Helper {}\n"), 0o644); err != nil {
		t.Fatalf("write untracked source: %v", err)
	}

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status == verificationPassed {
		t.Fatalf("status = passed — the build only passed because of a file the commit does not contain")
	}
	if !v.incomplete() {
		t.Fatalf("status = %q, want %q", v.Status, verificationIncomplete)
	}
	if !strings.Contains(v.Reason, "Helper.java") {
		t.Errorf("reason = %q, want it to name the uncommitted file", v.Reason)
	}
	// The command still ran: a failure on a superset of the commit is a
	// real failure, so the evidence is kept either way.
	if len(v.Commands) != 1 || v.Commands[0].Status != verifyStepPassed {
		t.Errorf("commands = %+v, want the Build recorded as it actually exited", v.Commands)
	}
}

// A measured failure still wins over the uncommitted-tree gap: it is the
// actionable finding, and the extra files cannot have caused it.
func TestRunPlanVerificationFailureOutranksAnUncommittedTree(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: exit 4\n")
	if err := os.WriteFile(filepath.Join(dir, "Stray.java"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write untracked source: %v", err)
	}

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status != verificationFailed {
		t.Fatalf("status = %q, want %q", v.Status, verificationFailed)
	}
}

// Build output the project ignores is not uncommitted input: git excludes
// it, so it must not cost the run its pass.
func TestRunPlanVerificationIgnoresGitignoredBuildOutput(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: true\n")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\ntarget/\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if _, err := wt.Add(".gitignore"); err != nil {
		t.Fatalf("git add .gitignore: %v", err)
	}
	if _, err := wt.Commit("ignore build output", &gogit.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	for _, rel := range []string{"node_modules/pkg/index.js", "target/classes/App.class"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status != verificationPassed {
		t.Errorf("status = %q (%s), want %q — ignored build output is not uncommitted input",
			v.Status, v.Reason, verificationPassed)
	}
}

// The one untracked file the harness writes itself is exempt: it is not the
// agent's uncommitted work and says nothing about the commit.
func TestRunPlanVerificationIgnoresTheHarnessOwnAnalysisFile(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: true\n")
	if err := os.MkdirAll(filepath.Join(dir, ".konveyor"), 0o755); err != nil {
		t.Fatalf("mkdir .konveyor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".konveyor", "analysis.json"), []byte("[]"), 0o644); err != nil {
		t.Fatalf("write analysis: %v", err)
	}

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status != verificationPassed {
		t.Errorf("status = %q (%s), want %q — the harness wrote that file itself",
			v.Status, v.Reason, verificationPassed)
	}
	// Any OTHER untracked file in the same directory still counts.
	if err := os.WriteFile(filepath.Join(dir, ".konveyor", "scratch.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}
	if v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor()); !v.incomplete() {
		t.Errorf("status = %q, want %q — only the harness's own file is exempt", v.Status, verificationIncomplete)
	}
}

// Cancellation AFTER a command has passed: the build passes, the run is
// cancelled while the test command is running, and the test is never
// measured. The verdict must not be the build's.
func TestRunPlanVerificationCancelledMidPassIsIncomplete(t *testing.T) {
	requireShell(t)
	marker := filepath.Join(t.TempDir(), "build-ran")
	// Build touches the marker and exits; Test then sleeps long enough to
	// be interrupted. The watcher below cancels once the marker appears and
	// a beat has passed, so Build has certainly exited and Test has
	// certainly started.
	dir, repo := writePlan(t, "## Verification\n- Build: touch "+marker+"\n- Test: sleep 5\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(marker); err == nil {
				time.Sleep(200 * time.Millisecond)
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	v := runPlan(ctx, dir, repo, time.Minute, newRedactor())

	if v.Status == verificationPassed {
		t.Fatalf("status = passed — Test was never measured, so the pass is unearned")
	}
	if !v.incomplete() {
		t.Fatalf("status = %q, want %q", v.Status, verificationIncomplete)
	}
	if !strings.Contains(v.Reason, "Test") {
		t.Errorf("reason = %q, want it to name the command that went unmeasured", v.Reason)
	}
	if len(v.Commands) != 2 {
		t.Fatalf("recorded %d commands, want 2", len(v.Commands))
	}
	if got := v.Commands[0]; got.Status != verifyStepPassed {
		t.Errorf("Build = %+v, want it recorded as passed — it did pass", got)
	}
	if got := v.Commands[1]; got.Status != verifyStepAborted {
		t.Errorf("Test = %+v, want %q — the gap must be visible per command, not only in the verdict",
			got, verifyStepAborted)
	}
}

// settle is the aggregation on its own: one passing command plus one the
// harness never measured is not a pass, whatever order they come in.
func TestSettlePassedPlusUnmeasuredIsIncomplete(t *testing.T) {
	zero := 0
	v := &verification{Status: verificationPassed, Commit: "abc123abc123", Commands: []verificationStep{
		{Label: "Build", Status: verifyStepPassed, ExitCode: &zero},
		{Label: "Test", Status: verifyStepAborted, Reason: "run cancelled during this command"},
	}}

	v.settle(newRedactor())

	if v.Status != verificationIncomplete {
		t.Fatalf("status = %q, want %q", v.Status, verificationIncomplete)
	}
	if !strings.Contains(v.Reason, "Test") {
		t.Errorf("reason = %q, want it to name Test", v.Reason)
	}
}

// The step cap hid a failing command behind a prefix of passing ones: six
// passing commands then `Test: exit 7` reported passed, and the failure
// never ran or appeared in the record.
func TestRunPlanVerificationStepCapIsIncompleteNotPassed(t *testing.T) {
	requireShell(t)
	plan := "## Verification\n" + strings.Repeat("- Build: true\n", verifyMaxSteps) + "- Test: exit 7\n"
	dir, repo := writePlan(t, plan)

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status == verificationPassed {
		t.Fatalf("status = passed — a command past the cap never ran, so the section was not verified")
	}
	if !v.incomplete() {
		t.Fatalf("status = %q, want %q", v.Status, verificationIncomplete)
	}
	if !strings.Contains(v.Reason, "not run") {
		t.Errorf("reason = %q, want it to say the remaining commands were not run", v.Reason)
	}
	if len(v.Commands) != verifyMaxSteps {
		t.Errorf("recorded %d commands, want the cap of %d", len(v.Commands), verifyMaxSteps)
	}
}

// A real failure outranks a gap: it is the actionable finding.
func TestRunPlanVerificationFailureOutranksAnIncompleteGap(t *testing.T) {
	requireShell(t)
	plan := "## Verification\n- Build: exit 3\n" + strings.Repeat("- Lint: true\n", verifyMaxSteps) + "- Test: true\n"
	dir, repo := writePlan(t, plan)

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status != verificationFailed {
		t.Fatalf("status = %q, want %q — a measured failure is the most actionable result", v.Status, verificationFailed)
	}
	if !strings.Contains(v.Reason, "Build") {
		t.Errorf("reason = %q, want it to name the failing command", v.Reason)
	}
}

// unmet is what enforcement acts on: failed and incomplete yes, notRun no.
func TestVerificationUnmet(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{verificationFailed, true},
		{verificationIncomplete, true},
		{verificationPassed, false},
		{verificationNotRun, false},
	}
	for _, tc := range cases {
		v := &verification{Status: tc.status}
		if got := v.unmet(); got != tc.want {
			t.Errorf("(%s).unmet() = %v, want %v", tc.status, got, tc.want)
		}
	}
	var none *verification
	if none.unmet() {
		t.Error("a nil verification must not read as unmet")
	}
}

// The plan skill asks for Blackbox as "steps to start the app and verify key
// business flows still work", and a model that writes those steps as
// sub-bullets gives each one a label. Read on its own, `Start the app` is a
// command — and `mvn quarkus:dev` never exits, so the harness would spend a
// whole timeout on it and record a measured failure over a plan whose every
// scorable command passed.
func TestParsePlanVerificationNestedProseStepsAreNotCommands(t *testing.T) {
	content := "## Verification\n" +
		"- Build: mvn -q clean compile\n" +
		"- Blackbox:\n" +
		"  - Start the app: mvn quarkus:dev\n" +
		"  - Check the endpoint: curl -sf localhost:8080/q/health\n" +
		"- Test: mvn test\n"

	steps, _, found := parsePlanVerification(content)

	if !found {
		t.Fatal("found = false, want the section")
	}
	if len(steps) != 5 {
		t.Fatalf("got %d steps %+v, want 5", len(steps), steps)
	}
	for _, i := range []int{2, 3} {
		if steps[i].runnable() {
			t.Errorf("step %d = %+v, want it skipped as part of the Blackbox steps", i, steps[i])
		}
		if !strings.Contains(steps[i].Skip, "Blackbox") {
			t.Errorf("step %d skip = %q, want it to name the prose line it belongs to", i, steps[i].Skip)
		}
	}
	// A sibling back at the parent's indentation closes the block: the
	// nesting is what makes a line prose, not where it sits in the list.
	if steps[4].Command != "mvn test" {
		t.Errorf("step 4 = %+v, want the un-nested Test line still read as a command", steps[4])
	}
}

// Tab-indented sub-bullets nest like space-indented ones.
func TestParsePlanVerificationNestedWithTabs(t *testing.T) {
	steps, _, _ := parsePlanVerification("## Verification\n- Blackbox:\n\t- Start the app: npm start\n")
	if len(steps) != 2 {
		t.Fatalf("got %d steps %+v, want 2", len(steps), steps)
	}
	if steps[1].runnable() {
		t.Errorf("step 1 = %+v, want it skipped as part of the Blackbox steps", steps[1])
	}
}

// The cap dropping EVERY command is the same gap as the cap dropping one, and
// the first cut read it as notRun — "there was nothing to measure" — which
// enforcement ignores by design. A section that named commands the harness
// never ran is incomplete.
func TestRunPlanVerificationCapDroppingEveryCommandIsIncomplete(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n"+
		strings.Repeat("- Blackbox: start the app and click around\n", verifyMaxSteps)+
		"- Build: exit 7\n")

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status != verificationIncomplete {
		t.Fatalf("status = %q, want %q — the section named a command the cap dropped", v.Status, verificationIncomplete)
	}
	if !v.unmet() {
		t.Error("unmet() = false, want true — enforcement must be able to act on a section that went unmeasured")
	}
	if !strings.Contains(v.Reason, "not run") {
		t.Errorf("reason = %q, want it to say the rest were not run", v.Reason)
	}
}

// The per-command timeout bounds one command; without a budget for the pass a
// plan naming a dozen slow ones spends hours after the agent's turn, with the
// final push and the termination blob behind it.
func TestRunPlanVerificationPassBudgetLeavesAGap(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: true\n- Test: sleep 30\n- Lint: true\n")

	// Per-command timeout far above the budget, so what stops the pass is
	// the budget and nothing else.
	v := runPlanBudget(context.Background(), dir, repo, time.Minute, 400*time.Millisecond, newRedactor())

	if v.Status != verificationIncomplete {
		t.Fatalf("status = %q, want %q", v.Status, verificationIncomplete)
	}
	if !strings.Contains(v.Reason, "budget") {
		t.Errorf("reason = %q, want it to name the pass budget", v.Reason)
	}
	if len(v.Commands) != 3 {
		t.Fatalf("got %d commands %+v, want 3", len(v.Commands), v.Commands)
	}
	if v.Commands[0].Status != verifyStepPassed {
		t.Errorf("Build = %+v, want it recorded as it exited", v.Commands[0])
	}
	// The budget is the harness running out of its own clock, not the
	// command failing to exit: recorded as a gap, never as a timeout,
	// which settle would count as a measured failure.
	for _, i := range []int{1, 2} {
		if v.Commands[i].Status != verifyStepAborted {
			t.Errorf("command %d = %+v, want %q", i, v.Commands[i], verifyStepAborted)
		}
		if !strings.Contains(v.Commands[i].Reason, "budget") {
			t.Errorf("command %d reason = %q, want it to name the budget", i, v.Commands[i].Reason)
		}
	}
}

// The agent appends its handoff section as its last act — the verify skill
// makes it mandatory, after the commit of the work, and #241 reads it straight
// from the worktree for that reason. Counting it as uncommitted input would
// make every clean verify stage report incomplete over a file no build
// compiles.
func TestRunPlanVerificationIgnoresTheUncommittedHandoff(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: true\n")
	if err := os.MkdirAll(filepath.Join(dir, ".konveyor"), 0o755); err != nil {
		t.Fatalf("mkdir .konveyor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, handoffPath), []byte("## Verify\n- Status: passed\n"), 0o644); err != nil {
		t.Fatalf("write handoff: %v", err)
	}

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status != verificationPassed {
		t.Fatalf("status = %q (%s), want %q", v.Status, v.Reason, verificationPassed)
	}
}

// The exemption is by exact path: anything else the agent leaves under
// .konveyor/ counts like any other uncommitted file.
func TestRunPlanVerificationCountsOtherKonveyorFiles(t *testing.T) {
	requireShell(t)
	dir, repo := writePlan(t, "## Verification\n- Build: true\n")
	if err := os.MkdirAll(filepath.Join(dir, ".konveyor"), 0o755); err != nil {
		t.Fatalf("mkdir .konveyor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".konveyor/scratch.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}

	v := runPlan(context.Background(), dir, repo, time.Minute, newRedactor())

	if v.Status != verificationIncomplete {
		t.Fatalf("status = %q, want %q", v.Status, verificationIncomplete)
	}
	if !strings.Contains(v.Reason, "scratch.json") {
		t.Errorf("reason = %q, want it to name the uncommitted file", v.Reason)
	}
}

// verification.reason survives to the last rung of the termination blob's trim
// ladder, so a git error quoting a credentialed remote must not travel in it.
func TestIncompleteRedactsItsReason(t *testing.T) {
	red := &redactor{secrets: []string{"ghp_supersecrettoken"}}

	v := incomplete(red, "resolve branch %s: %v", "main",
		errors.New("remote https://ghp_supersecrettoken@github.com/o/r.git not found"))

	if strings.Contains(v.Reason, "ghp_supersecrettoken") {
		t.Errorf("reason = %q, want the secret redacted", v.Reason)
	}
}

// The pod log is the only copy of a failing command's output that outlives the
// pod, and the interesting part of a build's output is where it stopped.
func TestClipVerifyTailKeepsTheEnd(t *testing.T) {
	output := strings.Repeat("progress\n", 500) + "ERROR: 45 problems"

	got := clipVerifyTail(output, 40)

	if !strings.Contains(got, "ERROR: 45 problems") {
		t.Errorf("clipVerifyTail = %q, want the end of the output", got)
	}
	if len([]rune(got)) > 40 {
		t.Errorf("clipVerifyTail returned %d runes, want at most 40", len([]rune(got)))
	}
}
