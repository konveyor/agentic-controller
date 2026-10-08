package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konveyor/migration-harness/internal/config"
	"github.com/konveyor/migration-harness/internal/hub"
)

// requiredConfigEnvVars are the env vars config.LoadFromEnv requires (see
// internal/config/config.go); clearing all of them makes it fail
// deterministically regardless of the ambient environment.
var requiredConfigEnvVars = []string{
	"KONVEYOR_LLM_MODEL",
	"KONVEYOR_MODEL_PRIMARY_MODEL",
	"HUB_BASE_URL",
	"APP_ID",
	"KONVEYOR_ACP_SECRET_KEY",
	"TARGET_BRANCH",
}

func TestRunStagePreservesErrorInTerminationLog(t *testing.T) {
	// A real failing run: missing configuration is the first thing
	// runStage checks, so this exercises the same early-return path as a
	// hub-resolution or clone failure (issue #189 follow-up) — none of
	// those set term.StopReason before returning, so without the fix the
	// termination log would only ever contain {"exitCode":1,"outcome":"failed"}.
	for _, k := range requiredConfigEnvVars {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	logPath := filepath.Join(t.TempDir(), "termination-log")
	t.Setenv("HARNESS_TERMINATION_LOG_PATH", logPath)

	code, err := runStage(nil, nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if err == nil {
		t.Fatal("expected an error from missing configuration, got nil")
	}

	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read termination log: %v", readErr)
	}
	var got terminationBlob
	if unmarshalErr := json.Unmarshal(data, &got); unmarshalErr != nil {
		t.Fatalf("termination log is not valid JSON: %v (data: %s)", unmarshalErr, data)
	}
	if got.ExitCode != 1 || got.Outcome != outcomeFailed.String() {
		t.Errorf("termination blob = %+v, want ExitCode=1, Outcome=%q", got, outcomeFailed.String())
	}
	if got.StopReason != err.Error() {
		t.Errorf("StopReason = %q, want the returned error preserved: %q", got.StopReason, err.Error())
	}
}

func TestDiscoverSkills_NoSkills(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HARNESS_SKILLS_DIR", dir)

	paths, err := discoverSkills()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected no paths, got: %v", paths)
	}
}

func TestDiscoverSkills_WithSkills(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HARNESS_SKILLS_DIR", dir)

	skillDir := filepath.Join(dir, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("do the thing"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, err := discoverSkills()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 1 {
		t.Errorf("expected 1 path, got: %v", paths)
	}
}

func TestDiscoverSkills_EmptySkillFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HARNESS_SKILLS_DIR", dir)

	skillDir := filepath.Join(dir, "empty-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, err := discoverSkills()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 1 {
		t.Errorf("expected 1 path (skill is mounted), got: %v", paths)
	}
}

func TestResolveFromHub_NoRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/applications/42" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42,"name":"missing-repo","repository":null}`))
	}))
	defer server.Close()

	cfg := &config.Config{AppID: "42"}
	_, err := resolveFromHub(cfg, hub.NewClient(server.URL, "test-token"))
	if err == nil {
		t.Fatal("expected an error for an application without a source repository")
	}
	if !strings.Contains(err.Error(), `application "missing-repo" has no source repository configured`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSymlinkSkillsDir(t *testing.T) {
	homeDir := t.TempDir()
	skillsSrc := t.TempDir()

	if err := symlinkSkillsDir(homeDir, skillsSrc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	link := filepath.Join(homeDir, ".agents", "skills")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", link, err)
	}
	if target != skillsSrc {
		t.Errorf("symlink target = %q, want %q", target, skillsSrc)
	}
}

func TestSymlinkSkillsDir_AlreadyExistsDir(t *testing.T) {
	homeDir := t.TempDir()
	skillsSrc := t.TempDir()

	if err := os.MkdirAll(filepath.Join(homeDir, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := symlinkSkillsDir(homeDir, skillsSrc)
	if err == nil {
		t.Fatal("expected error when .agents/skills already exists as a directory")
	}
	if !strings.Contains(err.Error(), "not a symlink") {
		t.Errorf("error should mention 'not a symlink', got: %v", err)
	}
}

func TestSymlinkSkillsDir_Idempotent(t *testing.T) {
	homeDir := t.TempDir()
	skillsSrc := t.TempDir()

	if err := symlinkSkillsDir(homeDir, skillsSrc); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := symlinkSkillsDir(homeDir, skillsSrc); err != nil {
		t.Fatalf("second call (same target) should be idempotent: %v", err)
	}

	link := filepath.Join(homeDir, ".agents", "skills")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", link, err)
	}
	if target != skillsSrc {
		t.Errorf("symlink target = %q, want %q", target, skillsSrc)
	}
}

func TestSymlinkSkillsDir_RelinksOnDifferentTarget(t *testing.T) {
	homeDir := t.TempDir()
	oldSrc := t.TempDir()
	newSrc := t.TempDir()

	if err := symlinkSkillsDir(homeDir, oldSrc); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := symlinkSkillsDir(homeDir, newSrc); err != nil {
		t.Fatalf("second call (different target): %v", err)
	}

	link := filepath.Join(homeDir, ".agents", "skills")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", link, err)
	}
	if target != newSrc {
		t.Errorf("symlink target = %q, want %q", target, newSrc)
	}
}

func TestSymlinkSkillsDir_ResolvesRelativePath(t *testing.T) {
	parent := t.TempDir()
	homeDir := filepath.Join(parent, "home")
	skillsSrc := filepath.Join(parent, "skills")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillsSrc, 0o755); err != nil {
		t.Fatal(err)
	}

	relPath, err := filepath.Rel(parent, skillsSrc)
	if err != nil {
		t.Fatal(err)
	}

	oldWd, _ := os.Getwd()
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWd)

	if err := symlinkSkillsDir(homeDir, relPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	link := filepath.Join(homeDir, ".agents", "skills")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", link, err)
	}
	if !filepath.IsAbs(target) {
		t.Errorf("symlink target should be absolute, got %q", target)
	}
}

