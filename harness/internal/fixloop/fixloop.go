package fixloop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/konveyor/migration-harness/internal/git"
	"github.com/konveyor/migration-harness/internal/goose"
	"github.com/konveyor/migration-harness/internal/logging"
	"github.com/konveyor/migration-harness/internal/verify"
)

type FixResult struct {
	Fixed       bool   `json:"fixed"`
	FileChanged string `json:"file_changed,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

type FixLoopReport struct {
	MigrationType string `json:"migration_type"`
	Iterations    int    `json:"iterations"`
	Status        string `json:"status"`
}

type CommitRecord struct {
	SHA     string
	Message string
	Iter    int
}

type PushFn func() error

func Run(ctx context.Context, repoDir, runDir, recipesDir, migrationType string, maxIterations int, runner goose.Runner, repo *gogit.Repository, pushFn PushFn) (*FixLoopReport, []CommitRecord, error) {
	logging.Header("Step 5: Fix Loop")

	verifyPath := filepath.Join(runDir, "verify.json")
	if _, err := os.Stat(verifyPath); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("no verification output found — run step 4 (verify) first")
	}

	vr := loadVerifyResult(verifyPath)
	if vr != nil && vr.BuildOk {
		logging.Ok("build status: SUCCESS (from verification report)")
		logging.Ok("fix loop not needed — skipping")
		report := &FixLoopReport{
			MigrationType: migrationType,
			Iterations:    0,
			Status:        "skipped",
		}
		logging.Ok("Step 5/5 complete (skipped - build already successful)")
		return report, nil, nil
	}

	logging.Warn("build status: FAILED (from verification report)")
	logging.Info("starting fix loop (max %d iterations)...", maxIterations)

	var commits []CommitRecord

	for iter := 1; iter <= maxIterations; iter++ {
		verifyIterPath := filepath.Join(runDir, fmt.Sprintf("verify-fix-%d.json", iter))

		logging.Info("iteration %d/%d — re-verifying...", iter, maxIterations)
		iterResult, err := verify.Run(ctx, repoDir, runDir, recipesDir, migrationType, runner)
		if err == nil && iterResult.BuildOk && len(iterResult.Errors) == 0 {
			logging.Ok("iteration %d/%d — build clean, no errors!", iter, maxIterations)
			report := &FixLoopReport{
				MigrationType: migrationType,
				Iterations:    iter,
				Status:        "success",
			}
			writeFixReport(runDir, repoDir, report, migrationType)
			logging.Ok("Step 5/5 complete")
			return report, commits, nil
		}

		iterData, _ := json.MarshalIndent(iterResult, "", "  ")
		os.WriteFile(verifyIterPath, iterData, 0644)

		var currentVerify *verify.VerifyResult
		if iter == 1 {
			currentVerify = vr
		} else {
			currentVerify = iterResult
		}

		if currentVerify == nil || len(currentVerify.Errors) == 0 {
			logging.Warn("iteration %d/%d — build not OK but no errors reported; cannot auto-fix", iter, maxIterations)
			break
		}

		firstErr := currentVerify.Errors[0]
		logging.Info("iteration %d/%d — fixing: %s", iter, maxIterations, firstErr.File)
		logging.Info("     error: %s", truncate(firstErr.Message, 100))

		fixResult := attemptFix(ctx, runner, recipesDir, repoDir, runDir, migrationType, firstErr, iter)
		if !fixResult.Fixed {
			logging.Err("iteration %d/%d — fix did not succeed (%s)", iter, maxIterations, firstErr.File)
			break
		}

		logging.Ok("iteration %d/%d — fixed %s", iter, maxIterations, fixResult.FileChanged)

		if repo != nil {
			commitMsg := fmt.Sprintf("fix: %s", fixResult.FileChanged)
			hash, err := git.CommitAll(repo, commitMsg)
			if err != nil {
				logging.Warn("git commit after fix #%d: %v", iter, err)
			} else if hash != plumbing.ZeroHash {
				commits = append(commits, CommitRecord{SHA: hash.String(), Message: commitMsg, Iter: iter})
				if pushFn != nil {
					if err := pushFn(); err != nil {
						logging.Warn("git push after fix #%d: %v", iter, err)
					}
				}
			}
		}
	}

	report := &FixLoopReport{
		MigrationType: migrationType,
		Iterations:    maxIterations,
		Status:        "manual_intervention_needed",
	}
	writeFixReport(runDir, repoDir, report, migrationType)
	logging.Warn("hit max iterations (%d) — manual intervention needed", maxIterations)
	return report, commits, nil
}

func attemptFix(ctx context.Context, runner goose.Runner, recipesDir, repoDir, runDir, migrationType string, verifyErr verify.VerifyError, iter int) FixResult {
	fixPath := filepath.Join(runDir, fmt.Sprintf("fix-%d.json", iter))
	recipeFile := filepath.Join(recipesDir, "fix.yaml")

	output, err := runner.RunRecipe(ctx, recipeFile, 8, map[string]string{
		"repo":                     repoDir,
		"verification_report_path": filepath.Join(runDir, "verification-report.md"),
		"migration_type":           migrationType,
		"error_file":               verifyErr.File,
		"error_message":            verifyErr.Message,
	})

	var result FixResult
	if err != nil {
		result = FixResult{Fixed: false, Summary: err.Error()}
	} else if err := json.Unmarshal(output, &result); err != nil {
		result = FixResult{Fixed: false, Summary: fmt.Sprintf("parse goose output: %v", err)}
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	os.WriteFile(fixPath, data, 0644)

	return result
}

func loadVerifyResult(path string) *verify.VerifyResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var r verify.VerifyResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil
	}
	return &r
}

func writeFixReport(runDir, repoDir string, report *FixLoopReport, migrationType string) {
	var b strings.Builder
	b.WriteString("# Fix Loop Report\n\n")
	fmt.Fprintf(&b, "**Migration:** %s\n", migrationType)
	fmt.Fprintf(&b, "**Iterations:** %d\n", report.Iterations)
	fmt.Fprintf(&b, "**Status:** %s\n\n", report.Status)

	if report.Status == "success" {
		b.WriteString("## Result\n\nAll compilation errors resolved. Build is now successful.\n")
	} else {
		b.WriteString("## Next Steps\n\nManual intervention is required. Review the remaining errors in verification-report.md\n")
	}

	path := filepath.Join(runDir, "fix-loop-report.md")
	os.WriteFile(path, []byte(b.String()), 0644)
	copyFile(path, filepath.Join(repoDir, "fix-loop-report.md"))
}

func copyFile(src, dst string) {
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	os.WriteFile(dst, data, 0644)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
