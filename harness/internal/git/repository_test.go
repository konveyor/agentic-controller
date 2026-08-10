package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konveyor/tackle2-hub/shared/scm"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, string(out), err)
	}
	return strings.TrimSpace(string(out))
}

func setupBareRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", dir)
	return dir
}

func seedBareRepo(t *testing.T, remoteDir string) {
	t.Helper()
	tmpDir := filepath.Join(t.TempDir(), "seed")
	runGit(t, "", "clone", remoteDir, tmpDir)
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# test\n"), 0644)
	runGit(t, tmpDir, "add", "README.md")
	runGit(t, tmpDir, "-c", "user.name=test", "-c", "user.email=test@test.com", "commit", "-m", "initial")
	runGit(t, tmpDir, "push", "origin", "HEAD")
}

func newTestRepo(t *testing.T, remoteDir, cloneDir string) *Repository {
	t.Helper()
	home := filepath.Join(t.TempDir(), "scm-home")
	remote := scm.Remote{
		URL: remoteDir,
	}
	return NewRepository(remote, cloneDir, home)
}

func TestFetch(t *testing.T) {
	remoteDir := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cloneDir := filepath.Join(t.TempDir(), "work")
	repo := newTestRepo(t, remoteDir, cloneDir)

	if err := repo.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(cloneDir, "README.md"))
	if err != nil {
		t.Fatal("README.md not found after fetch")
	}
	if string(data) != "# test\n" {
		t.Errorf("README.md = %q, want %q", string(data), "# test\n")
	}
}

