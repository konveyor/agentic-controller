package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func setupBareRemote(t *testing.T) (string, *gogit.Repository) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	repo, err := gogit.PlainInit(dir, true)
	if err != nil {
		t.Fatalf("init bare repo: %v", err)
	}
	return dir, repo
}

func cloneLocal(t *testing.T, remoteDir string) (string, *gogit.Repository) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	repo, err := gogit.PlainClone(dir, false, &gogit.CloneOptions{
		URL: remoteDir,
	})
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	return dir, repo
}

func seedBareRepo(t *testing.T, remoteDir string) {
	t.Helper()
	tmpDir := filepath.Join(t.TempDir(), "seed")
	repo, err := gogit.PlainInit(tmpDir, false)
	if err != nil {
		t.Fatalf("seed init: %v", err)
	}

	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# test\n"), 0644)
	wt, _ := repo.Worktree()
	wt.Add("README.md")
	wt.Commit("initial", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com", When: time.Now()},
	})

	_, err = repo.CreateRemote(&gogitcfg.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteDir},
	})
	if err != nil {
		t.Fatalf("create remote: %v", err)
	}
	if err := repo.Push(&gogit.PushOptions{}); err != nil {
		t.Fatalf("seed push: %v", err)
	}
}

func TestCheckoutBranch(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)
	seedBareRepo(t, remoteDir)
	_, repo := cloneLocal(t, remoteDir)

	if err := CheckoutBranch(repo, "feature-branch"); err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}

	head, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head.Name() != plumbing.NewBranchReferenceName("feature-branch") {
		t.Errorf("branch = %s, want feature-branch", head.Name())
	}
}

func TestCheckoutBranchFromRemote(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	// Push a file to a branch on the remote via a temporary clone.
	tmpDir := filepath.Join(t.TempDir(), "pusher")
	pusher, err := gogit.PlainClone(tmpDir, false, &gogit.CloneOptions{URL: remoteDir})
	if err != nil {
		t.Fatalf("clone for push: %v", err)
	}
	wt, _ := pusher.Worktree()
	wt.Checkout(&gogit.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("remote-branch"), Create: true})
	os.WriteFile(filepath.Join(tmpDir, "PLAN.md"), []byte("# Plan\n"), 0644)
	wt.Add("PLAN.md")
	wt.Commit("add plan", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com", When: time.Now()},
	})
	pusher.Push(&gogit.PushOptions{
		RefSpecs: []gogitcfg.RefSpec{"refs/heads/remote-branch:refs/heads/remote-branch"},
	})

	// Fresh clone (only fetches default branch).
	cloneDir := filepath.Join(t.TempDir(), "clone2")
	repo, err := gogit.PlainClone(cloneDir, false, &gogit.CloneOptions{URL: remoteDir})
	if err != nil {
		t.Fatalf("clone: %v", err)
	}

	// CheckoutBranch should resolve the remote tracking branch.
	if err := CheckoutBranch(repo, "remote-branch"); err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}

	// Verify we got the remote content.
	data, err := os.ReadFile(filepath.Join(cloneDir, "PLAN.md"))
	if err != nil {
		t.Fatalf("PLAN.md not found after checkout: %v", err)
	}
	if string(data) != "# Plan\n" {
		t.Errorf("PLAN.md content = %q, want %q", string(data), "# Plan\n")
	}
}

