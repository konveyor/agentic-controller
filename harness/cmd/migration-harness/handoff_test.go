package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// executeRefused is the handoff from #241: the execute skill found no plan
// to execute, recorded that, and ended its turn normally.
const executeRefused = `## Execute
- Status: failed

| Step | File | Action | Result | Error |
|------|------|--------|--------|-------|
| - | - | - | failed | docs/plan.md not found |
`

const executeCompleted = `## Execute
- Status: completed

| Step | File | Action | Result | Error |
|------|------|--------|--------|-------|
| 1 | src/main/java/App.java | MODIFY | applied | — |
| 5 | src/main/java/Legacy.java | MODIFY | failed | file missing |
`

const verifyFailed = `## Verify
- Status: failed
- Build: failed (rounds: 3, remaining errors: App.java:12 cannot find symbol)
- Tests: skipped
- Summary: build still broken after 3 rounds
`

func TestParseHandoffVerdict(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		wantOK     bool
		wantStatus string
		wantDetail string
	}{
		{
			name:       "execute refused: detail from the failed row's error cell",
			content:    executeRefused,
			wantOK:     true,
			wantStatus: "failed",
			wantDetail: "docs/plan.md not found",
		},
		{
			// A completed stage with a failed step is still completed — the
			// step table is the skill's own bookkeeping, not the verdict.
			name:       "execute completed with a failed step keeps its status",
			content:    executeCompleted,
			wantOK:     true,
			wantStatus: "completed",
			wantDetail: "file missing",
		},
		{
			name:       "verify failed: detail from the Summary line",
			content:    verifyFailed,
			wantOK:     true,
			wantStatus: "failed",
			wantDetail: "build still broken after 3 rounds",
		},
		{
			// Each stage appends its section, so the last one is this
			// stage's: an earlier refusal does not taint a later stage.
			name:       "last section wins: execute failed then verify passed",
			content:    executeRefused + "\n" + strings.Replace(verifyFailed, "Status: failed", "Status: passed", 1),
			wantOK:     true,
			wantStatus: "passed",
			wantDetail: "build still broken after 3 rounds",
		},
		{
			name:       "last section wins: execute completed then verify failed",
			content:    executeCompleted + "\n" + verifyFailed,
			wantOK:     true,
			wantStatus: "failed",
			wantDetail: "build still broken after 3 rounds",
		},
		{
			name:       "inline reason on the Status line beats the table",
			content:    "## Execute\n- Status: failed — nothing to migrate in this module\n\n| Step | Result | Error |\n|---|---|---|\n| 1 | failed | later |\n",
			wantOK:     true,
			wantStatus: "failed",
			wantDetail: "nothing to migrate in this module",
		},
		{
			name:       "bold label and upper case",
			content:    "## Execute\n* **Status**: FAILED\n",
			wantOK:     true,
			wantStatus: "failed",
		},
		{
			name:       "bold label with the colon inside",
			content:    "## Verify\n- **Status:** passed\n",
			wantOK:     true,
			wantStatus: "passed",
		},
		{
			name:       "sub-headings stay inside the section",
			content:    "## Execute\n- Status: failed\n\n### Steps\n\n| Step | Result | Error |\n|---|---|---|\n| 2 | failed | pom.xml unreadable |\n",
			wantOK:     true,
			wantStatus: "failed",
			wantDetail: "pom.xml unreadable",
		},
		{
			name:       "no heading at all: the whole document is the section",
			content:    "Status: failed\nReason: repository has no Java sources\n",
			wantOK:     true,
			wantStatus: "failed",
			wantDetail: "repository has no Java sources",
		},
		{
			name:       "CRLF line endings",
			content:    "## Execute\r\n- Status: failed\r\n\r\n| Step | Result | Error |\r\n|---|---|---|\r\n| - | failed | docs/plan.md not found |\r\n",
			wantOK:     true,
			wantStatus: "failed",
			wantDetail: "docs/plan.md not found",
		},
		{
			name:       "placeholder error cell gives no detail",
			content:    "## Execute\n- Status: failed\n\n| Step | Result | Error |\n|---|---|---|\n| 1 | failed | — |\n",
			wantOK:     true,
			wantStatus: "failed",
			wantDetail: "",
		},
		{
			name:       "status the harness does not recognise is reported as written",
			content:    "## Execute\n- Status: blocked\n",
			wantOK:     true,
			wantStatus: "blocked",
		},
		{
			name:    "section without a Status line",
			content: "## Execute\n\n| Step | Result | Error |\n|---|---|---|\n| 1 | failed | x |\n",
			wantOK:  false,
		},
		{
			name:    "a Status line in an earlier section does not count",
			content: executeRefused + "\n## Verify\n- Build: passed\n",
			wantOK:  false,
		},
		{
			name:    "empty document",
			content: "",
			wantOK:  false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseHandoffVerdict(c.content)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (verdict %+v)", ok, c.wantOK, got)
			}
			if !ok {
				return
			}
			if got.Status != c.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, c.wantStatus)
			}
			if got.Detail != c.wantDetail {
				t.Errorf("Detail = %q, want %q", got.Detail, c.wantDetail)
			}
		})
	}
}

