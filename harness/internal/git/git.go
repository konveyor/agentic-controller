package git

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	gogitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/konveyor/migration-harness/internal/logging"
)

func isChildOf(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

func Clone(ctx context.Context, cred *Credentials, destDir string) (*gogit.Repository, error) {
	destDir, err := filepath.Abs(destDir)
	if err != nil {
		return nil, fmt.Errorf("resolve destination: %w", err)
	}

	if _, err := os.Stat(destDir); err == nil {
		if !isChildOf(destDir, "/workspace") && !isChildOf(destDir, os.TempDir()) {
			return nil, fmt.Errorf("refusing to remove %s: not under /workspace or temp", destDir)
		}
		if err := os.RemoveAll(destDir); err != nil {
			return nil, fmt.Errorf("remove %s: %w", destDir, err)
		}
	}

	repo, err := gogit.PlainCloneContext(ctx, destDir, false, &gogit.CloneOptions{
		URL:  cred.RepoURL,
		Auth: cred.Auth(),
	})
	if err != nil {
		return nil, fmt.Errorf("clone %s: %w", cred.RepoURL, err)
	}
	return repo, nil
}

func StripCredentials(repo *gogit.Repository) error {
	remote, err := repo.Remote("origin")
	if err != nil {
		return fmt.Errorf("get remote origin: %w", err)
	}

	cfg := remote.Config()
	if len(cfg.URLs) == 0 {
		return nil
	}

	err = repo.DeleteRemote("origin")
	if err != nil {
		return fmt.Errorf("delete remote: %w", err)
	}

	bareURLs := make([]string, len(cfg.URLs))
	for i, u := range cfg.URLs {
		parsed, err := url.Parse(u)
		if err == nil && parsed.User != nil {
			parsed.User = nil
			bareURLs[i] = parsed.String()
		} else {
			bareURLs[i] = u
		}
	}

	_, err = repo.CreateRemote(&gogitcfg.RemoteConfig{
		Name: "origin",
		URLs: bareURLs,
	})
	if err != nil {
		return fmt.Errorf("recreate remote: %w", err)
	}

	return nil
}

// ConfigureAuthor sets the git commit identity (user.name / user.email)
// used for the agent's local commits. go-git derives both the author and
// the committer from these values.
func ConfigureAuthor(repo *gogit.Repository, name, email string) error {
	cfg, err := repo.Config()
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	cfg.User.Name = name
	cfg.User.Email = email
	return repo.SetConfig(cfg)
}

func CheckoutBranch(repo *gogit.Repository, branch string) error {
	localRef := plumbing.NewBranchReferenceName(branch)

	// Already on the requested branch — nothing to do.
	if head, err := repo.Head(); err == nil && head.Name() == localRef {
		return nil
	}

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	remoteRef := plumbing.NewRemoteReferenceName("origin", branch)

	// If the remote tracking branch exists, create local branch from it.
	if hash, err := repo.ResolveRevision(plumbing.Revision(remoteRef)); err == nil {
		return wt.Checkout(&gogit.CheckoutOptions{
			Branch: localRef,
			Hash:   *hash,
			Create: true,
		})
	}

	// Otherwise create a new branch from HEAD.
	err = wt.Checkout(&gogit.CheckoutOptions{
		Branch: localRef,
		Create: true,
	})
	if err != nil {
		err = wt.Checkout(&gogit.CheckoutOptions{
			Branch: localRef,
		})
		if err != nil {
			return fmt.Errorf("checkout branch %s: %w", branch, err)
		}
	}
	return nil
}

// HeadSHA returns the hash of the current HEAD commit.
func HeadSHA(repo *gogit.Repository) (string, error) {
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}
	return head.Hash().String(), nil
}

// branchTip resolves refs/heads/<branch>, the ref Push sends to the
// remote. That is not the same thing as HEAD. The agent is free to
// change branches during a run — to park an experiment on a scratch
// branch, say — and when it does, HEAD's history is not the history
// that reaches the remote. Anything deciding or reporting on the push
// has to ask the branch.
func branchTip(repo *gogit.Repository, branch string) (plumbing.Hash, error) {
	ref, err := repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("resolve branch %s: %w", branch, err)
	}
	return ref.Hash(), nil
}

// FileChangedSince reports whether the worktree file at relPath differs
// from its content at baseSHA — created since the base, or different
// bytes, committed or not. A file absent from the worktree is not a
// change: there is nothing to read. An empty baseSHA fails open
// (changed), as Push does: an unknown base must never hide the run's
// own output.
func FileChangedSince(repo *gogit.Repository, baseSHA, relPath string) (bool, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return false, fmt.Errorf("get worktree: %w", err)
	}
	current, err := os.ReadFile(filepath.Join(wt.Filesystem.Root(), relPath))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", relPath, err)
	}
	if baseSHA == "" {
		return true, nil
	}

	commit, err := repo.CommitObject(plumbing.NewHash(baseSHA))
	if err != nil {
		return false, fmt.Errorf("resolve base commit %s: %w", baseSHA, err)
	}
	f, err := commit.File(relPath)
	if errors.Is(err, object.ErrFileNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s at %s: %w", relPath, baseSHA, err)
	}
	base, err := f.Contents()
	if err != nil {
		return false, fmt.Errorf("read %s at %s: %w", relPath, baseSHA, err)
	}
	return base != string(current), nil
}

// commitsSinceMaxWalk bounds the history walk. A run produces tens of
// commits — the turn ceiling puts a couple of hundred at the very
// outside — so a base this far back is not on the branch's
// first-parent chain at all and the walk is on its way to the
// repository's root. The clone is full depth and these are decade-old
// applications, so that is tens of thousands of objects decoded to
// answer "unknown" anyway, on the exit path, for one word of rung text.
const commitsSinceMaxWalk = 5000