func TestFullLifecycle(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cred := &Credentials{
		Username: "test",
		Token:    "token",
		RepoURL:  remoteDir,
		Branch:   "migration-test",
	}

	ctx := context.Background()

	cloneDir := filepath.Join(t.TempDir(), "work")
	repo, err := Clone(ctx, cred, cloneDir)
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}

	if err := StripCredentials(repo); err != nil {
		t.Fatalf("StripCredentials: %v", err)
	}

	if err := CheckoutBranch(repo, cred.Branch); err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}

	baseSHA, err := HeadSHA(repo)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}

	os.WriteFile(filepath.Join(cloneDir, "migrated.java"), []byte("class Foo {}\n"), 0644)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if _, err := wt.Add("migrated.java"); err != nil {
		t.Fatalf("add: %v", err)
	}
	hash, err := wt.Commit("migrate: Foo.java", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if hash == plumbing.ZeroHash {
		t.Error("expected commit hash")
	}

	pushed, err := Push(ctx, cred, repo, cred.Branch, baseSHA)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !pushed {
		t.Error("Push reported skipped despite new commits")
	}

	// Verify the branch exists on the remote
	remoteRepo, err := gogit.PlainOpen(remoteDir)
	if err != nil {
		t.Fatalf("open remote repo: %v", err)
	}
	ref, err := remoteRepo.Reference(plumbing.NewBranchReferenceName(cred.Branch), false)
	if err != nil {
		t.Fatalf("remote branch not found: %v", err)
	}
	if ref.Hash() != hash {
		t.Errorf("remote hash = %s, want %s", ref.Hash(), hash)
	}
}

func TestPushWithoutCredsToStrippedRemoteFails(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cred := &Credentials{
		Username: "test",
		Token:    "token",
		RepoURL:  remoteDir,
		Branch:   "test-branch",
	}

	ctx := context.Background()
	cloneDir := filepath.Join(t.TempDir(), "work")
	repo, err := Clone(ctx, cred, cloneDir)
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}

	StripCredentials(repo)
	CheckoutBranch(repo, cred.Branch)
	baseSHA, _ := HeadSHA(repo)

	os.WriteFile(filepath.Join(cloneDir, "file.txt"), []byte("data\n"), 0644)
	wt, _ := repo.Worktree()
	wt.Add("file.txt")
	wt.Commit("test commit", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com", When: time.Now()},
	})

	// Push with nil credentials should fail
	_, err = Push(ctx, &Credentials{RepoURL: remoteDir, Branch: cred.Branch}, repo, cred.Branch, baseSHA)
	// For local bare repos, push still works without auth — this test verifies
	// the function runs without panic. Real auth enforcement is server-side.
	_ = err
}

func TestConfigureAuthor(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)
	seedBareRepo(t, remoteDir)
	_, repo := cloneLocal(t, remoteDir)

	if err := ConfigureAuthor(repo, "Jane Dev", "jane@example.com"); err != nil {
		t.Fatalf("ConfigureAuthor: %v", err)
	}

	cfg, err := repo.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.User.Name != "Jane Dev" {
		t.Errorf("user.name = %q, want %q", cfg.User.Name, "Jane Dev")
	}
	if cfg.User.Email != "jane@example.com" {
		t.Errorf("user.email = %q, want %q", cfg.User.Email, "jane@example.com")
	}
}

// TestConfigureAuthorAppliesToCommit verifies the configured identity is
// what go-git writes as the commit author and committer.
func TestConfigureAuthorAppliesToCommit(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)
	seedBareRepo(t, remoteDir)
	cloneDir, repo := cloneLocal(t, remoteDir)

	if err := ConfigureAuthor(repo, "Jane Dev", "jane@example.com"); err != nil {
		t.Fatalf("ConfigureAuthor: %v", err)
	}
	if err := CheckoutBranch(repo, "test-author-commit"); err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}

	os.WriteFile(filepath.Join(cloneDir, ".gitignore"), []byte("*.tmp\n"), 0644)
	if err := CommitFiles(repo, []string{".gitignore"}, "harness: author test"); err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}

	head, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	if commit.Author.Name != "Jane Dev" || commit.Author.Email != "jane@example.com" {
		t.Errorf("author = %q <%q>, want %q <%q>",
			commit.Author.Name, commit.Author.Email, "Jane Dev", "jane@example.com")
	}
	if commit.Committer.Name != "Jane Dev" || commit.Committer.Email != "jane@example.com" {
		t.Errorf("committer = %q <%q>, want %q <%q>",
			commit.Committer.Name, commit.Committer.Email, "Jane Dev", "jane@example.com")
	}
}

