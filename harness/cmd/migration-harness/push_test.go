package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	gogit "github.com/go-git/go-git/v5"
	gogitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/konveyor/migration-harness/internal/git"
)

func intPtr(n int) *int { return &n }

// sameCount compares two optional counts: both absent, or both present
// and equal. Absent and zero are different answers.
func sameCount(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func showCount(n *int) string {
	if n == nil {
		return "absent"
	}
	return fmt.Sprint(*n)
}

func TestRecordPush(t *testing.T) {
	const branch, base, tip = "fix-1", "b45e0000", "71p00000"
	tests := []struct {
		name string
		in   finalPush
		want pushReport
		// reason is a fragment the Reason must contain. Empty means the
		// record must carry no reason at all.
		reason string
	}{
		{
			name: "the final push carried the commits",
			in:   finalPush{outcome: git.PushAdvanced, landed: 1, tip: tip, commits: 3},
			want: pushReport{Status: pushPushed, Commit: tip, Commits: intPtr(3)},
		},
		{
			name: "the watcher got there first",
			in:   finalPush{outcome: git.PushUpToDate, landed: 4, tip: tip, commits: 7},
			want: pushReport{Status: pushPushed, Commit: tip, Commits: intPtr(7)},
		},
		{
			name: "pushed, but the count could not be made",
			in:   finalPush{outcome: git.PushAdvanced, landed: 1, tip: tip, commits: -1},
			want: pushReport{Status: pushPushed, Commit: tip},
		},
		{
			name: "pushed, but the tip would not resolve",
			in:   finalPush{outcome: git.PushAdvanced, landed: 1, commits: 2},
			want: pushReport{Status: pushPushed, Commits: intPtr(2)},
		},
		{
			name: "the run made no commits",
			in:   finalPush{outcome: git.PushSkipped, tip: base, commits: 0},
			want: pushReport{Status: pushNoChanges, Commits: intPtr(0)},
		},
		{
			name:   "the agent moved the branch back after the watcher pushed",
			in:     finalPush{outcome: git.PushSkipped, landed: 2, tip: base, commits: 0},
			want:   pushReport{Status: pushNoChanges, Commits: intPtr(0)},
			reason: "the remote still holds what this run pushed earlier (pushes that landed: 2)",
		},
		{
			// Commits past the base that this run did not push are not
			// this run's: neither claimed as pushed nor counted.
			name:   "the remote already held the tip and nothing of this run landed",
			in:     finalPush{outcome: git.PushUpToDate, tip: tip, commits: 4},
			want:   pushReport{Status: pushNoChanges},
			reason: "no push of this run moved it",
		},
		{
			// What the remote holds after a failed push is not something
			// the harness knows, so neither a commit nor a count is named
			// even though both were on hand.
			name: "the final push failed",
			in: finalPush{
				err:    errors.New("push fix-1: non-fast-forward update: refs/heads/fix-1"),
				landed: 1, tip: tip, commits: 3,
			},
			want:   pushReport{Status: pushFailed},
			reason: "non-fast-forward update",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recordPush(branch, base, tt.in, newRedactor())
			if got.Status != tt.want.Status {
				t.Errorf("status = %q, want %q", got.Status, tt.want.Status)
			}
			if got.Commit != tt.want.Commit {
				t.Errorf("commit = %q, want %q", got.Commit, tt.want.Commit)
			}
			if !sameCount(got.Commits, tt.want.Commits) {
				t.Errorf("commits = %s, want %s", showCount(got.Commits), showCount(tt.want.Commits))
			}
			if got.Branch != branch || got.Base != base {
				t.Errorf("branch, base = %q, %q; want %q, %q", got.Branch, got.Base, branch, base)
			}
			switch {
			case tt.reason == "" && got.Reason != "":
				t.Errorf("reason = %q, want none", got.Reason)
			case !strings.Contains(got.Reason, tt.reason):
				t.Errorf("reason = %q, want it to contain %q", got.Reason, tt.reason)
			}
			if got.pushed() != (tt.want.Status == pushPushed) {
				t.Errorf("pushed() = %v for status %q", got.pushed(), got.Status)
			}
		})
	}
}

