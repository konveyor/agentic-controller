package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/konveyor/migration-harness/internal/detect"
)

func writeDetectJSON(t *testing.T, dir string, dr detect.DetectResult) {
	t.Helper()
	data, _ := json.Marshal(dr)
	os.WriteFile(filepath.Join(dir, "detect.json"), data, 0644)
}

func TestCalcPlanTurnsDefault(t *testing.T) {
	runDir := t.TempDir()
	writeDetectJSON(t, runDir, detect.DetectResult{
		Files: detect.FileCounts{Java: 10},
		Manifests: detect.Manifests{PomXML: true},
	})

	turns := CalcPlanTurns(runDir, "/nonexistent")
	if turns < 12 {
		t.Errorf("turns = %d, want >= 12", turns)
	}
}

func TestCalcPlanTurnsMultiLang(t *testing.T) {
	runDir := t.TempDir()
	writeDetectJSON(t, runDir, detect.DetectResult{
		Files: detect.FileCounts{Java: 10, Python: 5, Go: 3},
	})

	turns := CalcPlanTurns(runDir, "/nonexistent")
	if turns < 15 {
		t.Errorf("turns = %d, want >= 15 for multi-lang", turns)
	}
}

func TestCalcPlanTurnsLargeProject(t *testing.T) {
	runDir := t.TempDir()
	writeDetectJSON(t, runDir, detect.DetectResult{
		Files: detect.FileCounts{Java: 200},
	})

	turns := CalcPlanTurns(runDir, "/nonexistent")
	if turns < 14 {
		t.Errorf("turns = %d, want >= 14 for 200 files", turns)
	}
}

func TestCalcPlanTurnsClamp(t *testing.T) {
	if c := clampTurns(5); c != 12 {
		t.Errorf("clampTurns(5) = %d, want 12", c)
	}
	if c := clampTurns(100); c != 50 {
		t.Errorf("clampTurns(100) = %d, want 50", c)
	}
	if c := clampTurns(25); c != 25 {
		t.Errorf("clampTurns(25) = %d, want 25", c)
	}
}

func TestMaxLangCount(t *testing.T) {
	fc := detect.FileCounts{Java: 100, Python: 50, Go: 200}
	if m := maxLangCount(fc); m != 200 {
		t.Errorf("maxLangCount = %d, want 200", m)
	}
}

func TestCountLanguages(t *testing.T) {
	fc := detect.FileCounts{Java: 10, Python: 0, Go: 5}
	if c := countLanguages(fc); c != 2 {
		t.Errorf("countLanguages = %d, want 2", c)
	}
}