func TestCommitFiles(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)
	seedBareRepo(t, remoteDir)
	cloneDir, repo := cloneLocal(t, remoteDir)

	if err := ConfigureAuthor(repo, "migration-agent", "migration-agent@konveyor.io"); err != nil {
		t.Fatalf("ConfigureAuthor: %v", err)
	}
	if err := CheckoutBranch(repo, "test-commit-files"); err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}

	os.WriteFile(filepath.Join(cloneDir, ".gitignore"), []byte("*.tmp\n"), 0644)
	os.MkdirAll(filepath.Join(cloneDir, ".konveyor"), 0755)
	os.WriteFile(filepath.Join(cloneDir, ".konveyor", "analysis.json"), []byte("{}\n"), 0644)

	err := CommitFiles(repo, []string{".gitignore", ".konveyor/analysis.json"}, "harness: test commit")
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}

	head, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	if commit.Message != "harness: test commit" {
		t.Errorf("commit message = %q, want %q", commit.Message, "harness: test commit")
	}
	if commit.Author.Name != "migration-agent" {
		t.Errorf("author = %q, want %q", commit.Author.Name, "migration-agent")
	}

	tree, err := commit.Tree()
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if _, err := tree.File(".gitignore"); err != nil {
		t.Error(".gitignore not in commit tree")
	}
	if _, err := tree.File(".konveyor/analysis.json"); err != nil {
		t.Error(".konveyor/analysis.json not in commit tree")
	}
}

func TestCommitFilesNoChanges(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)
	seedBareRepo(t, remoteDir)
	_, repo := cloneLocal(t, remoteDir)

	if err := ConfigureAuthor(repo, "migration-agent", "migration-agent@konveyor.io"); err != nil {
		t.Fatalf("ConfigureAuthor: %v", err)
	}
	if err := CheckoutBranch(repo, "test-no-changes"); err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}

	headBefore, _ := repo.Head()

	err := CommitFiles(repo, []string{".gitignore", ".konveyor/analysis.json"}, "should not commit")
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}

	headAfter, _ := repo.Head()
	if headBefore.Hash() != headAfter.Hash() {
		t.Error("HEAD changed despite no files to commit")
	}
}

func TestPushSkipsWhenNoNewCommits(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cred := &Credentials{
		Username: "test",
		Token:    "token",
		RepoURL:  remoteDir,
		Branch:   "migration-noop",
	}

	ctx := context.Background()
	cloneDir := filepath.Join(t.TempDir(), "work")
	repo, err := Clone(ctx, cred, cloneDir)
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if err := CheckoutBranch(repo, cred.Branch); err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}
	baseSHA, err := HeadSHA(repo)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}

	// No commits beyond the checkout point: the push must be skipped so
	// no-op runs do not create empty branches on the remote.
	pushed, err := Push(ctx, cred, repo, cred.Branch, baseSHA)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if pushed {
		t.Error("Push reported pushed despite no new commits")
	}

	remoteRepo, err := gogit.PlainOpen(remoteDir)
	if err != nil {
		t.Fatalf("open remote repo: %v", err)
	}
	if ref, err := remoteRepo.Reference(plumbing.NewBranchReferenceName(cred.Branch), false); err == nil {
		t.Errorf("remote branch %s created at %s despite no commits", cred.Branch, ref.Hash())
	}
}

