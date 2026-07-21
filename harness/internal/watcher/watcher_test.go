package watcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

const testQuietPeriod = 1 * time.Second

func TestWatcherDetectsFileChange(t *testing.T) {
	dir := t.TempDir()

	// Initialize a git repo so git add -u works
	runGit(t, dir, "init")
	writeFile(t, filepath.Join(dir, "App.java"), "class App {}")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")

	var commitCount atomic.Int32
	commitFn := func() error {
		commitCount.Add(1)
		return nil
	}

	w, err := New(dir, commitFn)
	if err != nil {
		t.Fatal(err)
	}
	w.WithQuietPeriod(testQuietPeriod)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	// Modify a tracked file
	writeFile(t, filepath.Join(dir, "App.java"), "class App { int x; }")

	// Wait for quiet period + buffer
	time.Sleep(testQuietPeriod + 2*time.Second)

	if commitCount.Load() == 0 {
		t.Error("expected at least one commit after quiet period")
	}
}

func TestWatcherIgnoresExcludedDirs(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	writeFile(t, filepath.Join(dir, "App.java"), "class App {}")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")

	var commitCount atomic.Int32
	commitFn := func() error {
		commitCount.Add(1)
		return nil
	}

	w, err := New(dir, commitFn)
	if err != nil {
		t.Fatal(err)
	}
	w.WithQuietPeriod(testQuietPeriod)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	// Write to an excluded dir — should NOT trigger commit
	gooseDir := filepath.Join(dir, ".goose")
	os.MkdirAll(gooseDir, 0755)
	writeFile(t, filepath.Join(gooseDir, "cache.db"), "data")

	time.Sleep(testQuietPeriod + 2*time.Second)

	if commitCount.Load() != 0 {
		t.Error("expected no commits for changes in excluded dirs")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}
