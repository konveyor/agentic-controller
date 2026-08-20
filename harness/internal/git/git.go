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

func EnsureGitignore(repoDir string, patterns []string) error {
	gitignorePath := filepath.Join(repoDir, ".gitignore")
	existing, _ := os.ReadFile(gitignorePath)
	content := string(existing)

	lines := strings.Split(content, "\n")
	knownLines := make(map[string]bool)
	for _, line := range lines {
		knownLines[strings.TrimSpace(line)] = true
	}
	var toAdd []string
	for _, p := range patterns {
		if !knownLines[p] {
			toAdd = append(toAdd, p)
		}
	}
	if len(toAdd) == 0 {
		return nil
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open .gitignore: %w", err)
	}
	defer f.Close()

	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		f.WriteString("\n")
	}
	for _, p := range toAdd {
		f.WriteString(p + "\n")
	}
	return nil
}

func CommitFiles(repo *gogit.Repository, paths []string, msg string) error {
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}
	for _, p := range paths {
		if _, err := wt.Add(p); err != nil {
			continue
		}
	}
	_, err = wt.Commit(msg, &gogit.CommitOptions{})
	if errors.Is(err, gogit.ErrEmptyCommit) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("commit: %w", err)
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

// Push updates refs/heads/<branch> on origin. baseSHA is the commit the
// run started from (HEAD after clone/checkout): when HEAD still equals
// it the run produced no commits and the push is skipped, so no-op runs
// do not litter the remote with empty branches. An empty baseSHA
// disables the check — an unknown base must never block a push of real
// work. The returned bool is false only when the push was skipped, so
// callers can report the absence of results instead of claiming they
// landed on the branch.
func Push(ctx context.Context, cred *Credentials, repo *gogit.Repository, branch, baseSHA string) (bool, error) {
	// Not redundant with the NoErrAlreadyUpToDate swallow below: when the
	// remote branch does not exist yet, PushContext sends a create command
	// (ZeroHash → local HEAD) instead of reporting already-up-to-date, so
	// this guard is the only thing preventing empty branch creation.
	if baseSHA != "" {
		if head, err := repo.Head(); err == nil && head.Hash().String() == baseSHA {
			logging.Info("no commits produced; skipping push of %s", branch)
			return false, nil
		}
	}

	refSpec := gogitcfg.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", branch, branch))

	err := repo.PushContext(ctx, &gogit.PushOptions{
		Auth:       cred.Auth(),
		RemoteName: "origin",
		RefSpecs:   []gogitcfg.RefSpec{refSpec},
	})
	if err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return false, fmt.Errorf("push %s: %w", branch, err)
	}

	return true, nil
}
