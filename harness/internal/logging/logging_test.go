package logging

import (
	"os"
	"strings"
	"testing"
)

func TestOutputFunctions(t *testing.T) {
	// Capture stderr
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	colorEnabled = false

	Info("test %s", "info")
	Ok("test %s", "ok")
	Warn("test %s", "warn")
	Err("test %s", "err")
	Header("test %s", "header")

	w.Close()
	os.Stderr = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	for _, want := range []string{"test info", "test ok", "test warn", "test err", "test header"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q", want)
		}
	}
}
