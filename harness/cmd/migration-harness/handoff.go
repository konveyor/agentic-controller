package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	gogit "github.com/go-git/go-git/v5"

	"github.com/konveyor/migration-harness/internal/git"
	"github.com/konveyor/migration-harness/internal/logging"
)

// handoffPath is the file stages hand work to each other through (ADR
// 0001). Each stage appends its own `## <Stage>` section, and the catalog
// skills open that section with a `- Status:` line — the stage's own
// verdict on its work (catalog/skills/execute, verify).
const handoffPath = ".konveyor/handoff.md"

// handoffStatusFailed is the status value that says the stage did not do
// its work. It is the one value the harness acts on: the ACP result alone
// cannot tell, because the agent writes its verdict and then ends the turn
// normally, so without this read a stage that refused its task exited 0
// and the workflow carried on from it (#241).
const handoffStatusFailed = "failed"

// handoffStatusesOK are the values the catalog skills write for a stage
// that did its work. Any status not here and not handoffStatusFailed is
// one the harness does not recognise: logged and recorded, never acted on.
var handoffStatusesOK = map[string]bool{
	"completed": true,
	"passed":    true,
	"succeeded": true,
	"done":      true,
}

// handoffDetailLen bounds the detail carried into the termination blob and
// the viewer notice: one line of why, not the whole section.
const handoffDetailLen = 300

// handoffVerdict is what a stage's handoff section says about the stage.
type handoffVerdict struct {
	// Status is the first word of the section's `- Status:` line,
	// lower-cased.
	Status string
	// Detail is the first reason the section gives: the rest of the Status
	// line, else the last cell of the first table row marked failed (the
	// execute skill's Error column), else a Summary, Error or Reason line.
	// Empty when the section gives none.
	Detail string
}

// failed reports whether the verdict says the stage did not do its work.
func (v handoffVerdict) failed() bool { return v.Status == handoffStatusFailed }

// recognised reports whether the status is one the harness knows.
func (v handoffVerdict) recognised() bool { return v.failed() || handoffStatusesOK[v.Status] }

// stopReason is the termination blob's free-text account of the verdict,
// in the "<source>: <what happened>" form the blob's other stop reasons
// use: the handoff's own reason when it gave one, else the bare status.
func (v handoffVerdict) stopReason() string {
	if v.Detail != "" {
		return "handoff: " + v.Detail
	}
	return "handoff: Status: " + v.Status
}

var (
	// handoffStatusLine matches the verdict line in the forms skills and
	// models actually write: `- Status: failed`, `* **Status**: passed`,
	// `**Status:** completed`, `Status: failed — docs/plan.md not found`.
	handoffStatusLine = regexp.MustCompile(`(?i)^\s*(?:[-*]\s*)?\**status\**\s*:\s*(.+?)\s*$`)
	// handoffDetailLine matches a line that carries a reason on its own.
	handoffDetailLine = regexp.MustCompile(`(?i)^\s*(?:[-*]\s*)?\**(?:summary|error|reason)\**\s*:\s*(.+?)\s*$`)
	// tableSeparator matches a Markdown table's header underline cells.
	tableSeparator = regexp.MustCompile(`^:?-+:?$`)
)

// stageHandoff returns the verdict from the last section of the handoff
// file under cloneDir, and false when there is nothing to read: no file,
// no `- Status:` line, or a file this stage did not touch — a verdict left
// by an earlier stage, or by an earlier run on this branch, is not this
// stage's. An unreadable file or base is logged and treated as nothing to
// read, so the outcome the turn earned stands.
func stageHandoff(cloneDir string, repo *gogit.Repository, baseSHA string) (handoffVerdict, bool) {
	changed, err := git.FileChangedSince(repo, baseSHA, handoffPath)
	if err != nil {
		logging.Warn("handoff: %v — not read", err)
		return handoffVerdict{}, false
	}
	if !changed {
		return handoffVerdict{}, false
	}
	content, err := os.ReadFile(filepath.Join(cloneDir, handoffPath))
	if err != nil {
		logging.Warn("handoff: read %s: %v — not read", handoffPath, err)
		return handoffVerdict{}, false
	}
	return parseHandoffVerdict(string(content))
}

// parseHandoffVerdict reads the verdict from the last `## ` section of a
// handoff document (the whole document when it has no such heading). ok is
// false when the section has no Status line.
func parseHandoffVerdict(content string) (v handoffVerdict, ok bool) {
	var tableDetail, lineDetail string
	for _, line := range lastHandoffSection(content) {
		if !ok {
			if m := handoffStatusLine.FindStringSubmatch(line); m != nil {
				v.Status, v.Detail = splitHandoffStatus(m[1])
				ok = true
				continue
			}
		}
		if tableDetail == "" {
			tableDetail = failedRowDetail(line)
		}
		if lineDetail == "" {
			if m := handoffDetailLine.FindStringSubmatch(line); m != nil {
				lineDetail = m[1]
			}
		}
	}
	if !ok {
		return handoffVerdict{}, false
	}
	if v.Detail == "" {
		v.Detail = tableDetail
	}
	if v.Detail == "" {
		v.Detail = lineDetail
	}
	v.Detail = clipHandoffDetail(v.Detail)
	return v, true
}

// lastHandoffSection returns the lines after the document's last `## `
// heading — the section the current stage appended — or every line when
// the document has no such heading. Deeper headings (`###`) belong to the
// section they sit in.
func lastHandoffSection(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	start := 0
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "## ") {
			start = i + 1
		}
	}
	return lines[start:]
}

// splitHandoffStatus separates a Status line's value into the status word
// and whatever follows it, so `failed — docs/plan.md not found` yields both.
func splitHandoffStatus(value string) (status, rest string) {
	value = strings.TrimLeft(value, "* \t")
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "", ""
	}
	status = strings.ToLower(strings.Trim(fields[0], ".,;:!*`\"'()"))
	rest = strings.TrimPrefix(value, fields[0])
	rest = strings.TrimSpace(strings.TrimLeft(rest, " \t-—–:|(,"))
	rest = strings.TrimSpace(strings.TrimRight(rest, ")"))
	return status, rest
}

// failedRowDetail returns the last cell of a Markdown table row that has a
// cell reading `failed` — the execute skill's Error column — or "" when the
// line is not such a row or the cell is a placeholder.
func failedRowDetail(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") {
		return ""
	}
	cells := strings.Split(strings.Trim(line, "|"), "|")
	failedRow, separatorRow := false, true
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
		// A lone "-" is a placeholder cell; only a row made entirely of
		// dashes is the header underline.
		if !tableSeparator.MatchString(cells[i]) {
			separatorRow = false
		}
		if strings.EqualFold(cells[i], handoffStatusFailed) {
			failedRow = true
		}
	}
	if separatorRow || !failedRow {
		return ""
	}
	last := cells[len(cells)-1]
	switch strings.ToLower(last) {
	case "", "-", "—", "–", handoffStatusFailed, "n/a", "none":
		return ""
	}
	return last
}

// clipHandoffDetail cuts a detail to handoffDetailLen runes with an
// ellipsis, so a skill's paragraph of reasons stays one line.
func clipHandoffDetail(detail string) string {
	if r := []rune(detail); len(r) > handoffDetailLen {
		return strings.TrimSpace(string(r[:handoffDetailLen-1])) + "…"
	}
	return detail
}
