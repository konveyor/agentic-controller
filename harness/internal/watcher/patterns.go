package watcher

var excludeDirs = map[string]bool{
	".goose": true, "__pycache__": true, ".git": true,
	"node_modules": true, "target": true,
}

var excludeExts = map[string]bool{
	".tmp": true, ".swp": true, ".bak": true,
}