func TestParseHandoffVerdictClipsLongDetail(t *testing.T) {
	long := strings.Repeat("x", handoffDetailLen+50)
	got, ok := parseHandoffVerdict("## Execute\n- Status: failed\n- Reason: " + long + "\n")
	if !ok {
		t.Fatal("expected a verdict")
	}
	if r := []rune(got.Detail); len(r) != handoffDetailLen || !strings.HasSuffix(got.Detail, "…") {
		t.Errorf("Detail = %d runes ending %q, want %d runes ending with an ellipsis",
			len(r), got.Detail[len(got.Detail)-3:], handoffDetailLen)
	}
}

func TestHandoffVerdictRecognised(t *testing.T) {
	for status, want := range map[string]bool{
		"failed": true, "completed": true, "passed": true, "succeeded": true, "done": true,
		"blocked": false, "partial": false, "": false,
	} {
		v := handoffVerdict{Status: status}
		if got := v.recognised(); got != want {
			t.Errorf("handoffVerdict{Status: %q}.recognised() = %v, want %v", status, got, want)
		}
		if status == "failed" && !v.failed() {
			t.Errorf("handoffVerdict{Status: %q}.failed() = false", status)
		}
	}
}

// handoffRepo initialises a repository under a temp dir with one commit
// (README only) and returns its path, the repository and the base SHA.
func handoffRepo(t *testing.T) (string, *gogit.Repository, string) {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	hash, err := wt.Commit("initial", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return dir, repo, hash.String()
}

func writeHandoff(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".konveyor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, handoffPath), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, repo *gogit.Repository, msg string) string {
	t.Helper()
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
		t.Fatal(err)
	}
	hash, err := wt.Commit(msg, &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return hash.String()
}

func TestStageHandoffReadsThisStagesVerdict(t *testing.T) {
	dir, repo, base := handoffRepo(t)

	// No handoff at all: nothing to read.
	if _, ok := stageHandoff(dir, repo, base); ok {
		t.Error("expected no verdict without a handoff file")
	}

	// The stage wrote and committed its refusal (#241).
	writeHandoff(t, dir, executeRefused)
	commitAll(t, repo, "execute: refused")
	v, ok := stageHandoff(dir, repo, base)
	if !ok || !v.failed() || v.Detail != "docs/plan.md not found" {
		t.Errorf("committed refusal: got (%+v, %v)", v, ok)
	}

	// Uncommitted counts too: the file is the stage's verdict whether or
	// not the agent remembered to commit it.
	dir2, repo2, base2 := handoffRepo(t)
	writeHandoff(t, dir2, executeRefused)
	v, ok = stageHandoff(dir2, repo2, base2)
	if !ok || !v.failed() {
		t.Errorf("uncommitted refusal: got (%+v, %v)", v, ok)
	}
}

func TestStageHandoffIgnoresAnEarlierStagesVerdict(t *testing.T) {
	dir, repo, _ := handoffRepo(t)

	// A previous stage (or run) left a refusal on the branch; this stage's
	// base is the commit that carries it, and this stage does not touch the
	// file — its verdict must not be re-read as this stage's.
	writeHandoff(t, dir, executeRefused)
	base := commitAll(t, repo, "execute: refused")
	if v, ok := stageHandoff(dir, repo, base); ok {
		t.Errorf("stale handoff read as this stage's verdict: %+v", v)
	}

	// Appending a section makes it this stage's file again, and the new
	// section is the verdict.
	writeHandoff(t, dir, executeRefused+"\n## Verify\n- Status: passed\n")
	v, ok := stageHandoff(dir, repo, base)
	if !ok || v.Status != "passed" {
		t.Errorf("appended section: got (%+v, %v)", v, ok)
	}
}

func TestStageHandoffWithUnknownBaseReadsTheFile(t *testing.T) {
	// An empty base fails open, as Push does: the file is taken as this
	// stage's output.
	dir, repo, _ := handoffRepo(t)
	writeHandoff(t, dir, executeRefused)
	v, ok := stageHandoff(dir, repo, "")
	if !ok || !v.failed() {
		t.Errorf("got (%+v, %v), want the refusal", v, ok)
	}
}
