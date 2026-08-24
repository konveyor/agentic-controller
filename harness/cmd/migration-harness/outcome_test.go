package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konveyor/migration-harness/internal/acp"
)

func TestOutcomeExitCode(t *testing.T) {
	cases := []struct {
		o    outcome
		want int
	}{
		{outcomeSucceeded, 0},
		{outcomeFailed, 1},
		{outcomeLimitReached, 2},
	}
	for _, c := range cases {
		if got := c.o.exitCode(); got != c.want {
			t.Errorf("%v.exitCode() = %d, want %d", c.o, got, c.want)
		}
	}
}

func TestClassifyOutcome(t *testing.T) {
	cases := []struct {
		name      string
		result    *acp.PromptResult
		err       error
		wantOut   outcome
		wantLimit limitKind
	}{
		{
			name:      "error is a failure",
			result:    nil,
			err:       errors.New("boom"),
			wantOut:   outcomeFailed,
			wantLimit: limitNone,
		},
		{
			name:      "natural completion succeeds",
			result:    &acp.PromptResult{StopReason: "end_turn"},
			wantOut:   outcomeSucceeded,
			wantLimit: limitNone,
		},
		{
			name:      "native turn limit is limitReached/maxTurns",
			result:    &acp.PromptResult{StopReason: "max_turn_requests"},
			wantOut:   outcomeLimitReached,
			wantLimit: limitMaxTurns,
		},
		{
			name:      "harness-triggered cost cancel is limitReached/maxCost",
			result:    &acp.PromptResult{StopReason: "cancelled", CostLimitReached: true},
			wantOut:   outcomeLimitReached,
			wantLimit: limitMaxCost,
		},
		{
			name:      "viewer cancel (no cost trigger) is a failure",
			result:    &acp.PromptResult{StopReason: "cancelled", CostLimitReached: false},
			wantOut:   outcomeFailed,
			wantLimit: limitNone,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotOut, gotLimit := classifyOutcome(c.result, c.err)
			if gotOut != c.wantOut || gotLimit != c.wantLimit {
				t.Errorf("classifyOutcome() = (%v, %v), want (%v, %v)", gotOut, gotLimit, c.wantOut, c.wantLimit)
			}
		})
	}
}

func TestCombineUsageNoHandoff(t *testing.T) {
	primary := &acp.PromptResult{TurnsUsed: 5, Cost: 2.5, ContextUsed: 1000, ContextSize: 200000}
	got := combineUsage(primary, nil)
	want := usage{TurnsUsed: 5, Cost: 2.5, ContextUsed: 1000, ContextSize: 200000}
	if got != want {
		t.Errorf("combineUsage() = %+v, want %+v", got, want)
	}
}

func TestCombineUsageAddsTurnsAndTakesMaxCost(t *testing.T) {
	primary := &acp.PromptResult{TurnsUsed: 5, Cost: 8.5}
	handoff := &acp.PromptResult{TurnsUsed: 2, Cost: 8.5} // cumulative — same or higher than primary's
	got := combineUsage(primary, handoff)
	if got.TurnsUsed != 7 {
		t.Errorf("TurnsUsed = %d, want 7 (additive)", got.TurnsUsed)
	}
	if got.Cost != 8.5 {
		t.Errorf("Cost = %v, want 8.5 (max, not sum)", got.Cost)
	}
}

func TestCombineUsageHandoffContextOverridesWhenReported(t *testing.T) {
	primary := &acp.PromptResult{ContextUsed: 1000, ContextSize: 200000}
	handoff := &acp.PromptResult{ContextUsed: 1200, ContextSize: 200000}
	got := combineUsage(primary, handoff)
	if got.ContextUsed != 1200 {
		t.Errorf("ContextUsed = %d, want 1200 (handoff's snapshot wins)", got.ContextUsed)
	}
}

func TestCombineUsageFallsBackToPrimaryWhenHandoffReportsNoContext(t *testing.T) {
	primary := &acp.PromptResult{ContextUsed: 1000, ContextSize: 200000}
	handoff := &acp.PromptResult{} // handoff prompt was too small to trigger a usage_update
	got := combineUsage(primary, handoff)
	if got.ContextUsed != 1000 || got.ContextSize != 200000 {
		t.Errorf("expected fallback to primary's snapshot, got %+v", got)
	}
}

func TestWriteTerminationLogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	term := terminationBlob{
		ExitCode:     2,
		Outcome:      "limitReached",
		LimitReached: "maxTurns",
		StopReason:   "max_turn_requests",
		Usage:        &usage{TurnsUsed: 170, Cost: 8.5},
	}
	writeTerminationLog(path, term)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got terminationBlob
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ExitCode != term.ExitCode || got.Outcome != term.Outcome ||
		got.LimitReached != term.LimitReached || got.StopReason != term.StopReason {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, term)
	}
	if got.Usage == nil || *got.Usage != *term.Usage {
		t.Errorf("usage round trip mismatch: got %+v, want %+v", got.Usage, term.Usage)
	}
}

func TestWriteTerminationLogHandlesLargeBlob(t *testing.T) {
	// Proves writeTerminationLog does not fail or corrupt the file when
	// the blob exceeds the size-warning threshold — the warning itself
	// goes through the logging package, which has no test-capturable
	// sink, so this checks the observable behavior (a successful,
	// intact write) rather than the log line.
	path := filepath.Join(t.TempDir(), "termination-log")
	huge := strings.Repeat("x", terminationLogSizeWarning+100)
	term := terminationBlob{ExitCode: 1, Outcome: "failed", StopReason: huge}
	writeTerminationLog(path, term)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got terminationBlob
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.StopReason != huge {
		t.Error("large blob was not written intact")
	}
}

func TestTerminationLogPathOverride(t *testing.T) {
	t.Setenv("HARNESS_TERMINATION_LOG_PATH", "/tmp/custom-termination-log")
	if got := terminationLogPath(); got != "/tmp/custom-termination-log" {
		t.Errorf("terminationLogPath() = %q, want override", got)
	}
}

func TestTerminationLogPathDefault(t *testing.T) {
	os.Unsetenv("HARNESS_TERMINATION_LOG_PATH")
	if got := terminationLogPath(); got != defaultTerminationLogPath {
		t.Errorf("terminationLogPath() = %q, want %q", got, defaultTerminationLogPath)
	}
}
