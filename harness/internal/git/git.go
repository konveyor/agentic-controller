package git

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/konveyor/migration-harness/internal/watcher"
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

func CommitAll(repo *gogit.Repository, message string) (plumbing.Hash, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("get worktree: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("get status: %w", err)
	}
	if status.IsClean() {
		return plumbing.ZeroHash, nil
	}

	staged := false
	for path, s := range status {
		if s.Worktree == gogit.Untracked && !watcher.ShouldStageNewFile(path) {
			continue
		}
		if _, err := wt.Add(path); err != nil {
			return plumbing.ZeroHash, fmt.Errorf("add %s: %w", path, err)
		}
		staged = true
	}
	if !staged {
		return plumbing.ZeroHash, nil
	}

	hash, err := wt.Commit(message, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "migration-harness",
			Email: "migration-harness@konveyor.io",
			When:  time.Now(),
		},
	})
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("commit: %w", err)
	}

	return hash, nil
}

// Push force-pushes the branch. Force is intentional: the watcher's
// auto-commits may create non-fast-forward histories, and each stage
// owns its branch exclusively. Concurrent runs on the same branch are
// not supported.
func Push(ctx context.Context, cred *Credentials, repo *gogit.Repository, branch string) error {
	refSpec := gogitcfg.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/heads/%s", branch, branch))

	err := repo.PushContext(ctx, &gogit.PushOptions{
		Auth:       cred.Auth(),
		RemoteName: "origin",
		RefSpecs:   []gogitcfg.RefSpec{refSpec},
		Force:      true,
	})
	if err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return fmt.Errorf("push %s: %w", branch, err)
	}

	return nil
}
