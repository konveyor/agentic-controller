package goose

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// errorSince is lastErrorSince without the timestamp, for the tests that
// only ask what was kept.
func (c *outputCapture) errorSince(since time.Time) string {
	line, _ := c.lastErrorSince(since)
	return line
}

// TestOutputCaptureForwardsEveryByte is the property that matters most:
// the capture sits in front of the pod log, so whatever it does to find
// error lines, the log must read exactly as it did before.
func TestOutputCaptureForwardsEveryByte(t *testing.T) {
	var log bytes.Buffer
	c := newOutputCapture(&log)

	writes := []string{
		"starting goose\n",
		"2026-11-03T21:00:00Z ERROR goose::providers::bedrock: Access",
		"DeniedException: not authorized\n",
		"no trailing newline here",
	}
	for _, w := range writes {
		if n, err := c.Write([]byte(w)); err != nil || n != len(w) {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil", w, n, err, len(w))
		}
	}

	if got, want := log.String(), strings.Join(writes, ""); got != want {
		t.Errorf("forwarded %q, want %q", got, want)
	}
}

func TestOutputCaptureKeepsTheLastErrorLine(t *testing.T) {
	since := time.Now()
	c := newOutputCapture(&bytes.Buffer{})
	c.Write([]byte("INFO goose: session started\n"))
	c.Write([]byte("ERROR goose::providers::bedrock: first failure\n"))
	c.Write([]byte("INFO goose: retrying\n"))
	c.Write([]byte("ERROR goose::providers::bedrock: AccessDeniedException: not authorized\n"))

	want := "ERROR goose::providers::bedrock: AccessDeniedException: not authorized"
	if got := c.errorSince(since); got != want {
		t.Errorf("errorSince = %q, want %q", got, want)
	}
}

// A line split across writes is one line: os/exec's copier hands over
// whatever the pipe had, not whole lines.
func TestOutputCaptureJoinsALineSplitAcrossWrites(t *testing.T) {
	since := time.Now()
	c := newOutputCapture(&bytes.Buffer{})
	c.Write([]byte("ERROR goose::providers::bedrock: Access"))
	c.Write([]byte("DeniedException: User is not"))
	c.Write([]byte(" authorized to perform bedrock:InvokeModel\n"))

	want := "ERROR goose::providers::bedrock: AccessDeniedException: User is not authorized to perform bedrock:InvokeModel"
	if got := c.errorSince(since); got != want {
		t.Errorf("errorSince = %q, want %q", got, want)
	}
}

// Several lines in one write, and a partial line left for the next one.
func TestOutputCaptureHandlesManyLinesInOneWrite(t *testing.T) {
	since := time.Now()
	c := newOutputCapture(&bytes.Buffer{})
	c.Write([]byte("INFO a\nERROR b\nINFO c\nERROR d"))
	if got := c.errorSince(since); got != "ERROR b" {
		t.Errorf("before the newline arrives, errorSince = %q, want %q", got, "ERROR b")
	}
	c.Write([]byte("\n"))
	if got := c.errorSince(since); got != "ERROR d" {
		t.Errorf("errorSince = %q, want %q", got, "ERROR d")
	}
}

func TestOutputCaptureIgnoresNarration(t *testing.T) {
	since := time.Now()
	c := newOutputCapture(&bytes.Buffer{})
	for _, line := range []string{
		"INFO goose: session started\n",
		"DEBUG goose::providers: request sent\n",
		"wrote 42 bytes\n",
	} {
		c.Write([]byte(line))
	}
	if got := c.errorSince(since); got != "" {
		t.Errorf("errorSince = %q, want \"\" — none of those lines reports a failure", got)
	}
}

// The provider's own text is kept even when goose did not label the line:
// an AWS coral error names itself and nothing else has to.
func TestOutputCaptureKeepsAnUnlabelledProviderError(t *testing.T) {
	since := time.Now()
	c := newOutputCapture(&bytes.Buffer{})
	c.Write([]byte("ThrottlingException: Too many requests, please wait before trying again\n"))
	if got := c.errorSince(since); !strings.HasPrefix(got, "ThrottlingException:") {
		t.Errorf("errorSince = %q, want the ThrottlingException line", got)
	}
}

