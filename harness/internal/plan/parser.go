package plan

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	reStepHeader = regexp.MustCompile(`^### Step (\d+):\s*(.*)`)
	reFileField  = regexp.MustCompile(`^- File:\s*(.+)`)
	reActionField = regexp.MustCompile(`^- Action:\s*(\w+)`)

	reItemLine = regexp.MustCompile("^(\\d+)\\.\\s+(?:\\*\\*(?:DELETE|REMOVE)[:\\*]*\\s*)?`([^`]+)`(.*)")

	reSectionHeader = regexp.MustCompile(`^##\s`)
	reNextItem      = regexp.MustCompile(`^\d+\.\s+`)

	reMigrationType = map[string]*regexp.Regexp{
		"java-ee-to-quarkus":    regexp.MustCompile(`(?i)quarkus|java.?ee|jakarta|weblogic`),
		"python2-to-python3":    regexp.MustCompile(`(?i)python.?[23]|py2|py3`),
		"react-class-to-hooks":  regexp.MustCompile(`(?i)react|hooks|class.?component`),
		"dotnet-upgrade":        regexp.MustCompile(`(?i)\.net|asp\.net|csharp|c#|\.NET\s*(Core|Framework)`),
		"spring-boot-upgrade":   regexp.MustCompile(`(?i)spring.?boot|spring.?framework`),
	}

	reSource = regexp.MustCompile(`(?i)(?:from|source)[:\s]+(.+?)(?:\n|→|->)`)
	reTarget = regexp.MustCompile(`(?i)(?:to|target|→|->)\s*(.+?)(?:\n|$)`)
)

func ParsePlanMD(content string) *Plan {
	items := tryFormatA(content)
	if len(items) == 0 {
		items = tryFormatBC(content)
	}

	for i := range items {
		items[i].Layer = assignLayer(items[i].Path)
	}

	mt := detectMigrationType(content)
	src := extractMatch(reSource, content)
	tgt := extractMatch(reTarget, content)

	return &Plan{
		MigrationType: mt,
		SourceStack:   src,
		TargetStack:   tgt,
		Items:         items,
	}
}

func tryFormatA(content string) []PlanItem {
	lines := strings.Split(content, "\n")
	var items []PlanItem
	var current *PlanItem
	var bodyLines []string

	flushCurrent := func() {
		if current != nil {
			for _, bl := range bodyLines {
				bl = strings.TrimSpace(bl)
				if m := reFileField.FindStringSubmatch(bl); m != nil {
					current.Path = strings.TrimSpace(m[1])
				}
				if m := reActionField.FindStringSubmatch(bl); m != nil {
					current.Action = normalizeAction(strings.TrimSpace(m[1]))
				}
			}
			if current.Action == "" {
				current.Action = "migrate"
			}
			items = append(items, *current)
			current = nil
			bodyLines = nil
		}
	}

	for _, line := range lines {
		if m := reStepHeader.FindStringSubmatch(line); m != nil {
			flushCurrent()
			n, _ := strconv.Atoi(m[1])
			title := strings.TrimSpace(m[2])
			title = strings.TrimRight(title, " ✅")
			risk := "low"
			if strings.Contains(title, "⚠️") || strings.Contains(strings.ToUpper(title), "COMPLEX") {
				risk = "high"
			}
			current = &PlanItem{N: n, Notes: title, Risk: risk}
			bodyLines = nil
			continue
		}
		if current != nil {
			if reSectionHeader.MatchString(line) || strings.HasPrefix(line, "### Step") {
				flushCurrent()
			} else {
				bodyLines = append(bodyLines, line)
			}
		}
	}
	flushCurrent()

	return items
}

func tryFormatBC(content string) []PlanItem {
	lines := strings.Split(content, "\n")
	var items []PlanItem

	for i, line := range lines {
		m := reItemLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		path := strings.TrimSpace(m[2])
		rest := strings.TrimSpace(m[3])
		fullLine := m[0]

		notes := collectNotes(lines, i+1, path)
		actionContext := fullLine + " " + notes
		action := detectAction(actionContext)
		risk := "low"
		if strings.Contains(fullLine, "⚠️") || strings.Contains(strings.ToUpper(fullLine), "COMPLEX") {
			risk = "high"
		}

		items = append(items, PlanItem{
			N:      n,
			Path:   path,
			Action: action,
			Risk:   risk,
			Notes:  notes,
		})
		_ = rest
	}

	return items
}

func collectNotes(lines []string, startIdx int, fallback string) string {
	var noteLines []string
	for j := startIdx; j < len(lines) && len(noteLines) < 3; j++ {
		l := strings.TrimSpace(lines[j])
		if l == "" {
			continue
		}
		if reNextItem.MatchString(l) || reSectionHeader.MatchString(l) {
			break
		}
		if strings.HasPrefix(l, "-") || strings.HasPrefix(l, "*") {
			cleaned := strings.TrimLeft(l, "- *")
			noteLines = append(noteLines, strings.TrimSpace(cleaned))
		}
	}
	if len(noteLines) == 0 {
		return fallback
	}
	return strings.Join(noteLines, "; ")
}

func detectAction(line string) string {
	upper := strings.ToUpper(line)
	if strings.Contains(upper, "DELETE") || strings.Contains(upper, "REMOVE") {
		return "delete"
	}
	if strings.Contains(upper, "CREATE") {
		return "create"
	}
	return "migrate"
}

func normalizeAction(action string) string {
	switch strings.ToLower(action) {
	case "modify":
		return "migrate"
	case "create":
		return "create"
	case "delete":
		return "delete"
	default:
		return strings.ToLower(action)
	}
}

func assignLayer(path string) string {
	p := strings.ToLower(path)

	buildFiles := []string{"pom.xml", "package.json", "build.gradle", ".csproj", ".sln",
		"cargo.toml", "gemfile", "go.mod", "requirements.txt", "pyproject.toml", "setup.py"}
	for _, bf := range buildFiles {
		if strings.Contains(p, bf) {
			return "build"
		}
	}

	configFiles := []string{"application.properties", "application.yml", "appsettings",
		"web.config", ".env", "config.yaml", "config.json",
		"persistence.xml", "web.xml", "beans.xml",
		"global.asax", "startup.cs", "program.cs"}
	for _, cf := range configFiles {
		if strings.Contains(p, cf) {
			return "config"
		}
	}

	layerDirs := map[string][]string{
		"model":       {"/model/", "/models/", "/domain/", "/entity/", "/entities/"},
		"service":     {"/service/", "/services/"},
		"api":         {"/rest/", "/controller/", "/controllers/", "/api/", "/endpoint/", "/endpoints/", "/handler/", "/handlers/"},
		"util":        {"/utils/", "/util/", "/helper/", "/helpers/", "/common/"},
		"persistence": {"/persistence/", "/repository/", "/repositories/", "/dao/", "/data/"},
		"view":        {"/views/", "/pages/"},
	}
	for layer, dirs := range layerDirs {
		for _, d := range dirs {
			if strings.Contains(p, d) {
				return layer
			}
		}
	}

	if strings.Contains(p, "weblogic/") {
		return "cleanup"
	}

	return "unknown"
}

func detectMigrationType(content string) string {
	order := []string{"java-ee-to-quarkus", "python2-to-python3", "react-class-to-hooks", "dotnet-upgrade", "spring-boot-upgrade"}
	for _, mt := range order {
		if reMigrationType[mt].MatchString(content) {
			return mt
		}
	}
	return "custom"
}

func extractMatch(re *regexp.Regexp, content string) string {
	m := re.FindStringSubmatch(content)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}
