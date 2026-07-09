package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckManifests(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project/>"), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)

	m := checkManifests(dir)

	if !m.PomXML {
		t.Error("expected pom.xml detected")
	}
	if !m.GoMod {
		t.Error("expected go.mod detected")
	}
	if m.PackageJSON {
		t.Error("expected package.json NOT detected")
	}
	if m.CargoTOML {
		t.Error("expected Cargo.toml NOT detected")
	}
}

func TestCountFiles(t *testing.T) {
	graph := &GraphJSON{
		Nodes: []GraphNode{
			{ID: "1", SourceFile: "src/main/java/Foo.java"},
			{ID: "2", SourceFile: "src/main/java/Bar.java"},
			{ID: "3", SourceFile: "app.py"},
			{ID: "4", SourceFile: "index.js"},
			{ID: "5", SourceFile: "App.tsx"},
			{ID: "6", SourceFile: "main.go"},
		},
	}

	fc := countFiles(graph)

	if fc.Java != 2 {
		t.Errorf("Java = %d, want 2", fc.Java)
	}
	if fc.Python != 1 {
		t.Errorf("Python = %d, want 1", fc.Python)
	}
	if fc.JavaScript != 1 {
		t.Errorf("JavaScript = %d, want 1", fc.JavaScript)
	}
	if fc.TypeScript != 1 {
		t.Errorf("TypeScript = %d, want 1", fc.TypeScript)
	}
	if fc.Go != 1 {
		t.Errorf("Go = %d, want 1", fc.Go)
	}
}

func TestComputeStats(t *testing.T) {
	graph := &GraphJSON{
		Nodes: []GraphNode{
			{ID: "1", Degree: 5},
			{ID: "2", Degree: 25},
			{ID: "3", Degree: 30},
			{ID: "4", Degree: 10},
		},
		Links: []GraphLink{
			{Source: "1", Target: "2"},
			{Source: "2", Target: "3"},
			{Source: "3", Target: "4"},
		},
		Communities: []GraphCommunity{
			{ID: 0, Nodes: []string{"1", "2"}},
			{ID: 1, Nodes: []string{"3", "4"}},
		},
	}

	stats := computeStats(graph)

	if stats.Nodes != 4 {
		t.Errorf("Nodes = %d, want 4", stats.Nodes)
	}
	if stats.Edges != 3 {
		t.Errorf("Edges = %d, want 3", stats.Edges)
	}
	if stats.Communities != 2 {
		t.Errorf("Communities = %d, want 2", stats.Communities)
	}
	if stats.GodNodes != 2 {
		t.Errorf("GodNodes = %d, want 2", stats.GodNodes)
	}
}

func TestParseGraphJSON(t *testing.T) {
	dir := t.TempDir()
	graphPath := filepath.Join(dir, "graph.json")

	graph := GraphJSON{
		Nodes: []GraphNode{
			{ID: "a", Label: "ClassA", SourceFile: "A.java", Degree: 5},
		},
		Links: []GraphLink{
			{Source: "a", Target: "b", Relation: "imports"},
		},
		Communities: []GraphCommunity{
			{ID: 0, Nodes: []string{"a", "b"}},
		},
	}

	data, _ := json.Marshal(graph)
	os.WriteFile(graphPath, data, 0644)

	parsed, err := parseGraphJSON(graphPath)
	if err != nil {
		t.Fatalf("parseGraphJSON: %v", err)
	}

	if len(parsed.Nodes) != 1 {
		t.Errorf("Nodes = %d, want 1", len(parsed.Nodes))
	}
	if parsed.Nodes[0].SourceFile != "A.java" {
		t.Errorf("SourceFile = %q, want A.java", parsed.Nodes[0].SourceFile)
	}
}

func TestDetectResultJSON(t *testing.T) {
	result := DetectResult{
		Repo:      "/path/to/repo",
		Manifests: Manifests{PomXML: true, GoMod: true},
		Files:     FileCounts{Java: 10, Go: 5},
		Graph:     GraphStats{Nodes: 100, Edges: 200, Communities: 5, GodNodes: 2},
		GraphFile: "graph.json",
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed DetectResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !parsed.Manifests.PomXML {
		t.Error("expected pom_xml = true")
	}
	if parsed.Files.Java != 10 {
		t.Errorf("Java = %d, want 10", parsed.Files.Java)
	}
	if parsed.Graph.Nodes != 100 {
		t.Errorf("Nodes = %d, want 100", parsed.Graph.Nodes)
	}
}
