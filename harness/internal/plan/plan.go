package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/konveyor/migration-harness/internal/goose"
	"github.com/konveyor/migration-harness/internal/logging"
)

func Run(ctx context.Context, repoDir, runDir, request string, skillDir string, runner goose.Runner) (*Plan, error) {
	logging.Header("Step 2: Plan")

	logging.Info("2a. gathering project context...")
	contextText, err := GatherContext(runDir, skillDir)
	if err != nil {
		return nil, fmt.Errorf("gather context: %w", err)
	}
	ctxPath := filepath.Join(runDir, "plan-context.txt")
	os.WriteFile(ctxPath, []byte(contextText), 0644)
	logging.Ok("2a. gathered %d bytes of project context", len(contextText))

	logging.Info("2b. loading planner skill and migration references...")
	refCount := countRefs(skillDir)
	logging.Ok("2b. loaded planner skill + %d reference doc(s)", refCount)

	logging.Info("2c. building plan recipe...")
	recipe := RenderRecipe(repoDir, request, contextText, skillDir)
	recipePath := filepath.Join(runDir, "plan-recipe.yaml")
	os.WriteFile(recipePath, []byte(recipe), 0644)
	logging.Ok("2c. recipe ready (%dKB)", len(recipe)/1024)

	turns := CalcPlanTurns(runDir, skillDir)
	logging.Info("2d. running goose planner (max %d turns)...", turns)
	logging.Info("     this is the LLM step — may take 30-90s depending on model")

	_, err = runner.RunRecipe(ctx, recipePath, turns, nil)
	if err != nil {
		logging.Warn("goose planner: %v", err)
	}

	planMD := filepath.Join(repoDir, "PLAN.md")
	if _, err := os.Stat(planMD); os.IsNotExist(err) {
		return nil, fmt.Errorf("plan failed — PLAN.md not written; see %s/logs/", runDir)
	}

	copyFile(planMD, filepath.Join(runDir, "PLAN.md"))
	logging.Ok("2e. PLAN.md written")

	logging.Info("2f. parsing PLAN.md → plan.json...")
	content, err := os.ReadFile(planMD)
	if err != nil {
		return nil, fmt.Errorf("read PLAN.md: %w", err)
	}
	plan := ParsePlanMD(string(content))

	planJSON, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal plan.json: %w", err)
	}
	planJSONPath := filepath.Join(runDir, "plan.json")
	if err := os.WriteFile(planJSONPath, planJSON, 0644); err != nil {
		return nil, fmt.Errorf("write plan.json: %w", err)
	}
	logging.Ok("2f. parsed %d items from PLAN.md", len(plan.Items))

	approval, err := PromptApproval(planMD)
	if err != nil {
		return nil, fmt.Errorf("approval: %w", err)
	}
	switch approval {
	case Rejected:
		return nil, fmt.Errorf("plan rejected by user")
	case Edited:
		copyFile(planMD, filepath.Join(runDir, "PLAN.md"))
		content, err = os.ReadFile(planMD)
		if err != nil {
			logging.Warn("re-read edited PLAN.md: %v", err)
		}
		plan = ParsePlanMD(string(content))
		planJSON, err = json.MarshalIndent(plan, "", "  ")
		if err != nil {
			logging.Warn("re-marshal plan.json: %v", err)
		} else if err := os.WriteFile(planJSONPath, planJSON, 0644); err != nil {
			logging.Warn("re-write plan.json: %v", err)
		}
		logging.Ok("2g. plan edited and re-parsed")
	case Approved:
		logging.Ok("2g. plan approved")
	}

	logging.Info("2h. writing .goosehints...")
	if err := WriteHints(repoDir, plan, request); err != nil {
		return nil, fmt.Errorf("write hints: %w", err)
	}
	logging.Ok("2h. .goosehints written")

	logging.Ok("Step 2/5 complete")
	return plan, nil
}

func countRefs(skillDir string) int {
	refsDir := filepath.Join(skillDir, "references")
	entries, err := os.ReadDir(refsDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".md" {
			count++
		}
	}
	return count
}

func copyFile(src, dst string) {
	data, err := os.ReadFile(src)
	if err != nil {
		logging.Warn("copyFile: read %s: %v", src, err)
		return
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		logging.Warn("copyFile: write %s: %v", dst, err)
	}
}
