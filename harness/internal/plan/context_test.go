package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konveyor/migration-harness/internal/detect"
)

func TestGatherContextBasic(t *testing.T) {
	runDir := t.TempDir()
	skillDir := t.TempDir()

	dr := detect.DetectResult{
		Repo:      "/test/repo",
		Manifests: detect.Manifests{PomXML: true},
		Files:     detect.FileCounts{Java: 10},
		Graph:     detect.GraphStats{Nodes: 5, Edges: 3},
	}
	data, _ := json.MarshalIndent(dr, "", "  ")
	os.WriteFile(filepath.Join(runDir, "detect.json"), data, 0644)

	ctx, err := GatherContext(runDir, skillDir)
	if err != nil {
		t.Fatalf("GatherContext: %v", err)
	}

	if !strings.Contains(ctx, "DETECTION SUMMARY") {
		t.Error("missing DETECTION SUMMARY")
	}
	if !strings.Contains(ctx, "pom_xml") {
		t.Error("missing pom_xml in context")
	}
}

func TestGatherContextWithGraph(t *testing.T) {
	runDir := t.TempDir()
	skillDir := t.TempDir()

	dr := detect.DetectResult{Files: detect.FileCounts{Java: 5}}
	data, _ := json.MarshalIndent(dr, "", "  ")
	os.WriteFile(filepath.Join(runDir, "detect.json"), data, 0644)

	graph := detect.GraphJSON{
		Nodes: []detect.GraphNode{
			{ID: "1", Label: "ClassA", SourceFile: "A.java", Degree: 25},
			{ID: "2", Label: "ClassB", SourceFile: "B.java", Degree: 5},
		},
		Links: []detect.GraphLink{
			{Source: "1", Target: "2", Relation: "imports"},
		},
		Communities: []detect.GraphCommunity{
			{ID: 0, Nodes: []string{"1", "2"}},
		},
	}
	gdata, _ := json.Marshal(graph)
	os.WriteFile(filepath.Join(runDir, "graph.json"), gdata, 0644)

	ctx, err := GatherContext(runDir, skillDir)
	if err != nil {
		t.Fatalf("GatherContext: %v", err)
	}

	checks := []string{
		"CODE GRAPH OVERVIEW",
		"ARCHITECTURAL LAYERS",
		"GOD NODES",
		"FILE TREE",
		"ClassA",
		"A.java",
	}
	for _, c := range checks {
		if !strings.Contains(ctx, c) {
			t.Errorf("missing %q in context", c)
		}
	}
}

func TestGatherContextWithRefs(t *testing.T) {
	runDir := t.TempDir()
	skillDir := t.TempDir()

	dr := detect.DetectResult{Files: detect.FileCounts{Java: 1}}
	data, _ := json.MarshalIndent(dr, "", "  ")
	os.WriteFile(filepath.Join(runDir, "detect.json"), data, 0644)

	refsDir := filepath.Join(skillDir, "references")
	os.MkdirAll(refsDir, 0755)
	os.WriteFile(filepath.Join(refsDir, "javaee-quarkus.md"), []byte("# ref"), 0644)

	ctx, err := GatherContext(runDir, skillDir)
	if err != nil {
		t.Fatalf("GatherContext: %v", err)
	}

	if !strings.Contains(ctx, "AVAILABLE MIGRATION REFERENCES") {
		t.Error("missing references section")
	}
	if !strings.Contains(ctx, "javaee-quarkus.md") {
		t.Error("missing reference file name")
	}
}