// An error from earlier in the run is not an explanation for the call
// that just failed.
func TestOutputCaptureErrorSinceIsAWindow(t *testing.T) {
	c := newOutputCapture(&bytes.Buffer{})
	c.Write([]byte("ERROR goose: a transient tool error, long before the prompt\n"))

	time.Sleep(2 * time.Millisecond)
	since := time.Now()
	if got := c.errorSince(since); got != "" {
		t.Errorf("errorSince = %q, want \"\" — that line predates the window", got)
	}

	time.Sleep(2 * time.Millisecond)
	c.Write([]byte("ERROR goose::providers: the call that failed\n"))
	if got := c.errorSince(since); got != "ERROR goose::providers: the call that failed" {
		t.Errorf("errorSince = %q, want the line written inside the window", got)
	}
}

func TestOutputCaptureBoundsWhatItKeeps(t *testing.T) {
	since := time.Now()
	c := newOutputCapture(&bytes.Buffer{})
	for i := 0; i < capturedLines*3; i++ {
		c.Write([]byte("ERROR goose: failure\n"))
	}
	c.mu.Lock()
	kept := len(c.lines)
	c.mu.Unlock()
	if kept != capturedLines {
		t.Errorf("kept %d lines, want the cap of %d", kept, capturedLines)
	}
	if got := c.errorSince(since); got != "ERROR goose: failure" {
		t.Errorf("errorSince = %q, want the retained failure line", got)
	}
}

// goose renders an SDK error with Debug formatting, so one line can be
// enormous. The log still gets all of it; what is kept is the head, where
// the reason is, marked as cut rather than spliced onto the tail.
func TestOutputCaptureTruncatesAnEnormousLine(t *testing.T) {
	since := time.Now()
	var log bytes.Buffer
	c := newOutputCapture(&log)

	head := "ERROR goose::providers::bedrock: AccessDeniedException: "
	line := head + strings.Repeat("x", capturedLineBytes*2)
	c.Write([]byte(line + "\n"))

	if log.Len() != len(line)+1 {
		t.Errorf("forwarded %d bytes, want all %d", log.Len(), len(line)+1)
	}
	got := c.errorSince(since)
	if !strings.HasPrefix(got, head) {
		t.Errorf("kept %q, want it to start with the reason %q", got, head)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("kept %q, want it marked as truncated", got)
	}
	if len([]byte(got)) > capturedLineBytes+len("…") {
		t.Errorf("kept %d bytes, want at most %d", len([]byte(got)), capturedLineBytes+len("…"))
	}
}

