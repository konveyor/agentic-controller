package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/konveyor/migration-harness/internal/logging"
)

type Manifests struct {
	PomXML         bool `json:"pom_xml"`
	PackageJSON    bool `json:"package_json"`
	PyprojectTOML  bool `json:"pyproject_toml"`
	RequirementsTXT bool `json:"requirements_txt"`
	SetupPy        bool `json:"setup_py"`
	GoMod          bool `json:"go_mod"`
	CargoTOML      bool `json:"cargo_toml"`
	Gemfile        bool `json:"gemfile"`
}

type FileCounts struct {
	Java       int `json:"java"`
	Python     int `json:"python"`
	JavaScript int `json:"javascript"`
	TypeScript int `json:"typescript"`
	Go         int `json:"go"`
	Rust       int `json:"rust"`
	CSharp     int `json:"csharp"`
	Ruby       int `json:"ruby"`
}

type GraphStats struct {
	Nodes       int `json:"nodes"`
	Edges       int `json:"edges"`
	Communities int `json:"communities"`
	GodNodes    int `json:"god_nodes"`
}

type DetectResult struct {
	Repo      string     `json:"repo"`
	Manifests Manifests  `json:"manifests"`
	Files     FileCounts `json:"files"`
	Graph     GraphStats `json:"graph"`
	GraphFile string     `json:"graph_file"`
}

type GraphJSON struct {
	Nodes       []GraphNode      `json:"nodes"`
	Links       []GraphLink      `json:"links"`
	Communities []GraphCommunity  `json:"communities"`
}

type GraphNode struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	SourceFile string `json:"source_file"`
	Degree     int    `json:"degree"`
}

type GraphLink struct {
	Source   string `json:"source"`
	Target  string `json:"target"`
	Relation string `json:"relation"`
}

type GraphCommunity struct {
	ID    any `json:"id"`
	Nodes []string    `json:"nodes"`
}

func Run(ctx context.Context, repoDir, runDir string) (*DetectResult, error) {
	logging.Header("Step 1: Detect")

	ensureGitignore(repoDir)

	logging.Info("1a. Checking manifest files...")
	manifests := checkManifests(repoDir)

	logging.Info("1b. Building code graph...")
	if err := runGraphify(ctx, repoDir); err != nil {
		return nil, fmt.Errorf("graphify: %w", err)
	}

	graphifyOut := filepath.Join(repoDir, "graphify-out")
	graphPath := filepath.Join(graphifyOut, "graph.json")
	reportPath := filepath.Join(graphifyOut, "GRAPH_REPORT.md")

	graph, err := parseGraphJSON(graphPath)
	if err != nil {
		return nil, fmt.Errorf("parse graph.json: %w", err)
	}

	logging.Info("1c. Counting source files...")
	files := countFiles(graph)

	stats := computeStats(graph)

	result := &DetectResult{
		Repo:      repoDir,
		Manifests: manifests,
		Files:     files,
		Graph:     stats,
		GraphFile: "graph.json",
	}

	// Copy artifacts to run dir
	copyFile(graphPath, filepath.Join(runDir, "graph.json"))
	copyFile(reportPath, filepath.Join(runDir, "GRAPH_REPORT.md"))

	detectPath := filepath.Join(runDir, "detect.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal detect.json: %w", err)
	}
	if err := os.WriteFile(detectPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write detect.json: %w", err)
	}

	logging.Ok("Detect complete: %d nodes, %d edges, %d communities",
		stats.Nodes, stats.Edges, stats.Communities)
	return result, nil
}

func checkManifests(repoDir string) Manifests {
	m := Manifests{}
	check := func(file string) bool {
		_, err := os.Stat(filepath.Join(repoDir, file))
		return err == nil
	}
	m.PomXML = check("pom.xml")
	m.PackageJSON = check("package.json")
	m.PyprojectTOML = check("pyproject.toml")
	m.RequirementsTXT = check("requirements.txt")
	m.SetupPy = check("setup.py")
	m.GoMod = check("go.mod")
	m.CargoTOML = check("Cargo.toml")
	m.Gemfile = check("Gemfile")
	return m
}

func runGraphify(ctx context.Context, repoDir string) error {
	graphifyPath, err := exec.LookPath("graphify")
	if err != nil {
		return fmt.Errorf("graphify not found in PATH: %w", err)
	}

	cmd := exec.CommandContext(ctx, graphifyPath, "update", repoDir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("graphify update: %w", err)
	}

	graphPath := filepath.Join(repoDir, "graphify-out", "graph.json")
	if _, err := os.Stat(graphPath); os.IsNotExist(err) {
		return fmt.Errorf("graphify did not produce graph.json at %s", graphPath)
	}

	return nil
}

func parseGraphJSON(path string) (*GraphJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var g GraphJSON
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

func countFiles(graph *GraphJSON) FileCounts {
	fc := FileCounts{}
	for _, node := range graph.Nodes {
		sf := strings.ToLower(node.SourceFile)
		switch {
		case strings.HasSuffix(sf, ".java"):
			fc.Java++
		case strings.HasSuffix(sf, ".py"):
			fc.Python++
		case strings.HasSuffix(sf, ".js") || strings.HasSuffix(sf, ".jsx"):
			fc.JavaScript++
		case strings.HasSuffix(sf, ".ts") || strings.HasSuffix(sf, ".tsx"):
			fc.TypeScript++
		case strings.HasSuffix(sf, ".go"):
			fc.Go++
		case strings.HasSuffix(sf, ".rs"):
			fc.Rust++
		case strings.HasSuffix(sf, ".cs"):
			fc.CSharp++
		case strings.HasSuffix(sf, ".rb"):
			fc.Ruby++
		}
	}
	return fc
}

func computeStats(graph *GraphJSON) GraphStats {
	godCount := 0
	for _, node := range graph.Nodes {
		if node.Degree > 20 {
			godCount++
		}
	}
	return GraphStats{
		Nodes:       len(graph.Nodes),
		Edges:       len(graph.Links),
		Communities: len(graph.Communities),
		GodNodes:    godCount,
	}
}

func ensureGitignore(repoDir string) {
	path := filepath.Join(repoDir, ".gitignore")
	entry := "graphify-out/"

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), entry) {
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	if len(data) > 0 && data[len(data)-1] != '\n' {
		f.WriteString("\n")
	}
	f.WriteString(entry + "\n")
}

func copyFile(src, dst string) {
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	os.WriteFile(dst, data, 0644)
}