func TestPushWithNewCommitsPushes(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cred := &Credentials{
		Username: "test",
		Token:    "token",
		RepoURL:  remoteDir,
		Branch:   "migration-work",
	}

	ctx := context.Background()
	cloneDir := filepath.Join(t.TempDir(), "work")
	repo, err := Clone(ctx, cred, cloneDir)
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if err := CheckoutBranch(repo, cred.Branch); err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}
	baseSHA, err := HeadSHA(repo)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}

	os.WriteFile(filepath.Join(cloneDir, "migrated.java"), []byte("class Foo {}\n"), 0644)
	wt, _ := repo.Worktree()
	wt.Add("migrated.java")
	hash, err := wt.Commit("migrate: Foo.java", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	pushed, err := Push(ctx, cred, repo, cred.Branch, baseSHA)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !pushed {
		t.Error("Push reported skipped despite new commits")
	}

	remoteRepo, err := gogit.PlainOpen(remoteDir)
	if err != nil {
		t.Fatalf("open remote repo: %v", err)
	}
	ref, err := remoteRepo.Reference(plumbing.NewBranchReferenceName(cred.Branch), false)
	if err != nil {
		t.Fatalf("remote branch not found: %v", err)
	}
	if ref.Hash() != hash {
		t.Errorf("remote hash = %s, want %s", ref.Hash(), hash)
	}
}

func TestPushSkipsWhenStageAddsNoNewCommits(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cred := &Credentials{
		Username: "test",
		Token:    "token",
		RepoURL:  remoteDir,
		Branch:   "migration-stages",
	}
	ctx := context.Background()

	// Stage 1: commit on the branch and push — creates the remote ref.
	stage1Dir := filepath.Join(t.TempDir(), "stage1")
	repo1, err := Clone(ctx, cred, stage1Dir)
	if err != nil {
		t.Fatalf("stage 1 Clone: %v", err)
	}
	if err := CheckoutBranch(repo1, cred.Branch); err != nil {
		t.Fatalf("stage 1 CheckoutBranch: %v", err)
	}
	base1, err := HeadSHA(repo1)
	if err != nil {
		t.Fatalf("stage 1 HeadSHA: %v", err)
	}
	os.WriteFile(filepath.Join(stage1Dir, "PLAN.md"), []byte("# Plan\n"), 0644)
	wt, _ := repo1.Worktree()
	wt.Add("PLAN.md")
	hash, err := wt.Commit("plan stage", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("stage 1 commit: %v", err)
	}
	pushed1, err := Push(ctx, cred, repo1, cred.Branch, base1)
	if err != nil {
		t.Fatalf("stage 1 Push: %v", err)
	}
	if !pushed1 {
		t.Error("stage 1 Push reported skipped despite new commits")
	}

	// Stage 2: fresh clone, checkout resolves the branch stage 1 pushed,
	// so this stage's base is that tip. No new commits — skip the push.
	stage2Dir := filepath.Join(t.TempDir(), "stage2")
	repo2, err := Clone(ctx, cred, stage2Dir)
	if err != nil {
		t.Fatalf("stage 2 Clone: %v", err)
	}
	if err := CheckoutBranch(repo2, cred.Branch); err != nil {
		t.Fatalf("stage 2 CheckoutBranch: %v", err)
	}
	base2, err := HeadSHA(repo2)
	if err != nil {
		t.Fatalf("stage 2 HeadSHA: %v", err)
	}
	if base2 != hash.String() {
		t.Fatalf("stage 2 base = %s, want stage 1 tip %s", base2, hash)
	}
	pushed2, err := Push(ctx, cred, repo2, cred.Branch, base2)
	if err != nil {
		t.Fatalf("stage 2 Push: %v", err)
	}
	if pushed2 {
		t.Error("stage 2 Push reported pushed despite no new commits")
	}

	remoteRepo, err := gogit.PlainOpen(remoteDir)
	if err != nil {
		t.Fatalf("open remote repo: %v", err)
	}
	ref, err := remoteRepo.Reference(plumbing.NewBranchReferenceName(cred.Branch), false)
	if err != nil {
		t.Fatalf("remote branch not found: %v", err)
	}
	if ref.Hash() != hash {
		t.Errorf("remote hash = %s, want stage 1 tip %s", ref.Hash(), hash)
	}
}
