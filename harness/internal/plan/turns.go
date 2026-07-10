package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/konveyor/migration-harness/internal/detect"
)

func CalcPlanTurns(runDir, skillDir string) int {
	turns := 10

	detectPath := filepath.Join(runDir, "detect.json")
	data, err := os.ReadFile(detectPath)
	if err != nil {
		return clampTurns(turns)
	}

	var dr detect.DetectResult
	if err := json.Unmarshal(data, &dr); err != nil {
		return clampTurns(turns)
	}

	if !hasMatchingReference(skillDir, dr) {
		turns += 2
	}

	primaryFiles := maxLangCount(dr.Files)
	turns += primaryFiles / 50

	langCount := countLanguages(dr.Files)
	if langCount > 2 {
		turns += 3
	}

	return clampTurns(turns)
}

func hasMatchingReference(skillDir string, dr detect.DetectResult) bool {
	refsDir := filepath.Join(skillDir, "references")
	if _, err := os.Stat(refsDir); err != nil {
		return false
	}

	if dr.Manifests.PomXML {
		if _, err := os.Stat(filepath.Join(refsDir, "javaee-quarkus.md")); err == nil {
			return true
		}
	}

	if dr.Files.Python > 0 {
		entries, err := os.ReadDir(refsDir)
		if err == nil {
			for _, e := range entries {
				if strings.Contains(strings.ToLower(e.Name()), "python") {
					return true
				}
			}
		}
	}

	if dr.Files.CSharp > 0 {
		if _, err := os.Stat(filepath.Join(refsDir, "dotnet-framework-to-core.md")); err == nil {
			return true
		}
	}

	return false
}

func maxLangCount(fc detect.FileCounts) int {
	counts := []int{fc.Java, fc.Python, fc.JavaScript, fc.TypeScript, fc.Go, fc.Rust, fc.CSharp, fc.Ruby}
	max := 0
	for _, c := range counts {
		if c > max {
			max = c
		}
	}
	return max
}

func countLanguages(fc detect.FileCounts) int {
	n := 0
	for _, c := range []int{fc.Java, fc.Python, fc.JavaScript, fc.TypeScript, fc.Go, fc.Rust, fc.CSharp, fc.Ruby} {
		if c > 0 {
			n++
		}
	}
	return n
}

func clampTurns(turns int) int {
	if turns < 12 {
		return 12
	}
	if turns > 50 {
		return 50
	}
	return turns
}
