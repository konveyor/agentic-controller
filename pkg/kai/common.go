package kai

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Shared cobra command "Use" strings, reused across the resource command groups
// (gateway, agent, workflow, skill) so the verbs stay consistent.
const (
	useCreate   = "create [name]"
	useEdit     = "edit <name>"
	useDelete   = "delete <name>"
	useList     = "list"
	useGet      = "get <name>"
	useDescribe = "describe <name>"
)

// Shared table column headers reused across listing commands.
const (
	colName     = "NAME"
	colReady    = "READY"
	colPhase    = "PHASE"
	colAge      = "AGE"
	colRequired = "REQUIRED"
)

// readyStatus returns the status of the "Ready" condition from a slice of
// standard metav1.Conditions, or "Unknown" when absent.
func readyStatus(conditions []metav1.Condition) string {
	for i := range conditions {
		if conditions[i].Type == "Ready" {
			return string(conditions[i].Status)
		}
	}
	return "Unknown"
}

// age renders a creation timestamp as a compact human-readable duration.
func age(t metav1.Time) string {
	if t.IsZero() {
		return "<unknown>"
	}
	d := time.Since(t.Time)
	if d < 0 {
		return "0s"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// table writes a simple aligned table to w. header and each row must have the
// same number of columns.
func table(w io.Writer, header []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
	writeRow(tw, header)
	for _, r := range rows {
		writeRow(tw, r)
	}
	_ = tw.Flush()
}

func writeRow(w io.Writer, cols []string) {
	for i, c := range cols {
		if i > 0 {
			_, _ = fmt.Fprint(w, "\t")
		}
		_, _ = fmt.Fprint(w, c)
	}
	_, _ = fmt.Fprintln(w)
}
