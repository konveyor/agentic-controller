package main

import (
	"encoding/json"
	"errors"
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
// runtime-native turn limit. Kept as a fallback in case a future goose
// version reports it gracefully, but empirically (live-tested against
// goose 1.36.0 + Vertex AI), this is NOT what actually happens — see
// the acp.ErrConnectionLost handling in classifyOutcome below.
const stopReasonMaxTurns = "max_turn_requests"

// handoffPromptText is sent once when an execution limit is reached
// (ADR 0011). Wording is limit-agnostic — the agent doesn't need to
// know whether it was turns or cost that triggered wind-down. It
// explicitly tells the agent to stop rather than resume the original
// task: live testing showed that without this, the agent — still
// holding the unfinished original instructions in context, and given
// a fresh native turn budget for this new prompt call — kept working
// the original task after writing the handoff instead of ending its
// turn.
const handoffPromptText = "You have reached your execution limit and must stop working on the original task now, regardless of what remains incomplete. Do only this: write a handoff to `.konveyor/handoff.md` documenting what you completed and what remains, then commit both your current work and `.konveyor/handoff.md` together in a single commit. Once that commit succeeds, end your turn immediately — do not resume the original task or perform any further actions."

// classifyOutcome maps a SendPrompt result to a run outcome and, when
// the outcome is a limit, which limit fired. nativeMaxTurns is the
// GOOSE_MAX_TURNS value actually configured for this run (0 if maxTurns
// was unset) — see the TurnsUsed comparison below for why it's needed.
// Isolated from I/O so it's unit-testable without a live goose connection.
func classifyOutcome(result *acp.PromptResult, err error, nativeMaxTurns int) (outcome, limitKind) {
	if err != nil {
		// goose does not gracefully report a stopReason when its native
		// GOOSE_MAX_TURNS limit is hit — it drops the websocket connection
		// instead (confirmed empirically; ADR 0011 assumed a graceful
		// "max_turn_requests" stopReason this goose version does not send).
		// Real turn progress plus an abrupt disconnect is the best signal
		// available that the native limit fired, rather than a genuine
		// early failure (disconnect before any turn ran).
		if errors.Is(err, acp.ErrConnectionLost) &&
			result != nil &&
			nativeMaxTurns > 0 &&
			result.TurnsUsed >= nativeMaxTurns {
			return outcomeLimitReached, limitMaxTurns
		}
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
	case nativeMaxTurns > 0 && result.TurnsUsed >= nativeMaxTurns:
		// Confirmed empirically: goose reports a clean "end_turn" even
		// when it silently truncated the task at its native
		// GOOSE_MAX_TURNS ceiling — stopReason does not distinguish
		// "genuinely done" from "cut off by the limit" at all. Turns
		// actually used reaching the configured ceiling is the only
		// reliable signal this goose version gives us.
		return outcomeLimitReached, limitMaxTurns
	case result.CostLimitReached:
		// Cost limit was exceeded during the turn, but goose finished
		// naturally and returned a clean stopReason before the cancel
		// landed. Keep outcomeSucceeded (exit 0) so a finished run is not
		// retroactively failed, while recording limitMaxCost so terminationData
		// reflects that the cost ceiling was hit.
		return outcomeSucceeded, limitMaxCost
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

// maxTerminationLogBytes is the kubelet's hard termination-message ceiling
// (ADR 0011). Exceeding this causes the kubelet to truncate mid-JSON, breaking
// unmarshaling in the controller.
const maxTerminationLogBytes = 4096

// terminationLogSizeWarning is a safety margin below the kubelet's
// 4096-byte termination-message ceiling (ADR 0011) — a canary for a
// future field addition that grows the blob.
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

// writeTerminationLog marshals term and writes it to path, guaranteeing valid
// JSON within the kubelet's 4096-byte ceiling. Failure is logged, never fatal —
// it must never mask the real exit code.
func writeTerminationLog(path string, term terminationBlob) {
	data, err := json.Marshal(term)
	if err != nil {
		logging.Warn("termination log: marshal: %v", err)
		return
	}
	if len(data) > maxTerminationLogBytes {
		logging.Warn("termination log: %d bytes exceeds %d-byte limit; trimming to fit", len(data), maxTerminationLogBytes)
		if len(term.StopReason) > 200 {
			term.StopReason = term.StopReason[:200] + "…"
		}
		data, err = json.Marshal(term)
		if err != nil || len(data) > maxTerminationLogBytes {
			// Fall back to minimal valid JSON preserving exitCode, outcome, and limitReached.
			term = terminationBlob{
				ExitCode:     term.ExitCode,
				Outcome:      term.Outcome,
				LimitReached: term.LimitReached,
			}
			data, _ = json.Marshal(term)
		}
	} else if len(data) > terminationLogSizeWarning {
		logging.Warn("termination log: %d bytes, approaching the kubelet's 4096-byte limit", len(data))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		logging.Warn("termination log: write %s: %v", path, err)
	}
}