func TestFetchRefusesUnsafePath(t *testing.T) {
	remoteDir := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	// Outside /workspace and the system temp dir, but under the test
	// working directory so creating it needs no special permissions
	// (unlike a system path such as /usr/local, which CI and sandboxed
	// dev machines may not allow writing to).
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	unsafeDir := filepath.Join(cwd, "unsafe-fetch-target")
	if err := os.Mkdir(unsafeDir, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	defer os.RemoveAll(unsafeDir)

	repo := newTestRepo(t, remoteDir, unsafeDir)
	if err := repo.Fetch(); err == nil || !strings.Contains(err.Error(), "refusing to remove") {
		t.Errorf("expected refusal for unsafe path, got: %v", err)
	}
}

func TestBranch(t *testing.T) {
	remoteDir := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cloneDir := filepath.Join(t.TempDir(), "work")
	repo := newTestRepo(t, remoteDir, cloneDir)
	if err := repo.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if err := repo.Branch("feature-branch"); err != nil {
		t.Fatalf("Branch: %v", err)
	}

	branch := runGit(t, cloneDir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "feature-branch" {
		t.Errorf("branch = %s, want feature-branch", branch)
	}
}

func TestBranchFromRemote(t *testing.T) {
	remoteDir := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	// Push a file to a branch on the remote.
	pusherDir := filepath.Join(t.TempDir(), "pusher")
	runGit(t, "", "clone", remoteDir, pusherDir)
	runGit(t, pusherDir, "checkout", "-b", "remote-branch")
	os.WriteFile(filepath.Join(pusherDir, "PLAN.md"), []byte("# Plan\n"), 0644)
	runGit(t, pusherDir, "add", "PLAN.md")
	runGit(t, pusherDir, "-c", "user.name=test", "-c", "user.email=test@test.com", "commit", "-m", "add plan")
	runGit(t, pusherDir, "push", "origin", "remote-branch")

	cloneDir := filepath.Join(t.TempDir(), "work")
	repo := newTestRepo(t, remoteDir, cloneDir)
	if err := repo.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := repo.Branch("remote-branch"); err != nil {
		t.Fatalf("Branch: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(cloneDir, "PLAN.md"))
	if err != nil {
		t.Fatal("PLAN.md not found after checkout")
	}
	if string(data) != "# Plan\n" {
		t.Errorf("PLAN.md = %q, want %q", string(data), "# Plan\n")
	}
}

func TestConfigureAuthor(t *testing.T) {
	remoteDir := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cloneDir := filepath.Join(t.TempDir(), "work")
	repo := newTestRepo(t, remoteDir, cloneDir)
	if err := repo.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if err := repo.ConfigureAuthor("migration-agent", "migration-agent@konveyor.io"); err != nil {
		t.Fatalf("ConfigureAuthor: %v", err)
	}

	name := runGit(t, cloneDir, "config", "user.name")
	if name != "migration-agent" {
		t.Errorf("user.name = %q, want %q", name, "migration-agent")
	}
	email := runGit(t, cloneDir, "config", "user.email")
	if email != "migration-agent@konveyor.io" {
		t.Errorf("user.email = %q, want %q", email, "migration-agent@konveyor.io")
	}
}

func TestCommitAndPush(t *testing.T) {
	remoteDir := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cloneDir := filepath.Join(t.TempDir(), "work")
	repo := newTestRepo(t, remoteDir, cloneDir)
	if err := repo.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := repo.Branch("test-commit"); err != nil {
		t.Fatalf("Branch: %v", err)
	}

	os.WriteFile(filepath.Join(cloneDir, ".gitignore"), []byte("*.tmp\n"), 0644)
	os.MkdirAll(filepath.Join(cloneDir, ".konveyor"), 0755)
	os.WriteFile(filepath.Join(cloneDir, ".konveyor", "analysis.json"), []byte("{}\n"), 0644)

	if err := repo.CommitAndPush(
		[]string{".gitignore", ".konveyor/analysis.json"},
		"harness: test commit",
	); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}

	msg := runGit(t, cloneDir, "log", "-1", "--format=%s")
	if msg != "harness: test commit" {
		t.Errorf("commit message = %q, want %q", msg, "harness: test commit")
	}

	// Verify pushed to remote.
	verifyDir := filepath.Join(t.TempDir(), "verify")
	runGit(t, "", "clone", "--branch", "test-commit", remoteDir, verifyDir)
	if _, err := os.Stat(filepath.Join(verifyDir, ".gitignore")); err != nil {
		t.Error(".gitignore not found on remote after push")
	}
}

func TestPush(t *testing.T) {
	remoteDir := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cloneDir := filepath.Join(t.TempDir(), "work")
	repo := newTestRepo(t, remoteDir, cloneDir)
	if err := repo.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := repo.Branch("test-push"); err != nil {
		t.Fatalf("Branch: %v", err)
	}
	if err := repo.ConfigureAuthor("test", "test@test.com"); err != nil {
		t.Fatalf("ConfigureAuthor: %v", err)
	}

	// Simulate agent commit (done outside SCM, using plain git).
	os.WriteFile(filepath.Join(cloneDir, "migrated.java"), []byte("class Foo {}\n"), 0644)
	runGit(t, cloneDir, "add", "migrated.java")
	runGit(t, cloneDir, "commit", "-m", "agent: migrate Foo.java")

	localHash := runGit(t, cloneDir, "rev-parse", "HEAD")

	ctx := context.Background()
	if pushed, err := repo.Push(ctx, ""); err != nil {
		t.Fatalf("Push: %v", err)
	} else if !pushed {
		t.Error("Push reported skipped despite new commits")
	}

	// Verify the commit arrived on the remote.
	verifyDir := filepath.Join(t.TempDir(), "verify")
	runGit(t, "", "clone", "--branch", "test-push", remoteDir, verifyDir)
	remoteHash := runGit(t, verifyDir, "rev-parse", "HEAD")
	if remoteHash != localHash {
		t.Errorf("remote HEAD = %s, want %s", remoteHash, localHash)
	}
}

func TestPushNoop(t *testing.T) {
	remoteDir := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cloneDir := filepath.Join(t.TempDir(), "work")
	repo := newTestRepo(t, remoteDir, cloneDir)
	if err := repo.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	ctx := context.Background()
	if _, err := repo.Push(ctx, ""); err != nil {
		t.Fatalf("Push (no-op) should not fail: %v", err)
	}
}

func TestPushSkipsWhenNoNewCommits(t *testing.T) {
	remoteDir := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cloneDir := filepath.Join(t.TempDir(), "work")
	repo := newTestRepo(t, remoteDir, cloneDir)
	if err := repo.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := repo.Branch("test-push-skip"); err != nil {
		t.Fatalf("Branch: %v", err)
	}

	baseSHA, err := repo.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}

	// No commits beyond the checkout point: the push must be skipped so
	// no-op runs do not create empty branches on the remote.
	ctx := context.Background()
	pushed, err := repo.Push(ctx, baseSHA)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if pushed {
		t.Error("Push reported pushed despite no new commits")
	}

	verifyDir := filepath.Join(t.TempDir(), "verify")
	if err := exec.Command("git", "clone", "--branch", "test-push-skip", remoteDir, verifyDir).Run(); err == nil {
		t.Errorf("remote branch test-push-skip created despite no commits")
	}
}

func TestPushWithNewCommitsPushes(t *testing.T) {
	remoteDir := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cloneDir := filepath.Join(t.TempDir(), "work")
	repo := newTestRepo(t, remoteDir, cloneDir)
	if err := repo.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := repo.Branch("test-push-with-commits"); err != nil {
		t.Fatalf("Branch: %v", err)
	}
	if err := repo.ConfigureAuthor("test", "test@test.com"); err != nil {
		t.Fatalf("ConfigureAuthor: %v", err)
	}

	baseSHA, err := repo.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}

	os.WriteFile(filepath.Join(cloneDir, "migrated.java"), []byte("class Foo {}\n"), 0644)
	runGit(t, cloneDir, "add", "migrated.java")
	runGit(t, cloneDir, "commit", "-m", "agent: migrate Foo.java")
	expectedHash := runGit(t, cloneDir, "rev-parse", "HEAD")

	ctx := context.Background()
	pushed, err := repo.Push(ctx, baseSHA)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !pushed {
		t.Error("Push reported skipped despite new commits")
	}

	verifyDir := filepath.Join(t.TempDir(), "verify")
	runGit(t, "", "clone", "--branch", "test-push-with-commits", remoteDir, verifyDir)
	remoteHash := runGit(t, verifyDir, "rev-parse", "HEAD")
	if remoteHash != expectedHash {
		t.Errorf("remote HEAD = %s, want %s", remoteHash, expectedHash)
	}
}

func TestIsDirty(t *testing.T) {
	remoteDir := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cloneDir := filepath.Join(t.TempDir(), "work")
	repo := newTestRepo(t, remoteDir, cloneDir)
	if err := repo.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	dirty, count, err := repo.IsDirty()
	if err != nil {
		t.Fatalf("IsDirty: %v", err)
	}
	if dirty {
		t.Error("expected clean worktree")
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	os.WriteFile(filepath.Join(cloneDir, "new-file.txt"), []byte("data\n"), 0644)

	dirty, count, err = repo.IsDirty()
	if err != nil {
		t.Fatalf("IsDirty: %v", err)
	}
	if !dirty {
		t.Error("expected dirty worktree")
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestEnsureGitignore(t *testing.T) {
	dir := t.TempDir()

	if err := EnsureGitignore(dir, []string{"*.tmp", "*.bak"}); err != nil {
		t.Fatalf("EnsureGitignore: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal("no .gitignore created")
	}
	content := string(data)
	if !strings.Contains(content, "*.tmp") || !strings.Contains(content, "*.bak") {
		t.Errorf("missing patterns in .gitignore: %s", content)
	}

	// Second call should not duplicate.
	if err := EnsureGitignore(dir, []string{"*.tmp", "*.swp"}); err != nil {
		t.Fatalf("EnsureGitignore (second): %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, ".gitignore"))
	if strings.Count(string(data), "*.tmp") != 1 {
		t.Error("*.tmp duplicated in .gitignore")
	}
	if !strings.Contains(string(data), "*.swp") {
		t.Error("*.swp not added")
	}
}

func TestFullLifecycle(t *testing.T) {
	remoteDir := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cloneDir := filepath.Join(t.TempDir(), "work")
	repo := newTestRepo(t, remoteDir, cloneDir)

	if err := repo.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := repo.ConfigureAuthor("migration-agent", "migration-agent@konveyor.io"); err != nil {
		t.Fatalf("ConfigureAuthor: %v", err)
	}
	if err := repo.Branch("migration-test"); err != nil {
		t.Fatalf("Branch: %v", err)
	}

	// Harness commits grounding data.
	os.WriteFile(filepath.Join(cloneDir, ".gitignore"), []byte("*.tmp\n"), 0644)
	if err := repo.CommitAndPush([]string{".gitignore"}, "harness: grounding data"); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}

	// Simulate agent work (plain git, no SCM credentials).
	os.WriteFile(filepath.Join(cloneDir, "migrated.java"), []byte("class Foo {}\n"), 0644)
	runGit(t, cloneDir, "add", "migrated.java")
	runGit(t, cloneDir, "commit", "-m", "agent: migrate Foo.java")
	expectedHash := runGit(t, cloneDir, "rev-parse", "HEAD")

	// Harness pushes agent's work.
	ctx := context.Background()
	if _, err := repo.Push(ctx, ""); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Verify on remote.
	verifyDir := filepath.Join(t.TempDir(), "verify")
	runGit(t, "", "clone", "--branch", "migration-test", remoteDir, verifyDir)
	remoteHash := runGit(t, verifyDir, "rev-parse", "HEAD")
	if remoteHash != expectedHash {
		t.Errorf("remote HEAD = %s, want %s", remoteHash, expectedHash)
	}
	if _, err := os.Stat(filepath.Join(verifyDir, "migrated.java")); err != nil {
		t.Error("migrated.java not found on remote")
	}
}
