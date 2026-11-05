package goose

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"time"
)

// outputCapture tees everything goose writes on its own stdout and
// stderr to w, while keeping the most recent error-level lines.
//
// goose's stdio is where a provider's own words land: when a model call
// is rejected, the AWS, OpenAI or Vertex error text is logged by the
// goose process, and only a boilerplate JSON-RPC error travels back over
// ACP (#323). Forwarding that stdio to the pod log — which is all the
// harness did before — makes the one place the reason exists the one
// place nobody can read later, and it goes away with the pod. Keeping the
// last few error lines lets the harness name the reason on the run's own
// outcome instead.
//
// Nothing here changes what reaches w: the tee writes first and reports
// w's own result, so the pod log is byte-for-byte what it always was.
type outputCapture struct {
	w io.Writer

	mu sync.Mutex
	// partial is the current line's bytes, still waiting for its newline.
	partial []byte
	// truncated records that partial hit capturedLineBytes, so the rest
	// of this line is being dropped rather than spliced onto its head.
	truncated bool
	lines     []capturedLine
}

// capturedLine is one error-level line and when the harness read it.
// The timestamp is goose's wall clock as observed here, not the one in
// the line's own text: callers ask for errors since a point in their own
// execution ("since this prompt was sent"), and parsing goose's log
// format to answer that would tie the harness to it.
type capturedLine struct {
	at   time.Time
	text string
}

const (
	// capturedLines is how many error-level lines are kept. A failing
	// model call logs a handful — the SDK error, then goose's own
	// wrapping of it — and the useful one is not always the last.
	capturedLines = 8
	// capturedLineBytes bounds one line. goose renders an SDK error with
	// Debug formatting, so a single line runs long; the reason is at the
	// front, and providerErrorSummary cuts it again for display.
	capturedLineBytes = 4096
)

// errorLineMarkers are the shapes a line takes when it reports a failure
// rather than narrating progress. "ERROR" is tracing's level field, which
// is how goose logs its own failures; the exception and error markers
// catch the provider text goose passes through verbatim, including AWS's
// coral errors (AccessDeniedException, ThrottlingException), which do not
// always arrive on a line goose has labelled.
var errorLineMarkers = []string{
	"ERROR",
	"Exception",
	"error:",
	"Error:",
}

func newOutputCapture(w io.Writer) *outputCapture {
	return &outputCapture{w: w}
}

// Write forwards p to the underlying writer and scans it for error
// lines. The forward happens first and its result is returned unchanged:
// capturing must never cost the pod log a byte.
func (c *outputCapture) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.scan(p)
	return n, err
}

func (c *outputCapture) scan(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			c.appendPartial(p)
			return
		}
		c.appendPartial(p[:i])
		c.record(string(c.partial))
		c.partial, c.truncated = c.partial[:0], false
		p = p[i+1:]
	}
}

// appendPartial grows the current line up to capturedLineBytes. Past
// that the remainder of the line is dropped: a line's head carries the
// reason, and splicing its head onto its tail would read as one sentence
// the process never wrote.
func (c *outputCapture) appendPartial(b []byte) {
	if c.truncated {
		return
	}
	if room := capturedLineBytes - len(c.partial); len(b) > room {
		b, c.truncated = b[:room], true
	}
	c.partial = append(c.partial, b...)
}

func (c *outputCapture) record(line string) {
	line = strings.TrimSpace(stripANSI(line))
	if line == "" || !looksLikeErrorLine(line) {
		return
	}
	if c.truncated {
		line += "…"
	}
	c.lines = append(c.lines, capturedLine{at: time.Now(), text: line})
	if len(c.lines) > capturedLines {
		c.lines = c.lines[len(c.lines)-capturedLines:]
	}
}

// lastErrorSince returns the most recent error-level line read after
// since, with when it was read, or "" when goose logged none in that
// window. The caller needs the timestamp as well as the text: whether a
// line can be the reason a call failed depends on when it was read
// relative to the failure, not merely on being inside the window (see
// ServeProcess.ProviderError).
func (c *outputCapture) lastErrorSince(since time.Time) (string, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.lines) - 1; i >= 0; i-- {
		if c.lines[i].at.After(since) {
			return c.lines[i].text, c.lines[i].at
		}
	}
	return "", time.Time{}
}

// looksLikeErrorLine reports whether a line of goose's stdio is reporting
// a failure.
func looksLikeErrorLine(line string) bool {
	for _, m := range errorLineMarkers {
		if strings.Contains(line, m) {
			return true
		}
	}
	return false
}

// stripANSI removes CSI escape sequences. goose colours its log output
// when its subscriber has ANSI on, which would otherwise split the level
// field ("\x1b[31mERROR\x1b[0m") past any marker match — and put escape
// codes on an AgentRun's status.
func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		// Skip ESC, an optional '[', then the parameter and
		// intermediate bytes, then the final byte that ends the
		// sequence (0x40–0x7e).
		i++
		if i < len(s) && s[i] == '[' {
			i++
		}
		for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
			i++
		}
	}
	return b.String()
}
