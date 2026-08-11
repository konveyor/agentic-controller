package watcher

var excludeDirs = map[string]bool{
	".agents": true, ".goose": true, "__pycache__": true, ".git": true,
	"node_modules": true, "target": true, "graphify-out": true,
}

var excludeExts = map[string]bool{
	".tmp": true, ".swp": true, ".bak": true,
}
