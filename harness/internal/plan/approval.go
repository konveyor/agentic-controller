package plan

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/konveyor/migration-harness/internal/logging"
)

type ApprovalResult int

const (
	Approved ApprovalResult = iota
	Edited
	Rejected
)

var AutoApprove bool

func PromptApproval(planMDPath string) (ApprovalResult, error) {
	content, err := os.ReadFile(planMDPath)
	if err != nil {
		return Rejected, fmt.Errorf("read PLAN.md: %w", err)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "══════════════════ PLAN ══════════════════")
	fmt.Fprint(os.Stderr, string(content))
	fmt.Fprintln(os.Stderr, "══════════════════════════════════════════")
	fmt.Fprintln(os.Stderr)

	if AutoApprove {
		logging.Ok("plan auto-approved")
		return Approved, nil
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Fprint(os.Stderr, "Approve and execute? [y/edit/N]: ")
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))

	switch answer {
	case "y", "yes":
		logging.Ok("plan approved")
		return Approved, nil
	case "edit", "e":
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		parts := strings.Fields(editor)
		if len(parts) == 0 {
			parts = []string{"vi"}
		}
		editorArgs := append(parts[1:], planMDPath)
		cmd := exec.Command(parts[0], editorArgs...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return Rejected, fmt.Errorf("editor: %w", err)
		}
		logging.Ok("plan edited")
		return Edited, nil
	default:
		logging.Info("aborted by user")
		return Rejected, nil
	}
}
