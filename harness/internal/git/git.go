package git

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func Clone(ctx context.Context, cred *Credentials, destDir string) (*gogit.Repository, error) {
	if _, err := os.Stat(destDir); err == nil {
		os.RemoveAll(destDir)
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
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	ref := plumbing.NewBranchReferenceName(branch)

	err = wt.Checkout(&gogit.CheckoutOptions{
		Branch: ref,
		Create: true,
	})
	if err != nil {
		err = wt.Checkout(&gogit.CheckoutOptions{
			Branch: ref,
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

	if err := wt.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("add all: %w", err)
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