// CommitsSince counts the commits between baseSHA — the commit the run
// started from — and the tip of branch, i.e. how much work this run
// put on the branch it pushes. It walks first-parent history from the
// branch tip and stops at baseSHA; a merge brought in from elsewhere
// therefore counts as one commit, which is what a viewer reading
// "pushed 7 commits" means.
//
// It counts from the branch rather than from HEAD because the branch
// is what Push sends. An agent that commits to the branch and then
// moves off it leaves HEAD on a history the remote never receives, and
// counting that history reports commits nobody pushed (#281).
//
// An empty baseSHA means the starting point was never recorded, so
// nothing can be counted and the result is -1, not 0 — the difference
// between "no work" and "unknown" matters to the caller rendering it.
// A baseSHA that is not an ancestor of the branch tip (the agent
// rebased, reset, or merged from the other side, all of which it is
// free to do) also yields -1 rather than the whole branch's length,
// whether that is discovered at the root or at commitsSinceMaxWalk.
func CommitsSince(repo *gogit.Repository, branch, baseSHA string) (int, error) {
	return commitsSince(repo, branch, baseSHA, commitsSinceMaxWalk)
}

// commitsSince is CommitsSince with the walk bound injected, so a test
// can reach the cap without building commitsSinceMaxWalk commits.
func commitsSince(repo *gogit.Repository, branch, baseSHA string, maxWalk int) (int, error) {
	if baseSHA == "" {
		return -1, nil
	}
	tip, err := branchTip(repo, branch)
	if err != nil {
		return -1, err
	}
	base := plumbing.NewHash(baseSHA)
	n := 0
	for hash := tip; hash != base; {
		if n == maxWalk {
			// Same answer the root would give, reached without reading
			// the rest of the repository to get there.
			logging.Info("commit count: base %s not within %d commits of branch %s; not counting", baseSHA, maxWalk, branch)
			return -1, nil
		}
		commit, err := repo.CommitObject(hash)
		if err != nil {
			return -1, fmt.Errorf("walk history from %s: %w", hash, err)
		}
		n++
		if commit.NumParents() == 0 {
			// Walked to the root without meeting baseSHA.
			return -1, nil
		}
		hash = commit.ParentHashes[0]
	}
	return n, nil
}

// PushOutcome distinguishes the three ways a push can succeed. Two
// different questions are asked of one push and they have different
// answers: "did this run put anything on the branch" (skipped or not)
// is what the closing notices report, while "did the remote move just
// now" (advanced or not) is what a count of pushes means. Collapsing
// them into a bool makes one of the two wrong — a watcher push that
// finds nothing new is still a successful push, and the final push is
// routinely already-up-to-date because the watcher got there first.
type PushOutcome int

const (
	// PushSkipped means the run produced no commits, so nothing was
	// sent: HEAD is still the commit the run started from.
	PushSkipped PushOutcome = iota
	// PushUpToDate means the push reached the remote and the remote
	// already had every commit offered.
	PushUpToDate
	// PushAdvanced means the remote branch moved as a result.
	PushAdvanced
)

func (o PushOutcome) String() string {
	switch o {
	case PushSkipped:
		return "skipped"
	case PushUpToDate:
		return "already up to date"
	case PushAdvanced:
		return "advanced"
	}
	return "unknown"
}

// Push updates refs/heads/<branch> on origin. baseSHA is the commit the
// run started from (the branch tip after clone/checkout): when the
// branch still points at it the run produced nothing for the branch and
// the push is skipped, so no-op runs do not litter the remote with empty
// branches. An empty baseSHA disables the check — an unknown base must
// never block a push of real work.
//
// See PushOutcome for what the three results mean. Callers reporting
// whether results reached the branch want "not PushSkipped"; callers
// counting pushes want PushAdvanced alone.
func Push(ctx context.Context, cred *Credentials, repo *gogit.Repository, branch, baseSHA string) (PushOutcome, error) {
	// Not redundant with the NoErrAlreadyUpToDate swallow below: when the
	// remote branch does not exist yet, PushContext sends a create command
	// (ZeroHash → local branch tip) instead of reporting
	// already-up-to-date, so this guard is the only thing preventing empty
	// branch creation.
	if baseSHA != "" {
		// The question is about the branch, not about HEAD. An agent that
		// commits to the branch and then checks something else out leaves
		// HEAD saying nothing about what this run put on the branch, and
		// reading HEAD here gets it wrong both ways: it skips a push that
		// had real commits to send, and it sends one that had none. A
		// branch that will not resolve falls through to PushContext, which
		// reports the real problem (#281).
		if tip, err := branchTip(repo, branch); err == nil && tip.String() == baseSHA {
			logging.Info("no commits produced; skipping push of %s", branch)
			return PushSkipped, nil
		}
	}

	refSpec := gogitcfg.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", branch, branch))

	err := repo.PushContext(ctx, &gogit.PushOptions{
		Auth:       cred.Auth(),
		RemoteName: "origin",
		RefSpecs:   []gogitcfg.RefSpec{refSpec},
	})
	// Already-up-to-date is a successful push of nothing: the watcher
	// fires on file activity, so most of its pushes find the remote
	// already holding every commit the agent has made so far.
	if errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return PushUpToDate, nil
	}
	if err != nil {
		return PushSkipped, fmt.Errorf("push %s: %w", branch, err)
	}

	return PushAdvanced, nil
}
