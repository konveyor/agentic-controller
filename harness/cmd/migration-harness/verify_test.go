package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

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

func writePlan(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, planPath), []byte(content), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	return dir
}

func TestParsePlanVerification(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		wantFound bool
		wantSteps []planStep
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
			name:      "the section is capped so a huge plan cannot flood the record",
			content:   "## Verification\n" + strings.Repeat("- Build: make\n", verifyMaxSteps+5),
			wantFound: true,
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
			steps, found := parsePlanVerification(tc.content)
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v", found, tc.wantFound)
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
	dir := writePlan(t, "## Verification\n- Build: exit 0\n- Test: exit 7\n- Blackbox: click around\n")

	v := runPlanVerification(context.Background(), dir, time.Minute, newRedactor())

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
	dir := writePlan(t, "## Verification\n- Build: echo 'error TS2339: Property _isPending does not exist' >&2; exit 2\n")

	v := runPlanVerification(context.Background(), dir, time.Minute, newRedactor())

	if v.Status != verificationFailed {
		t.Fatalf("status = %q, want %q", v.Status, verificationFailed)
	}
	if !strings.Contains(v.Commands[0].Output, "TS2339") {
		t.Errorf("output excerpt = %q, want the compiler error it printed", v.Commands[0].Output)
	}
}

func TestRunPlanVerificationPasses(t *testing.T) {
	requireShell(t)
	dir := writePlan(t, "## Verification\n- Build: true\n- Test: true\n")

	v := runPlanVerification(context.Background(), dir, time.Minute, newRedactor())

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
		setup      func(t *testing.T) string
		wantReason string
	}{
		{
			name:       "no plan at all",
			setup:      func(t *testing.T) string { return t.TempDir() },
			wantReason: "not found",
		},
		{
			name:       "a plan with no Verification section",
			setup:      func(t *testing.T) string { return writePlan(t, "# Plan\n\n## Steps\n- do a thing\n") },
			wantReason: "no ## Verification section",
		},
		{
			name:       "a Verification section with nothing in it",
			setup:      func(t *testing.T) string { return writePlan(t, "## Verification\n\n## Notes\n") },
			wantReason: "is empty",
		},
		{
			name: "a Verification section naming only manual steps",
			setup: func(t *testing.T) string {
				return writePlan(t, "## Verification\n- Blackbox: start the app and click around\n- Test: none\n")
			},
			wantReason: "names no command to run",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := runPlanVerification(context.Background(), tc.setup(t), time.Minute, newRedactor())
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
	dir := writePlan(t, "## Verification\n- Build: sleep 30\n")

	start := time.Now()
	v := runPlanVerification(context.Background(), dir, 300*time.Millisecond, newRedactor())
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
	dir := t.TempDir()
	marker := filepath.Join(dir, "still-running")
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	// The shell backgrounds a child that would touch the marker well after
	// the deadline; killing only `sh` would leave it to do so.
	plan := "## Verification\n- Build: sh -c 'sleep 2; touch " + marker + "' & wait\n"
	if err := os.WriteFile(filepath.Join(dir, planPath), []byte(plan), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	runPlanVerification(context.Background(), dir, 200*time.Millisecond, newRedactor())

	time.Sleep(3 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the backgrounded child survived the timeout — the process group was not killed")
	}
}

func TestRunPlanVerificationAbortedRunIsNotAFailure(t *testing.T) {
	requireShell(t)
	dir := writePlan(t, "## Verification\n- Build: true\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	v := runPlanVerification(ctx, dir, time.Minute, newRedactor())

	if v.failed() {
		t.Errorf("status = %q — a cancelled run did not measure a failure, it measured nothing", v.Status)
	}
	if got := v.Commands[0]; got.Status != verifyStepAborted {
		t.Errorf("Build = %+v, want %q", got, verifyStepAborted)
	}
}

func TestRunPlanVerificationRedactsSecrets(t *testing.T) {
	requireShell(t)
	const token = "ghp_sup3rs3cret"
	dir := writePlan(t, "## Verification\n- Build: echo "+token+"; exit 1\n")

	v := runPlanVerification(context.Background(), dir, time.Minute, newRedactor(token))

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