// Once a line overflows, its tail is dropped — not recorded as a line of
// its own, and not spliced onto the head.
func TestOutputCaptureDropsTheTailOfAnOverflowingLine(t *testing.T) {
	since := time.Now()
	c := newOutputCapture(&bytes.Buffer{})
	c.Write([]byte("ERROR head " + strings.Repeat("x", capturedLineBytes)))
	c.Write([]byte("ERROR tail that must not be read as its own line\n"))
	c.Write([]byte("ERROR the next real line\n"))

	if got := c.errorSince(since); got != "ERROR the next real line" {
		t.Errorf("errorSince = %q, want the line after the overflowing one", got)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.lines {
		if strings.Contains(l.text, "ERROR tail") {
			t.Errorf("kept %q — the tail of an overflowing line is not a line", l.text)
		}
	}
}

func TestOutputCaptureStripsANSI(t *testing.T) {
	since := time.Now()
	c := newOutputCapture(&bytes.Buffer{})
	c.Write([]byte("2026-11-03T21:00:00Z \x1b[31mERROR\x1b[0m goose: \x1b[1mAccessDeniedException\x1b[0m: nope\n"))

	want := "2026-11-03T21:00:00Z ERROR goose: AccessDeniedException: nope"
	if got := c.errorSince(since); got != want {
		t.Errorf("errorSince = %q, want %q", got, want)
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"no escapes", "plain ERROR line", "plain ERROR line"},
		{"colour", "\x1b[31mERROR\x1b[0m", "ERROR"},
		{"multi-param", "\x1b[1;33mWARN\x1b[0m x", "WARN x"},
		{"truncated sequence", "ERROR \x1b[", "ERROR "},
		{"bare escape", "ERROR \x1b", "ERROR "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripANSI(tt.in); got != tt.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ProviderError waits, because the JSON-RPC error and goose's stdio reach
// the harness by different paths and the response can win.
func TestProviderErrorWaitsForTheLine(t *testing.T) {
	c := newOutputCapture(&bytes.Buffer{})
	srv := &ServeProcess{output: c}
	sent := time.Now()

	go func() {
		time.Sleep(40 * time.Millisecond)
		c.Write([]byte("ERROR goose::providers::bedrock: AccessDeniedException: not authorized\n"))
	}()

	got := srv.ProviderError(sent, time.Now(), 2*time.Second)
	if !strings.Contains(got, "AccessDeniedException") {
		t.Errorf("ProviderError = %q, want the line written while it waited", got)
	}
}

// The regression djzager found in review: a stage prompt runs many turns,
// and a recoverable tool error logged during one of them is already
// captured when the call fails. Returning it the moment the failure
// arrives would make an unrelated error the viewer message and the
// termination reason, and would never wait for the provider error racing
// over stdio 40ms behind it.
func TestProviderErrorWaitsThroughAnEarlierToolError(t *testing.T) {
	c := newOutputCapture(&bytes.Buffer{})
	srv := &ServeProcess{output: c}
	sent := time.Now()

	c.Write([]byte("ERROR goose::developer: text_editor failed: No such file or directory\n"))

	failedAt := time.Now()
	go func() {
		time.Sleep(40 * time.Millisecond)
		c.Write([]byte("ERROR goose::providers::bedrock: AccessDeniedException: not authorized\n"))
	}()

	got := srv.ProviderError(sent, failedAt, 2*time.Second)
	if !strings.Contains(got, "AccessDeniedException") {
		t.Errorf("ProviderError = %q, want the provider error that arrived after the failure", got)
	}
	if strings.Contains(got, "text_editor") {
		t.Errorf("ProviderError = %q, want the earlier tool error not to be reported as the reason", got)
	}
}

// The usual case: goose logs the provider's error, then sends the
// JSON-RPC error, so the line can already have been read when the call
// comes back. It is about this failure and it is the answer.
func TestProviderErrorTakesTheLineReadJustBeforeTheFailure(t *testing.T) {
	c := newOutputCapture(&bytes.Buffer{})
	srv := &ServeProcess{output: c}
	sent := time.Now()
	c.Write([]byte("ERROR goose::providers::bedrock: AccessDeniedException: not authorized\n"))

	got := srv.ProviderError(sent, time.Now(), 100*time.Millisecond)
	if !strings.Contains(got, "AccessDeniedException") {
		t.Errorf("ProviderError = %q, want the line read just before the failure", got)
	}
}

// Far enough back and it is not about this call at all, whatever it says.
func TestProviderErrorIgnoresAnErrorFromEarlierInTheStage(t *testing.T) {
	c := newOutputCapture(&bytes.Buffer{})
	srv := &ServeProcess{output: c}
	sent := time.Now()
	c.Write([]byte("ERROR goose::developer: text_editor failed: No such file or directory\n"))

	time.Sleep(80 * time.Millisecond)
	if got := srv.ProviderError(sent, time.Now(), 50*time.Millisecond); got != "" {
		t.Errorf("ProviderError = %q, want \"\" — that error predates the window around the failure", got)
	}
}

// notBefore is a hard floor even inside the window: a line from before
// this prompt was sent is never the reason this prompt failed.
func TestProviderErrorNeverReportsFromBeforeThePrompt(t *testing.T) {
	c := newOutputCapture(&bytes.Buffer{})
	srv := &ServeProcess{output: c}
	c.Write([]byte("ERROR goose: a failure from the stage before this prompt\n"))

	time.Sleep(2 * time.Millisecond)
	sent := time.Now()
	if got := srv.ProviderError(sent, time.Now(), 60*time.Millisecond); got != "" {
		t.Errorf("ProviderError = %q, want \"\" — that line predates the prompt", got)
	}
}

func TestProviderErrorGivesUpWhenGooseLoggedNothing(t *testing.T) {
	srv := &ServeProcess{output: newOutputCapture(&bytes.Buffer{})}
	start := time.Now()
	if got := srv.ProviderError(start, start, 60*time.Millisecond); got != "" {
		t.Errorf("ProviderError = %q, want \"\"", got)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("waited %s, want it to give up at the grace period", waited)
	}
}

// A ServeProcess from before the capture existed, or one a test built by
// hand, must not panic.
func TestProviderErrorWithoutCapture(t *testing.T) {
	now := time.Now()
	if got := (&ServeProcess{}).ProviderError(now, now, time.Millisecond); got != "" {
		t.Errorf("ProviderError = %q, want \"\"", got)
	}
}
