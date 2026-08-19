package termination

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteProducesJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	t.Setenv(envPath, path)

	want := Data{Reason: "UnsupportedSourceSCM", Message: "only git is supported"}
	if err := Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read termination log: %v", err)
	}
	var got Data
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal %q: %v", raw, err)
	}
	if got != want {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
}

func TestWriteTruncatesLongMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	t.Setenv(envPath, path)

	long := strings.Repeat("x", maxMessageLen*2)
	if err := Write(Data{Reason: "StageFailed", Message: long}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read termination log: %v", err)
	}
	// The whole payload must stay under the kubelet's 4096-byte cap.
	if len(raw) > 4096 {
		t.Errorf("payload = %d bytes, want <= 4096", len(raw))
	}
	var got Data
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Message) != maxMessageLen {
		t.Errorf("message len = %d, want %d", len(got.Message), maxMessageLen)
	}
}

func TestWriteDefaultPath(t *testing.T) {
	// With no override set, path() falls back to the kubelet default.
	t.Setenv(envPath, "")
	if got := path(); got != defaultPath {
		t.Errorf("path() = %q, want %q", got, defaultPath)
	}
}
