package rundir

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	dir := t.TempDir()

	runDir, err := New(dir, "myapp")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		t.Fatalf("run dir not created: %s", runDir)
	}

	logsDir := filepath.Join(runDir, "logs")
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		t.Fatalf("logs dir not created: %s", logsDir)
	}
}

func TestLatest(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, "app-older"), 0755)
	time.Sleep(10 * time.Millisecond)
	os.MkdirAll(filepath.Join(dir, "app-newer"), 0755)

	latest, err := Latest(dir)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}

	if filepath.Base(latest) != "app-newer" {
		t.Errorf("Latest = %s, want app-newer", filepath.Base(latest))
	}
}

func TestLatestEmpty(t *testing.T) {
	dir := t.TempDir()

	_, err := Latest(dir)
	if err == nil {
		t.Fatal("expected error for empty runs dir")
	}
}