// TestRecordPushRedactsAndBoundsTheError: the error text lands on
// AgentRun.status, where anyone who can read the run reads it, and in a
// blob the kubelet cuts at 4096 bytes.
func TestRecordPushRedactsAndBoundsTheError(t *testing.T) {
	const token = "ghp_s3cr3tTokenValue0123456789"
	pushErr := errors.New("push b: https://x-access-token:" + token + "@github.com/o/r.git: " +
		"remote: " + strings.Repeat("世", 400))
	got := recordPush("b", "", finalPush{err: pushErr}, newRedactor(token))

	if strings.Contains(got.Reason, token) {
		t.Errorf("reason carries the credential: %q", got.Reason)
	}
	if !strings.HasSuffix(got.Reason, "…") {
		t.Errorf("a cut reason should say it was cut: %q", got.Reason)
	}
	if n := len(strings.TrimSuffix(got.Reason, "…")); n > pushReasonMaxBytes {
		t.Errorf("reason is %d bytes before the mark, want at most %d", n, pushReasonMaxBytes)
	}
	if !utf8.ValidString(got.Reason) {
		t.Errorf("reason was cut inside a rune: %q", got.Reason)
	}
}

// TestPushRecordSaysOnlyWhatWasMeasured is the JSON a consumer reads.
// Absent means unknown and present means measured, so a count of zero has
// to survive and an uncounted run must not publish one.
func TestPushRecordSaysOnlyWhatWasMeasured(t *testing.T) {
	keys := func(r *pushReport) map[string]any {
		t.Helper()
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	noChanges := keys(recordPush("b", "base", finalPush{outcome: git.PushSkipped, commits: 0}, nil))
	if noChanges["commits"] != float64(0) {
		t.Errorf("a measured zero published commits=%v, want 0", noChanges["commits"])
	}
	for _, k := range []string{"commit", "reason"} {
		if _, ok := noChanges[k]; ok {
			t.Errorf("a run that pushed nothing published %s=%v, want the key absent", k, noChanges[k])
		}
	}

	uncounted := keys(recordPush("b", "", finalPush{outcome: git.PushAdvanced, landed: 1, tip: "tip", commits: -1}, nil))
	if _, ok := uncounted["commits"]; ok {
		t.Errorf("an uncounted push published commits=%v, want the key absent", uncounted["commits"])
	}
	if _, ok := uncounted["base"]; ok {
		t.Errorf("an unresolved base published base=%v, want the key absent", uncounted["base"])
	}
	if uncounted["status"] != pushPushed || uncounted["commit"] != "tip" || uncounted["branch"] != "b" {
		t.Errorf("pushed record = %v", uncounted)
	}

	failed := keys(recordPush("b", "base", finalPush{err: errors.New("rejected"), tip: "tip", commits: 2}, nil))
	for _, k := range []string{"commit", "commits"} {
		if _, ok := failed[k]; ok {
			t.Errorf("a failed push published %s=%v, want the key absent", k, failed[k])
		}
	}
	if failed["status"] != pushFailed || failed["reason"] != "rejected" {
		t.Errorf("failed record = %v", failed)
	}
}

// TestPushRecordAndClosingRungAgree: runStage hands the closing rung the
// record's count rather than the raw one, so a viewer is never told
// "Pushed 4 commits" by a run whose record says nothing of it was pushed.
func TestPushRecordAndClosingRungAgree(t *testing.T) {
	cases := []finalPush{
		{outcome: git.PushAdvanced, landed: 1, tip: "t", commits: 3},
		{outcome: git.PushUpToDate, landed: 2, tip: "t", commits: 1},
		{outcome: git.PushAdvanced, landed: 1, tip: "t", commits: -1},
		{outcome: git.PushSkipped, commits: 0},
		{outcome: git.PushSkipped, landed: 3, commits: 0},
		{outcome: git.PushUpToDate, tip: "t", commits: 4},
		{outcome: git.PushUpToDate, tip: "t", commits: -1},
	}
	for _, p := range cases {
		rec := recordPush("b", "base", p, nil)
		// What runStage does before it emits the closing ladder.
		commits := -1
		if rec.Commits != nil {
			commits = *rec.Commits
		}
		rung := planPushRung("b", p.landed, commits, true)
		if strings.HasPrefix(rung, "Pushed") != rec.pushed() {
			t.Errorf("%+v: the rung says %q and the record says %q", p, rung, rec.Status)
		}
		if rec.Status == pushNoChanges && !strings.HasPrefix(rung, "No changes to push") {
			t.Errorf("%+v: the record says noChanges and the rung says %q", p, rung)
		}
	}
}

func TestPushReportVerdictOnly(t *testing.T) {
	var none *pushReport
	if none.verdictOnly() != nil || none.pushed() {
		t.Error("a run with no push record must stay without one, and is not pushed")
	}
	full := &pushReport{
		Status: pushPushed, Reason: "r", Branch: "b", Base: "base", Commit: "tip", Commits: intPtr(2),
	}
	got := full.verdictOnly()
	if got.Status != pushPushed || got.Commit != "tip" || !sameCount(got.Commits, intPtr(2)) {
		t.Errorf("verdict lost: %+v", got)
	}
	if got.Reason != "" || got.Branch != "" || got.Base != "" {
		t.Errorf("verdictOnly kept context it is meant to shed: %+v", got)
	}
}

// pushWorkspace builds what runStage has before the agent's turn: a
// remote holding one commit, a clone of it on the run's branch, and the
// commit the run starts from.
func pushWorkspace(t *testing.T, branch string) (cred *git.Credentials, remoteDir string, repo *gogit.Repository, cloneDir, base string) {
	t.Helper()
	remoteDir = filepath.Join(t.TempDir(), "remote.git")
	if _, err := gogit.PlainInit(remoteDir, true); err != nil {
		t.Fatalf("init remote: %v", err)
	}
	seedDir := filepath.Join(t.TempDir(), "seed")
	seed, err := gogit.PlainInit(seedDir, false)
	if err != nil {
		t.Fatalf("init seed: %v", err)
	}
	commitFile(t, seed, seedDir, "README.md")
	if _, err := seed.CreateRemote(&gogitcfg.RemoteConfig{Name: "origin", URLs: []string{remoteDir}}); err != nil {
		t.Fatalf("seed remote: %v", err)
	}
	if err := seed.Push(&gogit.PushOptions{}); err != nil {
		t.Fatalf("seed push: %v", err)
	}

	cred = &git.Credentials{Username: "test", Token: "token", RepoURL: remoteDir, Branch: branch}
	cloneDir = filepath.Join(t.TempDir(), "work")
	repo, err = git.Clone(context.Background(), cred, cloneDir)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if err := git.CheckoutBranch(repo, branch); err != nil {
		t.Fatalf("checkout %s: %v", branch, err)
	}
	base, err = git.HeadSHA(repo)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	return cred, remoteDir, repo, cloneDir, base
}

// commitFile writes name into dir and commits it, returning the commit.
func commitFile(t *testing.T, repo *gogit.Repository, dir, name string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add(name); err != nil {
		t.Fatal(err)
	}
	hash, err := wt.Commit("add "+name, &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("commit %s: %v", name, err)
	}
	return hash.String()
}

// remoteTip is what the remote holds for branch: "" when it has no such
// branch.
func remoteTip(t *testing.T, remoteDir, branch string) string {
	t.Helper()
	remote, err := gogit.PlainOpen(remoteDir)
	if err != nil {
		t.Fatalf("open remote: %v", err)
	}
	ref, err := remote.Reference(plumbing.NewBranchReferenceName(branch), false)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return ""
	}
	if err != nil {
		t.Fatalf("remote branch %s: %v", branch, err)
	}
	return ref.Hash().String()
}

