package watcher

import "testing"

func TestShouldStageNewFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"src/main/java/com/example/App.java", true},
		{"pom.xml", true},
		{"src/main/resources/application.properties", true},
		{".konveyor/results.json", true},
		{"PLAN.md", true},
		{"graph.json", true},
		{".goose/cache/foo.txt", false},
		{"__pycache__/mod.pyc", false},
		{"target/classes/App.class", false},
		{"scratch.tmp", false},
		{"file.swp", false},
		{"random.txt", false},
		{"src/main/java/.goose/internal.java", false},
		{"graphify-out/model/graph.json", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := ShouldStageNewFile(tt.path); got != tt.want {
				t.Errorf("ShouldStageNewFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
