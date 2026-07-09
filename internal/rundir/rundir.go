package rundir

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func New(runsDir, repoName string) (string, error) {
	ts := time.Now().Format("20060102-150405")
	dir := filepath.Join(runsDir, fmt.Sprintf("%s-%s", repoName, ts))
	logsDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return "", fmt.Errorf("create run dir: %w", err)
	}
	return dir, nil
}

func Latest(runsDir string) (string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("read runs dir: %w", err)
	}

	var dirs []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e)
		}
	}
	if len(dirs) == 0 {
		return "", fmt.Errorf("no runs found in %s", runsDir)
	}

	sort.Slice(dirs, func(i, j int) bool {
		infoI, _ := dirs[i].Info()
		infoJ, _ := dirs[j].Info()
		if infoI == nil || infoJ == nil {
			return dirs[i].Name() < dirs[j].Name()
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})

	return filepath.Join(runsDir, dirs[0].Name()), nil
}

func DefaultRunsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".migration-harness", "runs")
}
