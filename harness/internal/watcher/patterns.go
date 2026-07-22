package watcher

import (
	"path/filepath"
	"strings"
)

var sourceExts = map[string]bool{
	".java": true, ".xml": true, ".properties": true,
	".md": true, ".json": true, ".yaml": true, ".yml": true,
	".gradle": true, ".kt": true, ".groovy": true,
}

var excludeDirs = map[string]bool{
	".goose": true, "__pycache__": true, ".git": true,
	"node_modules": true, "target": true, "graphify-out": true,
}

var excludeExts = map[string]bool{
	".tmp": true, ".swp": true, ".bak": true,
}

func ShouldStageNewFile(path string) bool {
	base := filepath.Base(path)

	if base == "pom.xml" || base == "results.json" {
		return true
	}

	for _, part := range strings.Split(filepath.Dir(path), string(filepath.Separator)) {
		if excludeDirs[part] {
			return false
		}
	}

	ext := filepath.Ext(base)
	if excludeExts[ext] {
		return false
	}

	return sourceExts[ext]
}
