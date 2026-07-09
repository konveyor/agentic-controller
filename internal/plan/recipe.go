package plan

import (
	"os"
	"path/filepath"
	"strings"
)

func RenderRecipe(repoDir, request, context, skillDir string) string {
	var plannerSkill string
	skillPath := filepath.Join(skillDir, "skills", "migration-plan", "SKILL.md")
	if data, err := os.ReadFile(skillPath); err == nil {
		plannerSkill = indentBlock(string(data), 4)
	}

	indentedCtx := indentBlock(context, 4)

	var b strings.Builder
	b.WriteString(`version: "1.0.0"
title: "Migration plan"
description: "Generate PLAN.md using the planner skill."

settings:
  temperature: 1

extensions:
  - type: builtin
    name: developer
    timeout: 600
    bundled: true

instructions: |
  You are the planner sub-skill. Your ONLY job is to produce PLAN.md in the
  repo root. Do NOT modify any source files.

  === PLANNER SKILL ===
`)
	b.WriteString(plannerSkill)
	b.WriteString("\n  === END PLANNER SKILL ===\n\n")
	b.WriteString("  === PRE-GATHERED CONTEXT ===\n")
	b.WriteString("  The following has been pre-collected: detect.json, source file tree,\n")
	b.WriteString("  and build manifests. Do NOT re-run these discovery commands.\n")
	b.WriteString(indentedCtx)
	b.WriteString("\n  === END PRE-GATHERED CONTEXT ===\n\n")
	b.WriteString(`  YOUR JOB (follow this order strictly):

  PHASE 1 — Quick scan (max 3 reads):
  1. Read the pre-gathered context above (detect.json, file tree) — already done.
  2. Read the build manifest (pom.xml, package.json, .csproj, etc.) — 1 read.
  3. Check AVAILABLE REFERENCES list. If one matches, read it — 1 read.
     You MUST report which reference file you read in your final response.

  PHASE 2 — Write a DRAFT PLAN.md:
  4. Based on what you know so far, write PLAN.md to `)
	b.WriteString(repoDir)
	b.WriteString(`/PLAN.md NOW.
     For files you haven't read, make your best guess from the file name
     and mark uncertain steps with ⚠️.

  PHASE 3 — Refine (max 5 reads):
  5. Read ONLY the source files where your guess might be wrong — complex
     patterns like MDBs, JNDI lookups, lifecycle listeners, etc.
  6. Update PLAN.md with corrections based on what you read.
     If no corrections needed, skip this phase.

  RULES:
  - Write PLAN.md BEFORE reading source files. Draft first, refine after.
  - Max 8 file reads total across all phases.
  - If uncertain about a file, mark it ⚠️ and move on. Do NOT read every file.
  - In your response, report which reference file you read (or "none" if no match).

prompt: |
  Repo:              `)
	b.WriteString(repoDir)
	b.WriteString("\n  Migration request: ")
	b.WriteString(request)
	b.WriteString(`

  Follow the planner skill phases. All discovery data and references are
  already in your instructions. Read only complex source files you need,
  then write PLAN.md.

response:
  json_schema:
    type: object
    required: [plan_written, step_count, reference_used]
    properties:
      plan_written: { type: boolean }
      step_count:   { type: integer }
      complex_count: { type: integer }
      reference_used: { type: string, description: "Name of reference file read, or 'none'" }
      summary: { type: string }
`)
	return b.String()
}

func indentBlock(s string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}
