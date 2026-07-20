package execute

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/konveyor/migration-harness/internal/git"
	"github.com/konveyor/migration-harness/internal/goose"
	"github.com/konveyor/migration-harness/internal/logging"
	"github.com/konveyor/migration-harness/internal/plan"
)

type ItemResult struct {
	N            int      `json:"n"`
	Path         string   `json:"path"`
	Action       string   `json:"action"`
	Status       string   `json:"status"`
	Lesson       string   `json:"lesson,omitempty"`
	ErrorLog     string   `json:"error_log,omitempty"`
	FilesTouched []string `json:"files_touched,omitempty"`
}

type Summary struct {
	Ok      int `json:"ok"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

type CommitRecord struct {
	SHA     string
	Message string
	Step    int
}

type PushFn func() error

func Run(ctx context.Context, repoDir, runDir, recipesDir string, p *plan.Plan, runner goose.Runner, repo *gogit.Repository, pushFn PushFn) ([]ItemResult, *Summary, []CommitRecord, error) {
	logging.Header("Step 3: Execute")

	total := len(p.Items)
	logging.Info("executing %d items (migration: %s)", total, p.MigrationType)

	execLog := filepath.Join(runDir, "execution-log.md")
	initExecutionLog(execLog, p.MigrationType)

	var results []ItemResult
	var commits []CommitRecord
	summary := &Summary{}

	recipeFile := filepath.Join(recipesDir, "execute.yaml")

	for i, item := range p.Items {
		current := i + 1
		outPath := filepath.Join(runDir, fmt.Sprintf("item-%03d.json", item.N))

		if existing := loadExistingResult(outPath); existing != nil && existing.Status == "ok" {
			logging.Ok("(%d/%d) #%d already done, skipping", current, total, item.N)
			summary.Ok++
			results = append(results, *existing)
			continue
		}

		logging.Info("(%d/%d) #%d [%s] %s", current, total, item.N, item.Action, item.Path)
		logging.Info("     invoking goose — this may take 15-60s per file...")

		result := executeItem(ctx, runner, recipeFile, repoDir, runDir, item, p.MigrationType)
		writeItemResult(outPath, &result)
		appendExecutionLog(execLog, &result)

		switch result.Status {
		case "ok":
			summary.Ok++
			plan.UpdateHintsChecklist(repoDir, item.N)
			logging.Ok("(%d/%d) #%d done", current, total, item.N)
			if result.Lesson != "" && len(result.Lesson) > 80 {
				logging.Info("     lesson: %s...", result.Lesson[:80])
			} else if result.Lesson != "" {
				logging.Info("     lesson: %s", result.Lesson)
			}
		case "skipped":
			summary.Skipped++
			logging.Warn("(%d/%d) #%d skipped", current, total, item.N)
		default:
			summary.Failed++
			logging.Err("(%d/%d) #%d status=%s", current, total, item.N, result.Status)
		}

		if repo != nil {
			commitMsg := fmt.Sprintf("migrate: #%d %s", item.N, itemLabel(item))
			hash, err := git.CommitAll(repo, commitMsg)
			if err != nil {
				logging.Warn("git commit after item #%d: %v", item.N, err)
			} else if hash != plumbing.ZeroHash {
				commits = append(commits, CommitRecord{SHA: hash.String(), Message: commitMsg, Step: item.N})
				if pushFn != nil {
					if err := pushFn(); err != nil {
						logging.Warn("git push after item #%d: %v", item.N, err)
					}
				}
			}
		}

		results = append(results, result)
	}

	summaryPath := filepath.Join(runDir, "execute-summary.json")
	summaryData, _ := json.MarshalIndent(summary, "", "  ")
	os.WriteFile(summaryPath, summaryData, 0644)

	copyFile(execLog, filepath.Join(repoDir, "execution-log.md"))

	logging.Ok("Step 3/5 complete: %d ok, %d failed, %d skipped", summary.Ok, summary.Failed, summary.Skipped)
	return results, summary, commits, nil
}

func executeItem(ctx context.Context, runner goose.Runner, recipeFile, repoDir, runDir string, item plan.PlanItem, migrationType string) ItemResult {
	result := ItemResult{
		N:      item.N,
		Path:   item.Path,
		Action: item.Action,
		Status: "failed",
	}

	output, err := runner.RunRecipe(ctx, recipeFile, 10, map[string]string{
		"repo":           repoDir,
		"plan_md_path":   filepath.Join(runDir, "PLAN.md"),
		"migration_type": migrationType,
		"item_n":         fmt.Sprintf("%d", item.N),
		"item_path":      item.Path,
		"item_action":    item.Action,
	})
	if err != nil {
		result.ErrorLog = err.Error()
		return result
	}

	var parsed struct {
		Status       string   `json:"status"`
		FilesTouched []string `json:"files_touched"`
		Lesson       string   `json:"lesson"`
		ErrorLog     string   `json:"error_log"`
	}
	if err := json.Unmarshal(output, &parsed); err != nil {
		result.ErrorLog = fmt.Sprintf("parse goose output: %v", err)
		return result
	}

	result.Status = parsed.Status
	result.FilesTouched = parsed.FilesTouched
	result.Lesson = parsed.Lesson
	result.ErrorLog = parsed.ErrorLog

	if result.Status == "" {
		result.Status = "failed"
	}

	return result
}

func loadExistingResult(path string) *ItemResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var r ItemResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil
	}
	return &r
}

func writeItemResult(path string, r *ItemResult) {
	data, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile(path, data, 0644)
}

func initExecutionLog(path, migrationType string) {
	var b strings.Builder
	b.WriteString("# Execution Log\n\n")
	fmt.Fprintf(&b, "**Migration:** %s\n", migrationType)
	fmt.Fprintf(&b, "**Started:** %s\n\n", time.Now().Format(time.RFC3339))
	b.WriteString("---\n\n")
	os.WriteFile(path, []byte(b.String()), 0644)
}

func appendExecutionLog(path string, r *ItemResult) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "## Step #%d: %s - %s\n\n", r.N, r.Action, r.Path)
	fmt.Fprintf(f, "**Status:** %s\n", r.Status)
	if len(r.FilesTouched) > 0 {
		fmt.Fprintf(f, "**Files touched:** %s\n", strings.Join(r.FilesTouched, ", "))
	}
	if r.Lesson != "" {
		fmt.Fprintf(f, "\n**Lesson learned:**\n%s\n", r.Lesson)
	}
	if r.ErrorLog != "" {
		fmt.Fprintf(f, "\n**Errors:**\n```\n%s\n```\n", r.ErrorLog)
	}
	fmt.Fprintf(f, "\n---\n\n")
}

func itemLabel(item plan.PlanItem) string {
	if item.Path != "" {
		return item.Path
	}
	if item.Notes != "" {
		if len(item.Notes) > 80 {
			return item.Notes[:80]
		}
		return item.Notes
	}
	return item.Action
}

func copyFile(src, dst string) {
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	os.WriteFile(dst, data, 0644)
}