// TestPushRecordAgainstARealRemote drives the record through a run's
// life with real pushes, and holds each claim against what the remote
// holds rather than against what the harness believes it sent.
func TestPushRecordAgainstARealRemote(t *testing.T) {
	const branch = "fix-1"
	ctx := context.Background()
	cred, remoteDir, repo, cloneDir, base := pushWorkspace(t, branch)

	// Counts the way runStage's emitPush does: a push lands when it
	// moves the remote.
	landed := 0
	push := func() *pushReport {
		t.Helper()
		outcome, err := git.Push(ctx, cred, repo, branch, base)
		if err != nil {
			return recordPush(branch, base, finalPush{outcome: outcome, err: err, landed: landed}, nil)
		}
		if outcome == git.PushAdvanced {
			landed++
		}
		return recordPush(branch, base, observePush(repo, branch, base, outcome, landed), nil)
	}

	t.Run("no commits", func(t *testing.T) {
		rec := push()
		if rec.Status != pushNoChanges || !sameCount(rec.Commits, intPtr(0)) || rec.Commit != "" || rec.Reason != "" {
			t.Errorf("record = %+v, want noChanges with a measured zero and nothing else", rec)
		}
		if rec.Base != base || rec.Branch != branch {
			t.Errorf("base, branch = %q, %q; want %q, %q", rec.Base, rec.Branch, base, branch)
		}
		if got := remoteTip(t, remoteDir, branch); got != "" {
			t.Errorf("the remote has branch %s at %s after a run that changed nothing", branch, got)
		}
	})

	var pushedTip string
	t.Run("two commits", func(t *testing.T) {
		commitFile(t, repo, cloneDir, "one.txt")
		pushedTip = commitFile(t, repo, cloneDir, "two.txt")
		rec := push()
		if rec.Status != pushPushed || !sameCount(rec.Commits, intPtr(2)) || rec.Reason != "" {
			t.Errorf("record = %+v, want pushed with 2 commits", rec)
		}
		if got := remoteTip(t, remoteDir, branch); rec.Commit != got || got != pushedTip {
			t.Errorf("record names %s, the remote holds %s, the run committed %s", rec.Commit, got, pushedTip)
		}
	})

	t.Run("a final push after the watcher got there", func(t *testing.T) {
		rec := push()
		if rec.Status != pushPushed || rec.Commit != pushedTip || !sameCount(rec.Commits, intPtr(2)) {
			t.Errorf("record = %+v, want pushed at %s with 2 commits", rec, pushedTip)
		}
	})

	t.Run("the agent moves the branch back", func(t *testing.T) {
		wt, err := repo.Worktree()
		if err != nil {
			t.Fatal(err)
		}
		if err := wt.Reset(&gogit.ResetOptions{Commit: plumbing.NewHash(base), Mode: gogit.HardReset}); err != nil {
			t.Fatalf("reset to base: %v", err)
		}
		rec := push()
		if rec.Status != pushNoChanges || rec.Commit != "" {
			t.Errorf("record = %+v, want noChanges and no commit claimed", rec)
		}
		if !strings.Contains(rec.Reason, "the remote still holds") {
			t.Errorf("reason = %q, want it to say the remote is not where the branch is", rec.Reason)
		}
		if got := remoteTip(t, remoteDir, branch); got != pushedTip {
			t.Errorf("the remote moved to %s; it should still hold %s", got, pushedTip)
		}
	})

	t.Run("a push the remote rejects", func(t *testing.T) {
		// The branch now diverges from what the remote holds.
		commitFile(t, repo, cloneDir, "other.txt")
		rec := push()
		if rec.Status != pushFailed || rec.Reason == "" {
			t.Errorf("record = %+v, want failed with the remote's reason", rec)
		}
		if rec.Commit != "" || rec.Commits != nil {
			t.Errorf("a failed push claimed commit %q and count %s", rec.Commit, showCount(rec.Commits))
		}
		if got := remoteTip(t, remoteDir, branch); got != pushedTip {
			t.Errorf("the remote moved to %s on a rejected push; it should still hold %s", got, pushedTip)
		}
	})
}
