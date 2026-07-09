package plan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/konveyor/migration-harness/internal/detect"
)

func GatherContext(runDir, skillDir string) (string, error) {
	var b strings.Builder

	b.WriteString("=== DETECTION SUMMARY ===\n")
	detectData, err := os.ReadFile(filepath.Join(runDir, "detect.json"))
	if err != nil {
		return "", fmt.Errorf("read detect.json: %w", err)
	}
	b.Write(detectData)
	b.WriteString("\n\n")

	graphPath := filepath.Join(runDir, "graph.json")
	if _, err := os.Stat(graphPath); err == nil {
		if err := writeGraphContext(&b, graphPath); err != nil {
			return "", err
		}
	} else {
		b.WriteString("=== FILE TREE ===\n")
		b.WriteString("(Graph not available)\n\n")
	}

	refsDir := filepath.Join(skillDir, "references")
	if entries, err := os.ReadDir(refsDir); err == nil {
		b.WriteString("=== AVAILABLE MIGRATION REFERENCES ===\n")
		fmt.Fprintf(&b, "Directory: %s/\n", refsDir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				fmt.Fprintf(&b, "  - %s\n", e.Name())
			}
		}
		b.WriteString("\nRead the reference that matches the detected migration type.\n\n")
	}

	return b.String(), nil
}

func writeGraphContext(b *strings.Builder, graphPath string) error {
	data, err := os.ReadFile(graphPath)
	if err != nil {
		return fmt.Errorf("read graph.json: %w", err)
	}
	var g detect.GraphJSON
	if err := json.Unmarshal(data, &g); err != nil {
		return fmt.Errorf("parse graph.json: %w", err)
	}

	fileTypes := make(map[string]int)
	for _, n := range g.Nodes {
		if n.SourceFile == "" {
			continue
		}
		parts := strings.Split(n.SourceFile, ".")
		if len(parts) > 1 {
			ext := parts[len(parts)-1]
			if ext != "" {
				fileTypes[ext]++
			}
		}
	}

	b.WriteString("=== CODE GRAPH OVERVIEW ===\n")
	ftJSON, _ := json.Marshal(fileTypes)
	fmt.Fprintf(b, `{"nodes":%d,"edges":%d,"communities":%d,"file_types":%s}`,
		len(g.Nodes), len(g.Links), len(g.Communities), ftJSON)
	b.WriteString("\n\n")

	b.WriteString("=== ARCHITECTURAL LAYERS (communities from graph clustering) ===\n")
	fmt.Fprintf(b, "Graphify detected %d architectural clusters via community detection.\n", len(g.Communities))
	b.WriteString("These map to natural boundaries in your codebase (layers, modules, subsystems).\n\n")

	type comSize struct {
		id   string
		size int
	}
	communityMap := make(map[string]int)
	for _, c := range g.Communities {
		key := fmt.Sprintf("%v", c.ID)
		communityMap[key] += len(c.Nodes)
	}
	var comms []comSize
	for id, size := range communityMap {
		comms = append(comms, comSize{id, size})
	}
	sort.Slice(comms, func(i, j int) bool { return comms[i].size > comms[j].size })
	b.WriteString("Top 10 communities by size:\n")
	for i, c := range comms {
		if i >= 10 {
			break
		}
		fmt.Fprintf(b, "  - Community %s: %d nodes\n", c.id, c.size)
	}
	b.WriteString("\n")

	b.WriteString("=== GOD NODES (high-degree abstractions - handle with care) ===\n")
	b.WriteString("These nodes have many connections - changes here ripple across the system.\n")
	type nodeInfo struct {
		label      string
		degree     int
		sourceFile string
	}
	var sorted []nodeInfo
	for _, n := range g.Nodes {
		sorted = append(sorted, nodeInfo{n.Label, n.Degree, n.SourceFile})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].degree > sorted[j].degree })
	for i, n := range sorted {
		if i >= 10 {
			break
		}
		sf := n.sourceFile
		if sf == "" {
			sf = "unknown"
		}
		fmt.Fprintf(b, "  - %s (%d edges) → %s\n", n.label, n.degree, sf)
	}
	b.WriteString("\nMark god nodes as HIGH RISK in the migration plan.\n\n")

	b.WriteString("=== FILE TREE (from graph - source + config only) ===\n")
	var files []string
	seen := make(map[string]bool)
	for _, n := range g.Nodes {
		if n.SourceFile != "" && !seen[n.SourceFile] {
			files = append(files, n.SourceFile)
			seen[n.SourceFile] = true
		}
	}
	sort.Strings(files)
	for _, f := range files {
		fmt.Fprintf(b, "%s\n", f)
	}
	b.WriteString("\n")

	return nil
}
