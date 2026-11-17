package main

import (
	"fmt"

	gogit "github.com/go-git/go-git/v5"

	"github.com/konveyor/migration-harness/internal/git"
	"github.com/konveyor/migration-harness/internal/logging"
)

// Push statuses: what the run left on the branch it pushes to, as the
// termination blob records it (#129).
//
// The set is closed, and only pushPushed says this run's commits are on
// the remote. A consumer that meets a status it does not recognise, or no
// push section at all, must treat the run as not pushed: a status a later
// harness adds can then never read as "there is something to merge".
const (
	// pushPushed: the remote branch holds commits this run put there, and
	// the final push left it at the commit the record names.
	pushPushed = "pushed"
	// pushNoChanges: the run left no commits of its own on the branch, so
	// the final push had nothing to send. Normally nothing of the run is
	// on the remote either; when earlier pushes landed and the agent then
	// moved the branch back, Reason says so. It pairs with any outcome,
	// succeeded included. Whether a succeeded run that changed nothing
	// should count as a success is ADR 0020's first open question; this
	// status is the fact that question turns on, not an answer to it.
	pushNoChanges = "noChanges"
	// pushFailed: the final push returned an error. The stage fails with
	// it (exit 1), and the remote holds whatever earlier pushes left.
	pushFailed = "failed"
)

// pushReasonMaxBytes bounds the push error carried in the record. The
// whole blob has 4096 bytes (see writeTerminationLog), stopReason already
// carries the same failure in full, and a server's rejection can run to
// paragraphs.
const pushReasonMaxBytes = 300

// pushReport is the termination blob's `push` section: what this run left
// on the branch it pushes to (TARGET_BRANCH).
//
// It exists because the pod is gone by the time anyone asks. The plan
// ladder tells a viewer "Pushed 7 commits" or "No changes to push" while
// the run is live, and nothing kept that: a run that changed nothing
// ended with a blob identical to one that pushed a fix, so whatever acts
// on a finished run — a client deciding whether there is anything to
// open a pull request for — had to go and ask the remote (#129).
//
// It is a record, not an outcome. The exit code, the phase and the
// Succeeded condition are what they were without it; a consumer reads
// this beside them, the way it reads verification.
//
// Absent means unknown; present means measured — the rule the ladder's
// fields follow. The section is absent when the run ended before its
// final push (a setup failure) and from a harness that predates it, and
// neither may be read as "nothing was pushed".
type pushReport struct {
	// Status is one of the push* constants above.
	Status string `json:"status"`
	// Reason says what Status alone does not: the push error, or that the
	// remote holds something the branch does not account for.
	Reason string `json:"reason,omitempty"`
	// Branch is the branch the run pushes to.
	Branch string `json:"branch,omitempty"`
	// Base is the commit the run started from: the branch tip after the
	// clone and checkout. Everything past it on the branch is this run's
	// work. Empty when it could not be resolved.
	Base string `json:"base,omitempty"`
	// Commit is the branch tip the remote holds after the final push. Set
	// only with pushPushed: it is the commit a pull request from this
	// branch proposes, and when it equals verification.commit the tree
	// that was measured is the tree that was pushed.
	Commit string `json:"commit,omitempty"`
	// Commits is how many commits the run added to the branch. A pointer
	// because zero is a measurement and absence is not: nil when the
	// count could not be made (see git.CommitsSince).
	Commits *int `json:"commits,omitempty"`
}

// pushed reports whether the record says this run's commits are on the
// remote branch. A nil record — the run never reached its push — is not.
func (p *pushReport) pushed() bool { return p != nil && p.Status == pushPushed }

// verdictOnly is the record cut down to what writeTerminationLog keeps
// when the blob has to shrink: whether anything was pushed, which commit,
// and how many. The branch, the base and the reason are context a
// consumer can do without. The section itself is not, because its absence
// is read as "unknown".
func (p *pushReport) verdictOnly() *pushReport {
	if p == nil {
		return nil
	}
	return &pushReport{Status: p.Status, Commit: p.Commit, Commits: p.Commits}
}

// finalPush is what runStage observed at its final push, gathered in one
// value so recordPush can be tested without a repository.
type finalPush struct {
	// outcome and err are what git.Push returned for the final push.
	outcome git.PushOutcome
	err     error
	// landed is how many pushes moved the remote branch during the run,
	// the watcher's and the final one: the ladder's pushesLanded.
	landed int
	// tip is the branch tip after the push. Empty when unresolved.
	tip string
	// commits is git.CommitsSince for the branch: -1 when not counted.
	commits int
}

// observePush reads what recordPush needs from the repository once the
// final push has succeeded: how many commits the branch gained and where
// it points now. Either can fail without failing the stage; the record
// then leaves that field out, which is what absent means.
func observePush(repo *gogit.Repository, branch, base string, outcome git.PushOutcome, landed int) finalPush {
	p := finalPush{outcome: outcome, landed: landed, commits: -1}
	if n, err := git.CommitsSince(repo, branch, base); err != nil {
		logging.Warn("count commits on branch %s: %v — the closing rung and the push record omit the count", branch, err)
	} else {
		p.commits = n
	}
	tip, err := git.BranchTipSHA(repo, branch)
	if err != nil {
		logging.Warn("resolve the tip of branch %s: %v — the push record omits the commit", branch, err)
	}
	p.tip = tip
	return p
}

// recordPush classifies the final push into the record.
//
// The question is whether THIS run put anything on the remote branch,
// which is not quite what git.Push's outcome answers. A final push that
// found the remote up to date is the normal case of the watcher having
// got there first — but only if one of the run's pushes moved the remote
// at some point. Up to date with none landed means the remote already
// held the tip before the run pushed anything, so the commits there are
// not this run's.
//
// The ladder's closing rung and the closing notice both read this record
// (see runStage), so what a viewer was told and what the blob keeps
// cannot differ.
func recordPush(branch, base string, p finalPush, red *redactor) *pushReport {
	r := &pushReport{Branch: branch, Base: base}
	switch {
	case p.err != nil:
		r.Status = pushFailed
		r.Reason = boundPushReason(red.redact(p.err.Error()))
	case p.outcome == git.PushSkipped:
		// The branch still points at the commit the run started from, so
		// zero is measured, not assumed.
		r.Status = pushNoChanges
		zero := 0
		r.Commits = &zero
		if p.landed > 0 {
			// The agent committed, the watcher pushed, and the agent then
			// moved the branch back. Nothing is left to push, and the
			// remote is not where the branch is.
			r.Reason = fmt.Sprintf("the branch ended at the commit the run started from, "+
				"but the remote still holds what this run pushed earlier (pushes that landed: %d)", p.landed)
		}
	case p.landed == 0:
		// Not counted: commits past the base that this run did not push
		// are someone else's, and reporting their number here would
		// claim them.
		r.Status = pushNoChanges
		r.Reason = "the remote already held the branch tip and no push of this run moved it"
	default:
		r.Status = pushPushed
		r.Commit = p.tip
		if p.commits >= 0 {
			n := p.commits
			r.Commits = &n
		}
	}
	return r
}

// boundPushReason cuts a push error to pushReasonMaxBytes, on a rune
// boundary, marking the cut.
func boundPushReason(s string) string {
	if len(s) <= pushReasonMaxBytes {
		return s
	}
	return truncateRuneSafe(s, pushReasonMaxBytes) + "…"
}
