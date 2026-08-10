package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/konveyor/tackle2-hub/shared/scm"

	"github.com/konveyor/migration-harness/internal/logging"
)

// Repository wraps scm.Git to provide harness-specific git operations.
// Credentials live in a separate Home directory, invisible to agent subprocesses.
type Repository struct {
	remote scm.Remote
	path   string
	home   string
}

// NewRepository creates a Repository for the given remote and local path.
// home is the directory for git configuration and credentials, kept separate
// from the agent's HOME for credential isolation.
func NewRepository(remote scm.Remote, path, home string) *Repository {
	return &Repository{
		remote: remote,
		path:   path,
		home:   home,
	}
}

func (r *Repository) newSCM() *scm.Git {
	return &scm.Git{
		Base: scm.Base{
			Home:   r.home,
			Remote: r.remote,
			Path:   r.path,
		},
	}
}

// Fetch clones the repository. If the destination directory already exists,
// it is removed first (only under /workspace or the system temp dir).
func (r *Repository) Fetch() error {
	abs, err := filepath.Abs(r.path)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}
	if _, err := os.Stat(abs); err == nil {
		if !isChildOf(abs, "/workspace") && !isChildOf(abs, os.TempDir()) {
			return fmt.Errorf("refusing to remove %s: not under /workspace or temp", abs)
		}
		if err := os.RemoveAll(abs); err != nil {
			return fmt.Errorf("remove %s: %w", abs, err)
		}
	}
	return r.newSCM().Fetch()
}

// Branch checks out the given branch, creating it locally if it does not
// already exist remotely. Deliberately does not use scm.CREATE: that option
// pushes the new branch to the remote immediately, which would recreate the
// empty-branch litter Push's baseSHA check exists to prevent.
func (r *Repository) Branch(ref string) error {
	s := r.newSCM()
	if err := s.Branch(ref); err == nil {
		return nil
	}
	if _, err := s.Head(); err != nil {
		return fmt.Errorf("credential setup: %w", err)
	}
	cmd := exec.Command("/usr/bin/git", "checkout", "-b", ref)
	cmd.Dir = r.path
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "HOME="+r.home)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create branch %s: %s: %w", ref, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// CommitAndPush stages the given files, commits with the message, and pushes.
func (r *Repository) CommitAndPush(files []string, msg string) error {
	return r.newSCM().Commit(files, msg)
}

// HeadSHA returns the hash of the current HEAD commit.
func (r *Repository) HeadSHA() (string, error) {
	return r.newSCM().Head()
}

// Push pushes local commits to the remote. baseSHA is the commit the run
// started from: when HEAD still equals it the run produced no commits and
// the push is skipped, so no-op runs do not create empty branches on the
// remote. An empty baseSHA disables the check — an unknown base must never
// block a push of real work. The returned bool is false only when the push
// was skipped.
//
// KNOWN LIMITATION: ctx is not honored. scm.Git.Push() has no context
// parameter — it runs "git push" via command.Command.Run(), which is
// hardcoded to context.TODO() (tackle2-hub/shared/command/cmd.go). Before
// this switch to scm, Push shelled out directly with
// exec.CommandContext(ctx, ...), so a stuck push could be killed by the
// caller's SIGINT-bound ctx or by the final push's 25s timeout
// (see main.go's pushCtx). That guarantee is gone until scm.Git exposes a
// context-aware Push/RunWith path. If a hung push in production is ever
// observed blocking pod exit, either push for that upstream API or restore
// the exec.CommandContext(ctx, "git", "push", "origin", "HEAD") fallback
// here.
func (r *Repository) Push(ctx context.Context, baseSHA string) (bool, error) {
	s := r.newSCM()
	head, err := s.Head()
	if err != nil {
		return false, fmt.Errorf("credential setup: %w", err)
	}
	if baseSHA != "" && head == baseSHA {
		logging.Info("no commits produced; skipping push")
		return false, nil
	}
	if err := s.Push(); err != nil {
		return false, fmt.Errorf("push: %w", err)
	}
	return true, nil
}

// ConfigureAuthor sets the git author in the repository-level config so the
// agent subprocess can commit with the correct identity.
func (r *Repository) ConfigureAuthor(name, email string) error {
	for _, kv := range [][2]string{
		{"user.name", name},
		{"user.email", email},
	} {
		cmd := exec.Command("/usr/bin/git", "config", kv[0], kv[1])
		cmd.Dir = r.path
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git config %s: %s: %w", kv[0], strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}

// IsDirty returns true if the working tree has uncommitted changes,
// along with the count of dirty paths.
func (r *Repository) IsDirty() (bool, int, error) {
	cmd := exec.Command("/usr/bin/git", "status", "--porcelain")
	cmd.Dir = r.path
	out, err := cmd.Output()
	if err != nil {
		return false, 0, fmt.Errorf("git status: %w", err)
	}
	output := strings.TrimSpace(string(out))
	if output == "" {
		return false, 0, nil
	}
	return true, len(strings.Split(output, "\n")), nil
}

// Clean removes the SCM home directory (credentials and config).
func (r *Repository) Clean() error {
	return r.newSCM().Clean()
}

// Path returns the local repository path.
func (r *Repository) Path() string {
	return r.path
}

// EnsureGitignore appends patterns to .gitignore if not already present.
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

func isChildOf(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}
