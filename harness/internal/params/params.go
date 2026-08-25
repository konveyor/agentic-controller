// Package params loads the controller-written parameter file and
// renders it for the agent prompt (ADR 0009).
package params

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Path is the in-Sandbox path where the controller mounts the
// three-section parameter file. Part of the harness/controller contract
// (ADR 0009) — any harness implementation must read this file.
const Path = "/run/konveyor/params.json"

// ReserveFraction is the portion of a configured maxTurns/maxCost
// budget the runtime is allowed to consume before the harness reserves
// the remainder for its wind-down handoff prompt (ADR 0011).
const ReserveFraction = 0.85

// NativeTurnLimit reserves ReserveFraction of a configured maxTurns for
// the runtime's own native enforcement (e.g. GOOSE_MAX_TURNS) — the same
// value the goose package sets as the env var. Exposed here so callers
// that need to independently recognize "the runtime's native limit was
// reached" (e.g. by comparing it against turns actually used) compute
// the identical number, not a second copy of the same arithmetic.
// Returns 0 (unset) when maxTurns <= 0.
func NativeTurnLimit(maxTurns int) int {
	if maxTurns <= 0 {
		return 0
	}
	native := int(math.Floor(float64(maxTurns) * ReserveFraction))
	if native < 1 {
		native = 1
	}
	return native
}

// Execution is the controller's resolved execution controls (ADR 0011) —
// first-class CRD fields with defined semantics, unlike the open-ended
// workflow/agent param maps, so they get their own typed section.
type Execution struct {
	Mode     string `json:"mode,omitempty"`
	MaxTurns int    `json:"maxTurns,omitempty"`
	MaxCost  string `json:"maxCost,omitempty"`
}

// File is the three-section structure the controller writes: workflow
// and agent parameter values, plus resolved execution controls. Any
// section may be absent (e.g. workflow is absent for standalone runs).
type File struct {
	Workflow  map[string]any `json:"workflow,omitempty"`
	Agent     map[string]any `json:"agent,omitempty"`
	Execution Execution      `json:"execution"`
}

// Load reads and parses the params file at path. A missing file is not
// an error — it means no params.json was mounted (an older controller,
// or a run with no parameters at all) — and yields a zero File.
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return File{}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("read %s: %w", path, err)
	}

	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}

// MaxTurns reports the execution section's maxTurns override, if
// present and a positive number.
func (f File) MaxTurns() (int, bool) {
	if f.Execution.MaxTurns <= 0 {
		return 0, false
	}
	return f.Execution.MaxTurns, true
}

// RenderSection renders the workflow and agent parameter values as the
// body of the prompt's "## Parameters" section (ADR 0009). Returns ""
// when there are no values to show, so the caller can omit the section
// entirely rather than emit an empty header.
func RenderSection(f File) string {
	parts := make([]string, 0, 2)
	if s := renderValues("Workflow", f.Workflow); s != "" {
		parts = append(parts, s)
	}
	if s := renderValues("Agent", f.Agent); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n\n")
}

func renderValues(label string, values map[string]any) string {
	if len(values) == 0 {
		return ""
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n", label)
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %s\n", k, formatValue(values[k]))
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatValue renders a JSON-decoded value (float64, bool, or string,
// per encoding/json's default decoding into map[string]any) as prompt
// text, so a whole number param reads as "5" rather than "5e+00".
func formatValue(v any) string {
	switch t := v.(type) {
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) && t >= math.MinInt64 && t <= math.MaxInt64 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}
