package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/konveyor/migration-harness/internal/goose"
	"github.com/konveyor/migration-harness/internal/logging"
)

type VerifyError struct {
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

type VerifyResult struct {
	BuildOk      bool          `json:"build_ok"`
	TestsPassed  int           `json:"tests_passed"`
	TestsTotal   int           `json:"tests_total"`
	Errors       []VerifyError `json:"errors"`
	FixAttempts  int           `json:"fix_attempts"`
	FixesApplied string        `json:"fixes_applied,omitempty"`
	Summary      string        `json:"summary,omitempty"`
}

func Run(ctx context.Context, repoDir, runDir, recipesDir, migrationType string, runner goose.Runner) (*VerifyResult, error) {
	logging.Header("Step 4: Verify")
	logging.Info("verifying build + tests (migration: %s)", migrationType)

	outPath := filepath.Join(runDir, "verify.json")

	result, err := runVerify(ctx, runner, recipesDir, repoDir, runDir, migrationType)
	if err != nil {
		return nil, err
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	os.WriteFile(outPath, data, 0644)

	if result.FixAttempts > 0 {
		logging.Info("fix attempts: %d", result.FixAttempts)
	}

	if result.BuildOk {
		logging.Ok("build: OK | tests: %d/%d passed | errors: %d",
			result.TestsPassed, result.TestsTotal, len(result.Errors))
	} else {
		logging.Err("build: FAILED | tests: %d/%d passed | errors: %d",
			result.TestsPassed, result.TestsTotal, len(result.Errors))
	}

	if len(result.Errors) > 0 {
		logging.Info("first errors:")
		limit := 3
		if len(result.Errors) < limit {
			limit = len(result.Errors)
		}
		for _, e := range result.Errors[:limit] {
			logging.Info("  %s:%d — %s", e.File, e.Line, e.Message)
		}
	}

	writeVerifyReport(filepath.Join(runDir, "verification-report.md"), result, migrationType)
	copyFile(filepath.Join(runDir, "verification-report.md"), filepath.Join(repoDir, "verification-report.md"))

	logging.Ok("Step 4/5 complete")
	return result, nil
}

func runVerify(ctx context.Context, runner goose.Runner, recipesDir, repoDir, runDir, migrationType string) (*VerifyResult, error) {
	recipeFile := filepath.Join(recipesDir, "verify.yaml")
	output, err := runner.RunRecipe(ctx, recipeFile, 50, map[string]string{
		"repo":               repoDir,
		"plan_md_path":       filepath.Join(runDir, "PLAN.md"),
		"execution_log_path": filepath.Join(repoDir, "execution-log.md"),
		"migration_type":     migrationType,
	})
	if err != nil {
		return &VerifyResult{
			BuildOk: false,
			Errors:  []VerifyError{{Message: err.Error()}},
			Summary: fmt.Sprintf("goose verify failed: %v", err),
		}, nil
	}

	var result VerifyResult
	if err := json.Unmarshal(output, &result); err != nil {
		return &VerifyResult{
			BuildOk: false,
			Errors:  []VerifyError{{Message: fmt.Sprintf("parse goose output: %v", err)}},
		}, nil
	}

	return &result, nil
}

func writeVerifyReport(path string, r *VerifyResult, migrationType string) {
	var b strings.Builder
	b.WriteString("# Verification Report\n\n")
	fmt.Fprintf(&b, "**Migration:** %s\n", migrationType)
	fmt.Fprintf(&b, "**Timestamp:** %s\n\n", time.Now().Format(time.RFC3339))

	b.WriteString("## Build Status\n\n")
	if r.BuildOk {
		b.WriteString("- Compilation: **SUCCESS**\n")
	} else {
		b.WriteString("- Compilation: **FAILED**\n")
	}
	fmt.Fprintf(&b, "- Tests: %d/%d passed\n\n", r.TestsPassed, r.TestsTotal)

	if r.FixAttempts > 0 {
		b.WriteString("## Auto-Fix Attempts\n\n")
		fmt.Fprintf(&b, "- Fix iterations: %d\n", r.FixAttempts)
		if r.FixesApplied != "" {
			fmt.Fprintf(&b, "- Fixes applied: %s\n", r.FixesApplied)
		}
		b.WriteString("\n")
	}

	if len(r.Errors) > 0 {
		fmt.Fprintf(&b, "## Remaining Errors (%d total)\n\n", len(r.Errors))
		for _, e := range r.Errors {
			fmt.Fprintf(&b, "### %s:%d\n\n```\n%s\n```\n\n", e.File, e.Line, e.Message)
		}
	}

	b.WriteString("## Summary\n\n")
	if r.Summary != "" {
		b.WriteString(r.Summary)
	} else {
		b.WriteString("No summary provided")
	}
	b.WriteString("\n")

	os.WriteFile(path, []byte(b.String()), 0644)
}

func copyFile(src, dst string) {
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	os.WriteFile(dst, data, 0644)
}
