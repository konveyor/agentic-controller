package plan

import (
	"testing"
)

const formatA = `# Migration Plan

### Step 1: Update build configuration ✅
- File: pom.xml
- Action: MODIFY
- Convert Maven dependencies from Java EE to Quarkus

### Step 2: Migrate data model ⚠️ COMPLEX
- File: src/main/java/model/User.java
- Action: MODIFY
- Convert javax.persistence to jakarta.persistence

### Step 3: Create new config
- File: src/main/resources/application.properties
- Action: CREATE
- Add Quarkus configuration

## Verification
Build and run tests.
`

const formatB = "# Migration Plan\n\n" +
	"1. `pom.xml`\n" +
	"   - Convert Maven dependencies to Quarkus BOM\n" +
	"   - Remove Java EE dependencies\n" +
	"2. `src/main/java/model/User.java`\n" +
	"   - Convert javax to jakarta imports\n" +
	"3. `src/main/java/service/UserService.java` ⚠️\n" +
	"   - Complex EJB to CDI conversion\n" +
	"4. `src/main/resources/application.properties`\n" +
	"   - **CREATE NEW** Quarkus configuration\n\n" +
	"## Notes\nDone.\n"

const formatC = "# Migration Plan\n\n" +
	"1. `pom.xml`\n" +
	"   - Update dependencies\n" +
	"28. `src/main/java/model/Entity.java`\n" +
	"   - Migrate annotations\n" +
	"29. **DELETE:** `src/main/webapp/weblogic-application.xml`\n" +
	"   - Remove WebLogic-specific descriptor\n"

func TestParseFormatA(t *testing.T) {
	plan := ParsePlanMD(formatA)

	if len(plan.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(plan.Items))
	}

	item1 := plan.Items[0]
	if item1.N != 1 {
		t.Errorf("item1.N = %d, want 1", item1.N)
	}
	if item1.Path != "pom.xml" {
		t.Errorf("item1.Path = %q, want pom.xml", item1.Path)
	}
	if item1.Action != "migrate" {
		t.Errorf("item1.Action = %q, want migrate", item1.Action)
	}
	if item1.Risk != "low" {
		t.Errorf("item1.Risk = %q, want low", item1.Risk)
	}
	if item1.Layer != "build" {
		t.Errorf("item1.Layer = %q, want build", item1.Layer)
	}

	item2 := plan.Items[1]
	if item2.Risk != "high" {
		t.Errorf("item2.Risk = %q, want high", item2.Risk)
	}
	if item2.Layer != "model" {
		t.Errorf("item2.Layer = %q, want model", item2.Layer)
	}

	item3 := plan.Items[2]
	if item3.Action != "create" {
		t.Errorf("item3.Action = %q, want create", item3.Action)
	}
	if item3.Layer != "config" {
		t.Errorf("item3.Layer = %q, want config", item3.Layer)
	}
}

func TestParseFormatB(t *testing.T) {
	plan := ParsePlanMD(formatB)

	if len(plan.Items) != 4 {
		t.Fatalf("items = %d, want 4", len(plan.Items))
	}

	if plan.Items[0].Path != "pom.xml" {
		t.Errorf("item0.Path = %q, want pom.xml", plan.Items[0].Path)
	}
	if plan.Items[0].Layer != "build" {
		t.Errorf("item0.Layer = %q, want build", plan.Items[0].Layer)
	}

	if plan.Items[2].Risk != "high" {
		t.Errorf("item2.Risk = %q, want high (⚠️)", plan.Items[2].Risk)
	}
	if plan.Items[2].Layer != "service" {
		t.Errorf("item2.Layer = %q, want service", plan.Items[2].Layer)
	}

	if plan.Items[3].Action != "create" {
		t.Errorf("item3.Action = %q, want create", plan.Items[3].Action)
	}
}

func TestParseFormatC_Delete(t *testing.T) {
	plan := ParsePlanMD(formatC)

	if len(plan.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(plan.Items))
	}

	del := plan.Items[2]
	if del.N != 29 {
		t.Errorf("del.N = %d, want 29", del.N)
	}
	if del.Action != "delete" {
		t.Errorf("del.Action = %q, want delete", del.Action)
	}
	if del.Path != "src/main/webapp/weblogic-application.xml" {
		t.Errorf("del.Path = %q", del.Path)
	}
}

func TestMigrationType(t *testing.T) {
	tests := []struct {
		content string
		want    string
	}{
		{"Migrate from Java EE to Quarkus", "java-ee-to-quarkus"},
		{"Convert WebLogic app to Jakarta EE", "java-ee-to-quarkus"},
		{"Upgrade from Python 2 to Python 3", "python2-to-python3"},
		{"Convert React class components to hooks", "react-class-to-hooks"},
		{"Upgrade .NET Framework to .NET Core", "dotnet-upgrade"},
		{"Migrate Spring Boot 2 to Spring Boot 3", "spring-boot-upgrade"},
		{"Something entirely custom", "custom"},
	}

	for _, tt := range tests {
		got := detectMigrationType(tt.content)
		if got != tt.want {
			t.Errorf("detectMigrationType(%q) = %q, want %q", tt.content[:30], got, tt.want)
		}
	}
}

func TestAssignLayer(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"pom.xml", "build"},
		{"package.json", "build"},
		{"src/main/resources/application.properties", "config"},
		{"src/main/java/model/User.java", "model"},
		{"src/main/java/service/UserService.java", "service"},
		{"src/main/java/rest/UserEndpoint.java", "api"},
		{"src/main/java/controller/HomeController.java", "api"},
		{"src/main/java/utils/StringHelper.java", "util"},
		{"src/main/java/repository/UserRepo.java", "persistence"},
		{"src/main/webapp/views/index.jsp", "view"},
		{"src/main/java/SomeRandom.java", "unknown"},
	}

	for _, tt := range tests {
		got := assignLayer(tt.path)
		if got != tt.want {
			t.Errorf("assignLayer(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestCollectNotes(t *testing.T) {
	lines := []string{
		"1. `pom.xml`",
		"   - Convert Maven dependencies",
		"   - Remove old deps",
		"2. `next.java`",
	}
	notes := collectNotes(lines, 1, "fallback")
	if notes == "fallback" {
		t.Error("expected notes from bullet points, got fallback")
	}
}
