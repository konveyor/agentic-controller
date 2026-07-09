package handoff

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/konveyor/migration-harness/internal/execute"
	"github.com/konveyor/migration-harness/internal/plan"
	"github.com/konveyor/migration-harness/internal/verify"
)

type StepStatus struct {
	Status          string `json:"status"`
	DurationSeconds int    `json:"duration_seconds,omitempty"`
}

type DetectStatus struct {
	StepStatus
	Nodes       int `json:"nodes,omitempty"`
	Edges       int `json:"edges,omitempty"`
	Communities int `json:"communities,omitempty"`
}

type PlanStatus struct {
	StepStatus
	ItemsPlanned  int    `json:"items_planned,omitempty"`
	ReferenceUsed string `json:"reference_used,omitempty"`
}

type ExecuteStatus struct {
	StepStatus
	ItemsSucceeded int `json:"items_succeeded,omitempty"`
	ItemsFailed    int `json:"items_failed,omitempty"`
	ItemsSkipped   int `json:"items_skipped,omitempty"`
}

type VerifyStatus struct {
	StepStatus
	BuildOk     bool `json:"build_ok"`
	TestsPassed int  `json:"tests_passed,omitempty"`
	TestsFailed int  `json:"tests_failed,omitempty"`
}

type FixLoopStatus struct {
	StepStatus
	Iterations int `json:"iterations"`
}

type Pipeline struct {
	Detect  *DetectStatus  `json:"detect,omitempty"`
	Plan    *PlanStatus    `json:"plan,omitempty"`
	Execute *ExecuteStatus `json:"execute,omitempty"`
	Verify  *VerifyStatus  `json:"verify,omitempty"`
	FixLoop *FixLoopStatus `json:"fix_loop,omitempty"`
}

type CommitRecord struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Step    int    `json:"step"`
}

type Session struct {
	SchemaVersion    string         `json:"schema_version"`
	SessionID        string         `json:"session_id"`
	StartedAt        time.Time      `json:"started_at"`
	CompletedAt      time.Time      `json:"completed_at,omitempty"`
	Status           string         `json:"status"`
	MigrationRequest string         `json:"migration_request"`
	SourceRepo       string         `json:"source_repo"`
	TargetBranch     string         `json:"target_branch"`
	Model            string         `json:"model"`
	Provider         string         `json:"provider"`
	Pipeline         Pipeline       `json:"pipeline"`
	Commits          []CommitRecord `json:"commits"`
	Errors           []string       `json:"errors"`
}

func NewSession(sessionID, request, sourceRepo, targetBranch, model, provider string) *Session {
	return &Session{
		SchemaVersion:    "1.0",
		SessionID:        sessionID,
		StartedAt:        time.Now(),
		Status:           "in_progress",
		MigrationRequest: request,
		SourceRepo:       sourceRepo,
		TargetBranch:     targetBranch,
		Model:            model,
		Provider:         provider,
		Commits:          []CommitRecord{},
		Errors:           []string{},
	}
}

func WriteSession(repoDir string, session *Session) error {
	konveyorDir := filepath.Join(repoDir, ".konveyor")
	if err := os.MkdirAll(konveyorDir, 0755); err != nil {
		return fmt.Errorf("create .konveyor dir: %w", err)
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	return os.WriteFile(filepath.Join(konveyorDir, "session.json"), data, 0644)
}

func WriteHandoff(repoDir string, session *Session, p *plan.Plan, items []execute.ItemResult, vr *verify.VerifyResult) error {
	konveyorDir := filepath.Join(repoDir, ".konveyor")
	if err := os.MkdirAll(konveyorDir, 0755); err != nil {
		return fmt.Errorf("create .konveyor dir: %w", err)
	}

	var b strings.Builder
	b.WriteString("# Migration Handoff\n\n")

	b.WriteString("## Request\n")
	fmt.Fprintf(&b, "%s\n\n", session.MigrationRequest)

	status := session.Status
	if len(status) > 0 {
		status = strings.ToUpper(status[:1]) + status[1:]
	}
	fmt.Fprintf(&b, "## Status: %s\n\n", status)

	b.WriteString("## Summary\n")
	ok, failed, skipped := countResults(items)
	fmt.Fprintf(&b, "- %d of %d items migrated successfully\n", ok, len(items))
	if vr != nil {
		if vr.BuildOk {
			fmt.Fprintf(&b, "- Build passes, %d tests passing\n", vr.TestsPassed)
		} else {
			fmt.Fprintf(&b, "- Build failing, %d/%d tests passing\n", vr.TestsPassed, vr.TestsTotal)
		}
	}
	if failed > 0 {
		fmt.Fprintf(&b, "- %d item(s) failed\n", failed)
	}
	if skipped > 0 {
		fmt.Fprintf(&b, "- %d item(s) skipped\n", skipped)
	}
	b.WriteString("\n")

	b.WriteString("## What Was Done\n")
	for _, item := range items {
		icon := "x"
		if item.Status == "ok" {
			icon = "check"
		}
		switch icon {
		case "check":
			fmt.Fprintf(&b, "%d. [x] %s — %s\n", item.N, item.Path, item.Action)
		default:
			fmt.Fprintf(&b, "%d. [ ] %s — %s (%s)\n", item.N, item.Path, item.Action, item.Status)
		}
	}
	b.WriteString("\n")

	if failed > 0 || skipped > 0 {
		b.WriteString("## What Needs Manual Attention\n")
		for _, item := range items {
			if item.Status != "ok" {
				reason := item.ErrorLog
				if reason == "" {
					reason = item.Status
				}
				fmt.Fprintf(&b, "- %s — %s\n", item.Path, reason)
			}
		}
		b.WriteString("\n")
	}

	if vr != nil {
		b.WriteString("## Verification\n")
		if vr.BuildOk {
			b.WriteString("- Build: passing\n")
		} else {
			b.WriteString("- Build: failing\n")
		}
		fmt.Fprintf(&b, "- Tests: %d passed, %d failed\n\n", vr.TestsPassed, vr.TestsTotal-vr.TestsPassed)
	}

	return os.WriteFile(filepath.Join(konveyorDir, "handoff.md"), []byte(b.String()), 0644)
}

func countResults(items []execute.ItemResult) (ok, failed, skipped int) {
	for _, item := range items {
		switch item.Status {
		case "ok":
			ok++
		case "skipped":
			skipped++
		default:
			failed++
		}
	}
	return
}