func TestParseHubTokenID(t *testing.T) {
	tests := []struct {
		name       string
		hubTokenID string
		wantID     uint
		wantOK     bool
	}{
		{"empty", "", 0, false},
		{"valid", "42", 42, true},
		{"non-numeric", "abc", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{HubTokenID: tt.hubTokenID}
			id, ok := parseHubTokenID(cfg)
			if id != tt.wantID || ok != tt.wantOK {
				t.Errorf("parseHubTokenID() = (%d, %v), want (%d, %v)", id, ok, tt.wantID, tt.wantOK)
			}
		})
	}
}

func TestIsIntermediateWorkflowStage(t *testing.T) {
	tests := []struct {
		name               string
		workflowStage      string
		workflowStageCount string
		want               bool
	}{
		{"standalone run", "", "", false},
		{"last stage 3/3", "3", "3", false},
		{"intermediate 1/3", "1", "3", true},
		{"intermediate 1/2", "1", "2", true},
		{"intermediate 2/3", "2", "3", true},
		{"single stage 1/1", "1", "1", false},
		{"stage only", "1", "", false},
		{"count only", "", "3", false},
		{"stage zero", "0", "3", false},
		{"count zero", "1", "0", false},
		{"non-numeric stage", "abc", "3", false},
		{"non-numeric count", "1", "xyz", false},
		{"stage exceeds count", "5", "3", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				WorkflowStage:      tt.workflowStage,
				WorkflowStageCount: tt.workflowStageCount,
			}
			if got := isIntermediateWorkflowStage(cfg); got != tt.want {
				t.Errorf("isIntermediateWorkflowStage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTokenRevocationDecision(t *testing.T) {
	tests := []struct {
		name               string
		hubTokenID         string
		workflowStage      string
		workflowStageCount string
		stageSucceeded     bool
		wantRevoke         bool
	}{
		{"no token ID", "", "", "", false, false},
		{"standalone success", "1", "", "", true, true},
		{"standalone failure", "1", "", "", false, true},
		{"last stage success", "1", "3", "3", true, true},
		{"last stage failure", "1", "3", "3", false, true},
		{"intermediate success — defer to next stage", "1", "1", "3", true, false},
		{"intermediate failure — revoke (#109)", "1", "1", "3", false, true},
		{"single-stage success", "1", "1", "1", true, true},
		{"single-stage failure", "1", "1", "1", false, true},
		{"non-numeric token ID", "abc", "", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				HubTokenID:         tt.hubTokenID,
				WorkflowStage:      tt.workflowStage,
				WorkflowStageCount: tt.workflowStageCount,
			}
			_, hasToken := parseHubTokenID(cfg)
			if !hasToken {
				if tt.wantRevoke {
					t.Error("expected revocation but no token ID available")
				}
				return
			}
			intermediate := isIntermediateWorkflowStage(cfg)
			shouldRevoke := !(intermediate && tt.stageSucceeded)
			if shouldRevoke != tt.wantRevoke {
				t.Errorf("revocation decision = %v, want %v (intermediate=%v, stageSucceeded=%v)",
					shouldRevoke, tt.wantRevoke, intermediate, tt.stageSucceeded)
			}
		})
	}
}

func TestFetchAndWriteAnalysis(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/applications/42/analysis/insights" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id": 1, "description": "sample insight"}]`))
	}))
	defer server.Close()

	workDir := t.TempDir()
	hubClient := hub.NewClient(server.URL, "token")
	_, err := fetchAndWriteAnalysis(hubClient, "42", workDir)
	if err != nil {
		t.Fatalf("fetchAndWriteAnalysis failed: %v", err)
	}

	analysisFile := filepath.Join(workDir, ".konveyor", "analysis.json")
	data, err := os.ReadFile(analysisFile)
	if err != nil {
		t.Fatalf("reading analysis.json failed: %v", err)
	}
	if !strings.Contains(string(data), "sample insight") {
		t.Errorf("unexpected content: %s", string(data))
	}
}

func TestPlanTaskRung(t *testing.T) {
	tests := []struct {
		name  string
		cfg   config.Config
		turns int
		want  string
	}{
		{
			// 40 configured, 34 native — the prompt stops at 34 and the
			// rest is the wind-down reserve, so 34 is what a viewer is told.
			name:  "progress against the native ceiling, not the configured budget",
			cfg:   config.Config{Model: "m", MaxTurns: 40},
			turns: 12,
			want:  "Agent works its standing prompt (m, turn 12 of 34)",
		},
		{
			name:  "the last turn of the prompt reads as the last turn",
			cfg:   config.Config{Model: "m", MaxTurns: 200},
			turns: 170,
			want:  "Agent works its standing prompt (m, turn 170 of 170)",
		},
		{
			// Once the handoff prompt spends the reserve, turnsUsed passes
			// the native ceiling; the configured budget is then the only
			// denominator that is not already behind us.
			name:  "wind-down past the native ceiling falls back to the configured budget",
			cfg:   config.Config{Model: "m", MaxTurns: 200},
			turns: 178,
			want:  "Agent works its standing prompt (m, turn 178 of 200)",
		},
		{
			name:  "progress without a budget",
			cfg:   config.Config{Model: "m"},
			turns: 12,
			want:  "Agent works its standing prompt (m, 12 turns)",
		},
		{
			name:  "first turn without a budget",
			cfg:   config.Config{},
			turns: 1,
			want:  "Agent works its standing prompt (1 turn)",
		},
		{
			name: "standalone run with instructions",
			cfg: config.Config{
				Model: "claude-sonnet-4-5", MaxTurns: 40,
				StageInstructions: "Assess the coolstore repository for Quarkus migration.\nList blockers.",
			},
			want: `Agent works the task: “Assess the coolstore repository for Quarkus migration. List blockers.” (claude-sonnet-4-5, up to 34 turns)`,
		},
		{
			name: "workflow stage prefix",
			cfg: config.Config{
				WorkflowStage: "2", WorkflowStageCount: "3",
				Model: "gemini-2.5-pro", MaxTurns: 200,
				StageInstructions: "## Remediate\n\nFix the findings from the assess stage.",
			},
			// Both Track A changes land on this one line: the excerpt
			// is the paragraph under the heading rather than the heading,
			// and the budget is the runtime's native ceiling rather than
			// the configured maximum.
			want: `Stage 2 of 3 — agent works the task: “Fix the findings from the assess stage.” (gemini-2.5-pro, up to 170 turns)`,
		},
		{
			name: "no instructions: the agent prompt is not quoted",
			cfg: config.Config{
				Model:       "m",
				AgentPrompt: "\n\n- You are a Java migration agent.",
			},
			want: `Agent works its standing prompt (m)`,
		},
		{
			name: "no instructions on a workflow stage",
			cfg:  config.Config{WorkflowStage: "1", WorkflowStageCount: "2", Model: "m", MaxTurns: 10},
			want: `Stage 1 of 2 — agent works its standing prompt (m, up to 8 turns)`,
		},
		{
			// NativeTurnLimit floors at 1, so a one-turn budget does not
			// render as "up to 0 turns".
			name: "a one-turn budget keeps its turn",
			cfg:  config.Config{Model: "m", MaxTurns: 1},
			want: `Agent works its standing prompt (m, up to 1 turn)`,
		},
		{
			name: "hard-wrapped paragraph is joined before the cut",
			cfg: config.Config{
				Model:             "m",
				StageInstructions: "Migrate the coolstore services to\nQuarkus, one module at a time,\nand keep the tests green.\n\nSecond paragraph is not quoted.",
			},
			want: `Agent works the task: “Migrate the coolstore services to Quarkus, one module at a time, and keep the t…” (m)`,
		},
		{
			name: "long line is cut",
			cfg: config.Config{
				StageInstructions: strings.Repeat("word ", 40),
			},
			want: `Agent works the task: “` + strings.TrimSpace(strings.Repeat("word ", 40)[:79]) + `…”`,
		},
		{
			name: "no text, no model, no budget",
			cfg:  config.Config{},
			want: "Agent works its standing prompt",
		},
		{
			name: "invalid stage metadata is ignored",
			cfg:  config.Config{WorkflowStage: "5", WorkflowStageCount: "3", Model: "m"},
			want: "Agent works its standing prompt (m)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planTaskRung(&tt.cfg, nil, tt.turns); got != tt.want {
				t.Errorf("planTaskRung() =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
}

func TestTaskSummary(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "a heading is skipped for the paragraph under it",
			text: "# Stage 2: CDI migration\n\nReplace every EJB session bean with a CDI equivalent.",
			want: "Replace every EJB session bean with a CDI equivalent.",
		},
		{
			name: "a labelled preamble is skipped",
			text: "Context: the app is a JEE6 monolith on WildFly 10.\n\nPort it to Quarkus, module by module.",
			want: "Port it to Quarkus, module by module.",
		},
		{
			name: "heading then preamble then the work",
			text: "## Remediate\n\nBackground: the assess stage found 50 insights.\n\nFix them, highest effort first.",
			want: "Fix them, highest effort first.",
		},
		{
			name: "a heading with nothing under it is still better than nothing",
			text: "# Stage 2: CDI migration\n",
			want: "Stage 2: CDI migration",
		},
		{
			name: "a preamble with nothing after it is used as-is",
			text: "Context: the app is a JEE6 monolith.",
			want: "Context: the app is a JEE6 monolith.",
		},
		{
			name: "the label word without a colon is ordinary prose",
			text: "Context matters: pick the target that fits the app.\n\nSecond paragraph.",
			want: "Context matters: pick the target that fits the app.",
		},
		{
			name: "a heading run together with its text is one sentence, not a title",
			text: "# Migrate the order service\nand keep the tests green.\n\nSecond paragraph.",
			want: "Migrate the order service and keep the tests green.",
		},
		{
			name: "hard-wrapped prose is joined, later paragraphs dropped",
			text: "Migrate the coolstore services to\nQuarkus, one module at a time.\n\nNot this.",
			want: "Migrate the coolstore services to Quarkus, one module at a time.",
		},
		{
			name: "list markers are stripped from the opening line",
			text: "- Replace the JMS listeners with SmallRye Reactive Messaging.",
			want: "Replace the JMS listeners with SmallRye Reactive Messaging.",
		},
		{
			name: "a rule line opens nothing",
			text: "---\nMigrate the catalog service.",
			want: "Migrate the catalog service.",
		},
		{
			name: "blank",
			text: "\n  \n",
			want: "",
		},
		{
			name: "the chosen paragraph is still cut to length",
			text: "## Title\n\n" + strings.Repeat("word ", 40),
			want: strings.TrimSpace(strings.Repeat("word ", 40)[:taskSummaryMaxLen-1]) + "…",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskSummary(tt.text); got != tt.want {
				t.Errorf("taskSummary() =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
}

// TestPlanTaskRungRedactsTokenAcrossCutoff places a known token so the
// 80-rune excerpt cutoff falls inside it. Redacting after the cut would
// leave the token's head in the rung (and in the tee's replay ring).
func TestPlanTaskRungRedactsTokenAcrossCutoff(t *testing.T) {
	const secret = "ghp_supersecrettoken1234567890"
	red := &redactor{secrets: []string{secret}}
	// 60 runes of filler, then the 30-rune token: the cut at 79 lands
	// nineteen runes into it, while the "[redacted]" marker that replaces
	// it still fits inside the excerpt.
	filler := strings.Repeat("x", 60)
	cfg := &config.Config{StageInstructions: filler + secret + " and more text after it."}
	got := planTaskRung(cfg, red, 0)
	if strings.Contains(got, secret[:4]) {
		t.Fatalf("token head leaked past the excerpt cutoff: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("expected the redaction marker in %q", got)
	}
	// The agent prompt is never quoted, so a token there cannot leak.
	cfg = &config.Config{AgentPrompt: filler + secret}
	if got := planTaskRung(cfg, red, 0); strings.Contains(got, secret[:4]) {
		t.Fatalf("token from the agent prompt reached the rung: %q", got)
	}
}

func TestPlanTaskRungRedactsSecrets(t *testing.T) {
	red := &redactor{secrets: []string{"ghp_supersecrettoken"}}
	cfg := &config.Config{StageInstructions: "Push using ghp_supersecrettoken to the fork."}
	got := planTaskRung(cfg, red, 0)
	if strings.Contains(got, "ghp_supersecrettoken") {
		t.Fatalf("secret leaked into plan rung: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("expected redaction marker in %q", got)
	}
}

func TestPlanPrepRung(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		branch  string
		count   int
		want    string
	}{
		{
			name:    "repo, branch and insights",
			repoURL: "https://github.com/konveyor/coolstore.git", branch: "migration-1", count: 49,
			want: "Prepare workspace: github.com/konveyor/coolstore on branch migration-1, 49 analysis insights",
		},
		{
			name:    "embedded credentials are dropped",
			repoURL: "https://user:ghp_supersecrettoken@github.com/konveyor/coolstore", branch: "b", count: -1,
			want: "Prepare workspace: github.com/konveyor/coolstore on branch b",
		},
		{
			name:    "zero insights is said, unfetched is not",
			repoURL: "https://github.com/k/r", branch: "b", count: 0,
			want: "Prepare workspace: github.com/k/r on branch b, no analysis insights",
		},
		{
			name:    "single insight",
			repoURL: "https://github.com/k/r", branch: "b", count: 1,
			want: "Prepare workspace: github.com/k/r on branch b, 1 analysis insight",
		},
		{
			name:    "unparseable url falls back to clone",
			repoURL: "::not a url", branch: "b", count: -1,
			want: "Prepare workspace: clone on branch b",
		},
		{
			name:  "nothing known",
			count: -1,
			want:  "Prepare workspace: clone",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planPrepRung(tt.repoURL, tt.branch, tt.count, nil); got != tt.want {
				t.Errorf("planPrepRung() =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
}

func TestPlanPushRung(t *testing.T) {
	tests := []struct {
		name    string
		branch  string
		pushes  int
		commits int
		done    bool
		want    string
	}{
		{
			name: "nothing pushed yet", branch: "migration-1", commits: -1,
			want: "Push results to branch migration-1",
		},
		{
			name: "one push landed", branch: "migration-1", pushes: 1, commits: -1,
			want: "Push results to branch migration-1 — 1 push so far",
		},
		{
			name: "the watcher has been busy", branch: "migration-1", pushes: 17, commits: -1,
			want: "Push results to branch migration-1 — 17 pushes so far",
		},
		{
			name: "finished with a count", branch: "migration-1", pushes: 4, commits: 7, done: true,
			want: "Pushed 7 commits to branch migration-1",
		},
		{
			name: "finished with one commit", branch: "migration-1", pushes: 1, commits: 1, done: true,
			want: "Pushed 1 commit to branch migration-1",
		},
		{
			name: "finished having produced nothing", branch: "migration-1", commits: 0, done: true,
			want: "No changes to push to branch migration-1",
		},
		{
			name: "finished but the count is unknown", branch: "migration-1", pushes: 2, commits: -1, done: true,
			want: "Pushed results to branch migration-1",
		},
		{
			name: "no branch name to show", pushes: 1, commits: 2, done: true,
			want: "Pushed 2 commits to the run branch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planPushRung(tt.branch, tt.pushes, tt.commits, tt.done); got != tt.want {
				t.Errorf("planPushRung() =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
}
