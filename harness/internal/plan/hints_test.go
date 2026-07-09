package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteHints(t *testing.T) {
	dir := t.TempDir()
	plan := &Plan{
		MigrationType: "java-ee-to-quarkus",
		SourceStack:   "Java EE 7",
		TargetStack:   "Quarkus 3",
		Items: []PlanItem{
			{N: 1, Path: "pom.xml", Action: "migrate", Notes: "Update deps"},
			{N: 2, Path: "src/main/java/Foo.java", Action: "migrate", Notes: "javax to jakarta"},
		},
	}

	err := WriteHints(dir, plan, "Migrate to Quarkus")
	if err != nil {
		t.Fatalf("WriteHints: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".goosehints"))
	if err != nil {
		t.Fatalf("read .goosehints: %v", err)
	}
	content := string(data)

	checks := []string{
		"TOKEN DISCIPLINE",
		"Source: Java EE 7",
		"Target: Quarkus 3",
		"Type:   java-ee-to-quarkus",
		"1. [migrate] pom.xml",
		"- [ ] 1. pom.xml",
		"- [ ] 2. src/main/java/Foo.java",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("missing %q in .goosehints", check)
		}
	}
}

func TestUpdateHintsChecklist(t *testing.T) {
	dir := t.TempDir()
	hintsPath := filepath.Join(dir, ".goosehints")
	initial := "## Checklist\n- [ ] 1. pom.xml\n- [ ] 2. Foo.java\n"
	os.WriteFile(hintsPath, []byte(initial), 0644)

	err := UpdateHintsChecklist(dir, 1)
	if err != nil {
		t.Fatalf("UpdateHintsChecklist: %v", err)
	}

	data, _ := os.ReadFile(hintsPath)
	content := string(data)
	if !strings.Contains(content, "- [x] 1.") {
		t.Error("expected item 1 to be checked")
	}
	if !strings.Contains(content, "- [ ] 2.") {
		t.Error("expected item 2 to remain unchecked")
	}
}
