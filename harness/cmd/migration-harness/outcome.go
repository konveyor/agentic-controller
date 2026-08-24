package main

import (
	"encoding/json"
	"os"

	"github.com/konveyor/migration-harness/internal/acp"
	"github.com/konveyor/migration-harness/internal/logging"
)

// outcome is the three-way result of a stage's primary prompt, per the
// harness exit-code contract (ADR 0011).
type outcome int

const (
	outcomeSucceeded outcome = iota
	outcomeFailed
	outcomeLimitReached
)

func (o outcome) String() string {
	switch o {
	case outcomeSucceeded:
		return "succeeded"
	case outcomeLimitReached:
		return "limitReached"
	default:
		return "failed"
	}
}

// exitCode maps outcome to the harness exit-code contract: 0
// succeeded, 1 failed, 2 limit reached with handoff committed.
func (o outcome) exitCode() int {
	switch o {
	case outcomeSucceeded:
		return 0
	case outcomeLimitReached:
		return 2
	default:
		return 1
	}
}

// limitKind names which execution limit triggered outcomeLimitReached.
// The zero value limitNone means outcome is not outcomeLimitReached.
type limitKind string

const (
	limitNone     limitKind = ""
	limitMaxTurns limitKind = "maxTurns"
	limitMaxCost  limitKind = "maxCost"
)

// stopReasonMaxTurns is the ACP spec's documented stopReason for a
// runtime-native turn limit. Not verified against a real goose process
// in this environment (no goose binary/LLM credentials available) — if
// goose reports a different string in practice, this is the one line
// to change, plus a regression test.
const stopReasonMaxTurns = "max_turn_requests"

// handoffPromptText is sent once when an execution limit is reached
// (ADR 0011). Wording is limit-agnostic — the agent doesn't need to
// know whether it was turns or cost that triggered wind-down.
const handoffPromptText = "You have reached your execution limit. Commit your current work and write a handoff to `.konveyor/handoff.md` documenting what you completed and what remains."

// classifyOutcome maps a SendPrompt result to a run outcome and, when
// the outcome is a limit, which limit fired. Isolated from I/O so it's
// unit-testable without a live goose connection.
func classifyOutcome(result *acp.PromptResult, err error) (outcome, limitKind) {
	if err != nil {
		return outcomeFailed, limitNone
	}
	switch {
	case result.StopReason == "cancelled" && result.CostLimitReached:
		return outcomeLimitReached, limitMaxCost
	case result.StopReason == "cancelled":
		// Viewer-initiated cancel, not a limit — unchanged from the
		// harness's existing behavior of treating a cancelled run as a
		// failed stage.
		return outcomeFailed, limitNone
	case result.StopReason == stopReasonMaxTurns:
		return outcomeLimitReached, limitMaxTurns
	default:
		return outcomeSucceeded, limitNone
	}
}

// usage is the compact usage-stats section of the termination blob.
type usage struct {
	TurnsUsed   int     `json:"turnsUsed,omitempty"`
	ContextUsed int     `json:"contextUsed,omitempty"`
	ContextSize int     `json:"contextSize,omitempty"`
	Cost        float64 `json:"cost,omitempty"`
}

// combineUsage merges the primary prompt's result with an optional
// handoff prompt's result (nil when no handoff ran). Turns are
// additive (each call's count is local to that call); cost and
// context are "last non-zero snapshot wins" — ACP cost is
// session-cumulative (a later value already includes everything
// before it) and context occupancy is a snapshot, not cumulative, so
// neither should be summed across the two calls.
func combineUsage(primary, handoff *acp.PromptResult) usage {
	u := usage{
		TurnsUsed:   primary.TurnsUsed,
		ContextUsed: primary.ContextUsed,
		ContextSize: primary.ContextSize,
		Cost:        primary.Cost,
	}
	if handoff == nil {
		return u
	}
	u.TurnsUsed += handoff.TurnsUsed
	if handoff.Cost > u.Cost {
		u.Cost = handoff.Cost
	}
	if handoff.ContextSize > 0 {
		u.ContextUsed = handoff.ContextUsed
		u.ContextSize = handoff.ContextSize
	}
	return u
}

// terminationBlob is the compact JSON written to /dev/termination-log
// (ADR 0011). Kept small: the kubelet truncates termination messages
// at 4096 bytes.
type terminationBlob struct {
	ExitCode     int    `json:"exitCode"`
	Outcome      string `json:"outcome"`
	LimitReached string `json:"limitReached,omitempty"`
	StopReason   string `json:"stopReason,omitempty"`
	Usage        *usage `json:"usage,omitempty"`
}

// terminationLogSizeWarning is a safety margin below the kubelet's
// 4096-byte termination-message ceiling (ADR 0011) — a canary for a
// future field addition that grows the blob, not a hard truncation.
const terminationLogSizeWarning = 3500

// defaultTerminationLogPath is where the kubelet reads a container's
// termination message from (part of the harness contract, ADR 0011).
const defaultTerminationLogPath = "/dev/termination-log"

// terminationLogPath returns the path to write the termination blob
// to. HARNESS_TERMINATION_LOG_PATH overrides it for tests and for
// running the harness outside a Sandbox pod.
func terminationLogPath() string {
	if v := os.Getenv("HARNESS_TERMINATION_LOG_PATH"); v != "" {
		return v
	}
	return defaultTerminationLogPath
}

// writeTerminationLog marshals term and writes it to path. Failure is
// logged, never fatal — it must never mask the real exit code.
func writeTerminationLog(path string, term terminationBlob) {
	data, err := json.Marshal(term)
	if err != nil {
		logging.Warn("termination log: marshal: %v", err)
		return
	}
	if len(data) > terminationLogSizeWarning {
		logging.Warn("termination log: %d bytes, approaching the kubelet's 4096-byte limit", len(data))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		logging.Warn("termination log: write %s: %v", path, err)
	}
}
