// Package termination writes a machine-readable summary of the harness run
// to the pod's termination log. The kubelet copies the termination message
// into the container's terminated state, from where the controller lifts it
// onto AgentRunStatus.terminationData without interpretation (see CONTEXT.md).
package termination

import (
	"encoding/json"
	"os"
)

// defaultPath is the kubelet's default terminationMessagePath. The harness
// container relies on this default (no custom path set on the pod spec).
const defaultPath = "/dev/termination-log"

// envPath overrides the termination log path, primarily for tests.
const envPath = "HARNESS_TERMINATION_LOG"

// maxMessageLen bounds the human-readable message so the encoded payload
// stays well under the kubelet's 4096-byte termination-message cap (the
// remaining bytes leave room for the JSON envelope and the reason field).
const maxMessageLen = 3072

// Data is the termination payload. It is intentionally small and extensible
// so future fields (e.g. usage/cost data) can be added without changing the
// controller, which copies the raw JSON verbatim.
type Data struct {
	// Reason is a short machine-readable code (e.g. "UnsupportedSourceSCM").
	Reason string `json:"reason,omitempty"`
	// Message is the human-readable detail surfaced on the AgentRun status.
	Message string `json:"message,omitempty"`
}

// Write marshals d to JSON and writes it to the termination log. The path is
// taken from HARNESS_TERMINATION_LOG when set, otherwise /dev/termination-log.
// The message is truncated to keep the payload under the kubelet's cap.
func Write(d Data) error {
	if len(d.Message) > maxMessageLen {
		d.Message = d.Message[:maxMessageLen]
	}
	payload, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return os.WriteFile(path(), payload, 0o644)
}

func path() string {
	if p := os.Getenv(envPath); p != "" {
		return p
	}
	return defaultPath
}
