package watcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/konveyor/migration-harness/internal/logging"
)

// DefaultQuietPeriod is the debounce window: after the last relevant
// filesystem event, the watcher waits this long before committing.
// .konveyor/ is excluded from watching (results.json is written once at
// stage end) but NOT from ShouldStageNewFile, so the final CommitAll
// in main.go picks it up.
const DefaultQuietPeriod = 30 * time.Second

type CommitPushFn func() error

type Watcher struct {
	dir         string
	commitFn    CommitPushFn
	fsw         *fsnotify.Watcher
	quietPeriod time.Duration
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func New(dir string, commitFn CommitPushFn) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		dir:         dir,
		commitFn:    commitFn,
		fsw:         fsw,
		quietPeriod: DefaultQuietPeriod,
	}, nil
}

// WithQuietPeriod sets a custom debounce period (for testing).
func (w *Watcher) WithQuietPeriod(d time.Duration) *Watcher {
	w.quietPeriod = d
	return w
}

func (w *Watcher) Start(ctx context.Context) error {
	if err := w.addDirRecursive(w.dir); err != nil {
		return err
	}

	ctx, w.cancel = context.WithCancel(ctx)
	w.wg.Add(1)
	go w.loop(ctx)
	logging.Info("filesystem watcher started (quiet period: %s)", w.quietPeriod)
	return nil
}

func (w *Watcher) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	w.fsw.Close()
}

func (w *Watcher) loop(ctx context.Context) {
	defer w.wg.Done()
	timer := time.NewTimer(w.quietPeriod)
	timer.Stop()
	dirty := false

	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			rel, err := filepath.Rel(w.dir, event.Name)
			if err != nil {
				continue
			}
			if !isRelevantChange(rel) {
				continue
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					// Only add the directory if it's not excluded
					dirName := filepath.Base(event.Name)
					if !excludeDirs[dirName] && dirName != ".konveyor" {
						w.fsw.Add(event.Name)
					}
				}
			}
			dirty = true
			timer.Reset(w.quietPeriod)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			logging.Warn("watcher error: %v", err)
		case <-timer.C:
			if dirty {
				w.doCommit()
				dirty = false
			}
		}
	}
}

func isRelevantChange(relPath string) bool {
	// Check if the path itself or any of its directory components are excluded
	parts := strings.Split(relPath, string(filepath.Separator))
	for _, part := range parts {
		if excludeDirs[part] {
			return false
		}
	}

	base := filepath.Base(relPath)
	ext := filepath.Ext(base)
	return !excludeExts[ext]
}

func (w *Watcher) doCommit() {
	if err := w.stageFiles(); err != nil {
		logging.Warn("watcher stage: %v", err)
		return
	}
	if err := w.commitFn(); err != nil {
		logging.Warn("watcher commit+push: %v", err)
	}
}

func (w *Watcher) stageFiles() error {
	cmd := exec.Command("git", "-C", w.dir, "add", "-u")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add -u: %s: %w", out, err)
	}

	entries, err := w.findNewFiles()
	if err != nil {
		return err
	}
	for _, f := range entries {
		cmd := exec.Command("git", "-C", w.dir, "add", "--", f)
		if out, err := cmd.CombinedOutput(); err != nil {
			logging.Warn("git add %s: %s", f, out)
		}
	}
	return nil
}

func (w *Watcher) findNewFiles() ([]string, error) {
	cmd := exec.Command("git", "-C", w.dir, "ls-files", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var staged []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && ShouldStageNewFile(line) {
			staged = append(staged, line)
		}
	}
	return staged, nil
}

func (w *Watcher) addDirRecursive(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if excludeDirs[name] || name == ".konveyor" {
				return filepath.SkipDir
			}
			return w.fsw.Add(path)
		}
		return nil
	})
}
