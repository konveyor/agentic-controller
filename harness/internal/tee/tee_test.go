package tee

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/konveyor/migration-harness/internal/acp"
)

// fakeGoose accepts ACP WebSocket connections, answers every request with
// a canned result, and records frames per connection. The test can push
// frames on any accepted connection (conn 1 plays the run connection).
type fakeGoose struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	conns       []*websocket.Conn
	seen        map[int][]string // 1-based conn index -> frames received
	dialHeaders []string         // X-Secret-Key header per accepted dial
}

func newFakeGoose(t *testing.T) *fakeGoose {
	g := &fakeGoose{t: t, seen: make(map[int][]string)}
	upgrader := websocket.Upgrader{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.dialHeaders = append(g.dialHeaders, r.Header.Get("X-Secret-Key"))
		g.mu.Unlock()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		g.mu.Lock()
		g.conns = append(g.conns, conn)
		idx := len(g.conns)
		g.mu.Unlock()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			g.mu.Lock()
			g.seen[idx] = append(g.seen[idx], string(data))
			g.mu.Unlock()

			var req struct {
				ID     *int64 `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(data, &req) == nil && req.ID != nil && req.Method != "" {
				resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"echo":%q}}`, *req.ID, req.Method)
				g.mu.Lock()
				conn.WriteMessage(websocket.TextMessage, []byte(resp))
				g.mu.Unlock()
			}
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGoose) addr() string {
	u, _ := url.Parse(g.srv.URL)
	return u.Host
}

// pushTo writes a frame on the nth accepted connection (1-based).
func (g *fakeGoose) pushTo(n int, frame string) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		g.mu.Lock()
		if len(g.conns) >= n {
			err := g.conns[n-1].WriteMessage(websocket.TextMessage, []byte(frame))
			g.mu.Unlock()
			if err != nil {
				g.t.Errorf("push to conn %d: %v", n, err)
			}
			return
		}
		g.mu.Unlock()
		if time.Now().After(deadline) {
			g.t.Fatalf("conn %d never appeared", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (g *fakeGoose) framesOn(n int) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.seen[n]...)
}

const (
	testKey = "tee-test-key"

	// runSessionID is the run session every startTee attaches; existing
	// teed-frame fixtures use it too.
	runSessionID = "run-s1"
)

// startTee stands up fakeGoose, the run connection, and a tee server on an
// ephemeral port.
func startTee(t *testing.T, cfg Config) (*fakeGoose, *acp.WSClient, *Server) {
	t.Helper()
	g := newFakeGoose(t)

	host, portStr, _ := strings.Cut(g.addr(), ":")
	port, _ := strconv.Atoi(portStr)
	runConn, err := acp.NewWSClient(host, port, testKey)
	if err != nil {
		t.Fatalf("run conn: %v", err)
	}
	t.Cleanup(func() { runConn.Close() })

	cfg.SecretKey = testKey
	cfg.UpstreamAddr = g.addr()
	s := New(cfg)
	if err := s.Start(0); err != nil {
		t.Fatalf("start tee: %v", err)
	}
	t.Cleanup(s.Stop)
	s.AttachRun(runConn, runSessionID)

	// The run connection must be fakeGoose's conn 1 before any viewer
	// dials, so pushTo(1, ...) deterministically targets the run.
	deadline := time.Now().Add(5 * time.Second)
	for {
		g.mu.Lock()
		n := len(g.conns)
		g.mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run connection never reached fake goose")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return g, runConn, s
}

// viewerCount reports registered viewers, for attach synchronization.
func viewerCount(s *Server) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.viewers)
}

type viewerConn struct {
	t    *testing.T
	conn *websocket.Conn
	recv chan string
}

func dialViewer(t *testing.T, s *Server, token string) (*viewerConn, error) {
	before := viewerCount(s)
	u := fmt.Sprintf("ws://%s/acp?token=%s", s.Addr(), url.QueryEscape(token))
	conn, resp, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("dial: %w (status %d)", err, resp.StatusCode)
		}
		return nil, err
	}
	v := &viewerConn{t: t, conn: conn, recv: make(chan string, 64)}
	t.Cleanup(func() { conn.Close() })
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				close(v.recv)
				return
			}
			v.recv <- string(data)
		}
	}()

	// Handshake success precedes registration (serveViewer runs on its
	// own goroutine) — wait for the attach to be observable so tests can
	// broadcast immediately after dialing.
	deadline := time.Now().Add(5 * time.Second)
	for viewerCount(s) <= before {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("viewer never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return v, nil
}

// expect returns the next frame satisfying pred within the deadline.
func (v *viewerConn) expect(what string, pred func(string) bool) string {
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-v.recv:
			if !ok {
				v.t.Fatalf("connection closed while waiting for %s", what)
			}
			if pred(f) {
				return f
			}
		case <-deadline:
			v.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestViewerPipeAndTee(t *testing.T) {
	g, _, s := startTee(t, Config{})

	v, err := dialViewer(t, s, testKey)
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	// The viewer's own traffic pipes verbatim to its private goose conn.
	if err := v.conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":7,"method":"initialize","params":{}}`)); err != nil {
		t.Fatalf("viewer write: %v", err)
	}
	v.expect("initialize echo", func(f string) bool {
		return strings.Contains(f, `"echo":"initialize"`) && strings.Contains(f, `"id":7`)
	})

	// A run-connection update is teed to the viewer unmodified.
	teed := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"run-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"live"}}}}`
	g.pushTo(1, teed)
	got := v.expect("teed run update", func(f string) bool {
		return strings.Contains(f, "run-s1")
	})
	if got != teed {
		t.Fatalf("teed frame altered:\n got %s\nwant %s", got, teed)
	}

	// Non-update notifications on the run connection are not teed.
	g.pushTo(1, `{"jsonrpc":"2.0","method":"other/notification","params":{"sessionId":"run-s1"}}`)
	select {
	case f := <-v.recv:
		if strings.Contains(f, "other/notification") {
			t.Fatalf("non-update notification teed: %s", f)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

func TestViewerAuth(t *testing.T) {
	_, _, s := startTee(t, Config{})

	if _, err := dialViewer(t, s, "wrong-key"); err == nil {
		t.Fatal("bad token accepted")
	}

	// X-Secret-Key header carrier (what the hub proxy sends) works too.
	h := http.Header{}
	h.Set("X-Secret-Key", testKey)
	conn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://%s/acp", s.Addr()), h)
	if err != nil {
		t.Fatalf("header auth: %v", err)
	}
	conn.Close()

	// healthz needs no auth.
	resp, err := http.Get(fmt.Sprintf("http://%s/healthz", s.Addr()))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %v %v", err, resp)
	}
	resp.Body.Close()
}

func TestGarbageAndDisconnectDoNotAffectRun(t *testing.T) {
	g, runConn, s := startTee(t, Config{})

	v, err := dialViewer(t, s, testKey)
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	// Garbage from a viewer pipes to its own goose conn (conn 2) and is
	// otherwise inert.
	if err := v.conn.WriteMessage(websocket.TextMessage, []byte("not json at all {{{")); err != nil {
		t.Fatalf("garbage write: %v", err)
	}

	// The tee still works after garbage.
	g.pushTo(1, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"after-garbage","update":{}}}`)
	v.expect("teed frame after garbage", func(f string) bool { return strings.Contains(f, "after-garbage") })

	// Abrupt viewer disconnect: broadcasting continues harmlessly and the
	// run connection stays up.
	v.conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		n := len(s.viewers)
		s.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("viewer never removed after disconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	g.pushTo(1, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"nobody-watching","update":{}}}`)

	select {
	case <-runConn.Done():
		t.Fatal("run connection died")
	case <-time.After(200 * time.Millisecond):
	}

	if frames := g.framesOn(2); len(frames) == 0 || !strings.Contains(frames[0], "not json") {
		t.Fatalf("garbage should have piped to the viewer's goose conn, saw %v", frames)
	}
}

func TestForwardPermission(t *testing.T) {
	_, _, s := startTee(t, Config{HITLTimeout: 300 * time.Millisecond})

	// Nobody attached.
	params := json.RawMessage(`{"toolCall":{"title":"x"},"options":[]}`)
	if _, outcome := s.ForwardPermission(params); outcome != acp.ForwardNoViewers {
		t.Fatalf("expected NoViewers, got %v", outcome)
	}

	v, err := dialViewer(t, s, testKey)
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	// Viewer answers: result relayed, and the answer must NOT leak into
	// the viewer's own goose pipe.
	type fwdOut struct {
		result  json.RawMessage
		outcome acp.PermissionForwardOutcome
	}
	done := make(chan fwdOut, 1)
	go func() {
		r, o := s.ForwardPermission(params)
		done <- fwdOut{r, o}
	}()

	ask := v.expect("forwarded ask", func(f string) bool { return strings.Contains(f, "kperm-") })
	var askFrame struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal([]byte(ask), &askFrame); err != nil {
		t.Fatalf("ask frame: %v", err)
	}
	if askFrame.Method != "session/request_permission" || !strings.HasPrefix(askFrame.ID, "kperm-") {
		t.Fatalf("bad ask frame: %s", ask)
	}
	if string(askFrame.Params) != string(params) {
		t.Fatalf("params altered: %s", askFrame.Params)
	}

	answer := fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"result":{"outcome":{"outcome":"selected","optionId":"allow_once"}}}`, askFrame.ID)
	if err := v.conn.WriteMessage(websocket.TextMessage, []byte(answer)); err != nil {
		t.Fatalf("answer write: %v", err)
	}

	out := <-done
	if out.outcome != acp.ForwardAnswered {
		t.Fatalf("expected Answered, got %v", out.outcome)
	}
	if !strings.Contains(string(out.result), `"optionId":"allow_once"`) {
		t.Fatalf("result not relayed: %s", out.result)
	}

	// Unanswered ask times out — and flips viewers to unresponsive.
	if _, outcome := s.ForwardPermission(params); outcome != acp.ForwardTimeout {
		t.Fatalf("expected Timeout, got %v", outcome)
	}

	// While unresponsive, the next ask fast-denies instead of parking for
	// another full window (measured: must return well under the timeout).
	start := time.Now()
	if _, outcome := s.ForwardPermission(params); outcome != acp.ForwardNoViewers {
		t.Fatalf("unresponsive viewers should fast fail-closed, got %v", outcome)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("fast-deny took %v — parked on the timeout window", elapsed)
	}

	// A late answer to the timed-out ask is ignored as an answer, but it
	// proves a human is present — forwarding resumes.
	late := `{"jsonrpc":"2.0","id":"kperm-2","result":{"outcome":{"outcome":"cancelled"}}}`
	if err := v.conn.WriteMessage(websocket.TextMessage, []byte(late)); err != nil {
		t.Fatalf("late answer write: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !s.responsive.Load() {
		if time.Now().After(deadline) {
			t.Fatal("late kperm answer did not restore responsiveness")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, outcome := s.ForwardPermission(params); outcome != acp.ForwardTimeout {
		t.Fatalf("after human activity asks should forward again, got %v", outcome)
	}
}

func TestGooseCustomUpdatesTeed(t *testing.T) {
	g, _, s := startTee(t, Config{})

	v, err := dialViewer(t, s, testKey)
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	// goose's custom notification channel (usage_update, status_message)
	// tees exactly like session/update.
	teed := `{"jsonrpc":"2.0","method":"_goose/unstable/session/update","params":{"sessionId":"run-s1","update":{"sessionUpdate":"usage_update","used":1234,"contextLimit":200000}}}`
	g.pushTo(1, teed)
	got := v.expect("teed usage update", func(f string) bool {
		return strings.Contains(f, "usage_update")
	})
	if got != teed {
		t.Fatalf("custom update altered:\n got %s\nwant %s", got, teed)
	}
}

func TestViewerSteerRelayedToRunConnection(t *testing.T) {
	g, _, s := startTee(t, Config{SteerEnabled: true})

	v, err := dialViewer(t, s, testKey)
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	// A steer naming the run session must land on the RUN connection
	// (conn 1) — goose scopes the active run to the connection that
	// started it — and the reply must come back under the viewer's own
	// string id, never touching the viewer's private pipe (conn 2).
	steer := `{"jsonrpc":"2.0","id":"viewer-steer-1","method":"_goose/unstable/session/steer","params":{"sessionId":"run-s1","expectedRunId":"run_abc","prompt":[{"type":"text","text":"stop editing pom.xml; migrate the REST layer first"}]}}`
	if err := v.conn.WriteMessage(websocket.TextMessage, []byte(steer)); err != nil {
		t.Fatalf("steer write: %v", err)
	}

	reply := v.expect("steer reply", func(f string) bool {
		return strings.Contains(f, "viewer-steer-1") && strings.Contains(f, `"echo":"_goose/unstable/session/steer"`)
	})
	var replyFrame struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(reply), &replyFrame); err != nil || replyFrame.ID != "viewer-steer-1" || replyFrame.Error != nil {
		t.Fatalf("bad steer reply: %s", reply)
	}

	// The run connection saw the steer with the relayed params verbatim.
	deadline := time.Now().Add(2 * time.Second)
	for {
		var found bool
		for _, f := range g.framesOn(1) {
			if strings.Contains(f, "_goose/unstable/session/steer") &&
				strings.Contains(f, `"expectedRunId":"run_abc"`) &&
				strings.Contains(f, "migrate the REST layer first") {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("steer never reached the run connection: %v", g.framesOn(1))
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, f := range g.framesOn(2) {
		if strings.Contains(f, "steer") {
			t.Fatalf("steer leaked into the viewer's private pipe: %s", f)
		}
	}
}

func TestViewerSteerDisabledFailsFast(t *testing.T) {
	g, _, s := startTee(t, Config{SteerEnabled: false})

	v, err := dialViewer(t, s, testKey)
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	steer := `{"jsonrpc":"2.0","id":42,"method":"_goose/unstable/session/steer","params":{"sessionId":"run-s1","expectedRunId":"run_abc","prompt":[{"type":"text","text":"x"}]}}`
	if err := v.conn.WriteMessage(websocket.TextMessage, []byte(steer)); err != nil {
		t.Fatalf("steer write: %v", err)
	}
	v.expect("steer policy error", func(f string) bool {
		return strings.Contains(f, `"id":42`) && strings.Contains(f, "disabled by harness policy")
	})

	// Consumed, not forwarded: neither connection may see it.
	time.Sleep(100 * time.Millisecond)
	for n := 1; n <= 2; n++ {
		for _, f := range g.framesOn(n) {
			if strings.Contains(f, "session/steer") {
				t.Fatalf("disabled steer reached goose conn %d: %s", n, f)
			}
		}
	}
}

func TestViewerSteerOtherSessionPassesThrough(t *testing.T) {
	g, _, s := startTee(t, Config{SteerEnabled: true})

	v, err := dialViewer(t, s, testKey)
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	// Steering a session that is NOT the run session is the viewer's own
	// business with its private goose agent — verbatim pipe.
	steer := `{"jsonrpc":"2.0","id":7,"method":"_goose/unstable/session/steer","params":{"sessionId":"viewer-own-session","expectedRunId":"run_x","prompt":[{"type":"text","text":"y"}]}}`
	if err := v.conn.WriteMessage(websocket.TextMessage, []byte(steer)); err != nil {
		t.Fatalf("steer write: %v", err)
	}
	v.expect("own-session steer echo", func(f string) bool {
		return strings.Contains(f, `"id":7`) && strings.Contains(f, `"echo":"_goose/unstable/session/steer"`)
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		frames := g.framesOn(2)
		if len(frames) > 0 && strings.Contains(frames[0], "viewer-own-session") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("own-session steer should pipe to the viewer's goose conn, saw %v", frames)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestViewerCancelRelayedToRunConnection(t *testing.T) {
	g, _, s := startTee(t, Config{SteerEnabled: true})

	v, err := dialViewer(t, s, testKey)
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	cancel := `{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"run-s1"}}`
	if err := v.conn.WriteMessage(websocket.TextMessage, []byte(cancel)); err != nil {
		t.Fatalf("cancel write: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var found bool
		for _, f := range g.framesOn(1) {
			if strings.Contains(f, "session/cancel") && strings.Contains(f, "run-s1") {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancel never reached the run connection: %v", g.framesOn(1))
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, f := range g.framesOn(2) {
		if strings.Contains(f, "session/cancel") {
			t.Fatalf("run-session cancel leaked into the viewer's pipe: %s", f)
		}
	}
}

func TestViewerPromptOnRunSessionGatedWhileActive(t *testing.T) {
	g, _, s := startTee(t, Config{SteerEnabled: true})

	v, err := dialViewer(t, s, testKey)
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	// While the harness prompt is in flight, a viewer prompt on the run
	// session is rejected with goose's own steer guidance. (Numeric ids:
	// fakeGoose's echo only parses those; the tee itself accepts any id
	// shape, which the steer tests cover with string ids.)
	s.SetRunActive(true)
	prompt := `{"jsonrpc":"2.0","id":11,"method":"session/prompt","params":{"sessionId":"run-s1","prompt":[{"type":"text","text":"do something else"}]}}`
	if err := v.conn.WriteMessage(websocket.TextMessage, []byte(prompt)); err != nil {
		t.Fatalf("prompt write: %v", err)
	}
	v.expect("prompt gate error", func(f string) bool {
		return strings.Contains(f, `"id":11`) && strings.Contains(f, "use _goose/unstable/session/steer")
	})

	// After the run finishes it's goose's lazy session activation —
	// verbatim passthrough to the viewer's own connection.
	s.SetRunActive(false)
	prompt2 := `{"jsonrpc":"2.0","id":12,"method":"session/prompt","params":{"sessionId":"run-s1","prompt":[{"type":"text","text":"follow-up"}]}}`
	if err := v.conn.WriteMessage(websocket.TextMessage, []byte(prompt2)); err != nil {
		t.Fatalf("prompt2 write: %v", err)
	}
	v.expect("post-run prompt echo", func(f string) bool {
		return strings.Contains(f, `"id":12`) && strings.Contains(f, `"echo":"session/prompt"`)
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		var sawGated, sawAllowed bool
		for _, f := range g.framesOn(2) {
			if strings.Contains(f, "do something else") {
				sawGated = true
			}
			if strings.Contains(f, "follow-up") {
				sawAllowed = true
			}
		}
		if sawAllowed {
			if sawGated {
				t.Fatal("gated prompt leaked to the viewer's goose conn")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("post-run prompt never piped upstream: %v", g.framesOn(2))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHarnessStatusEmitAndReplay(t *testing.T) {
	_, _, s := startTee(t, Config{})

	// Emitted before anyone is attached — the replay ring holds it.
	s.EmitRunUpdate(map[string]any{
		"sessionUpdate": "plan",
		"entries": []map[string]any{
			{"content": "Prepare workspace", "priority": "medium", "status": "completed"},
		},
	})
	s.EmitRunNotice("stage running")

	v, err := dialViewer(t, s, testKey)
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	// A late viewer catches up in order: plan first, then the notice.
	plan := v.expect("replayed plan", func(f string) bool { return strings.Contains(f, `"sessionUpdate":"plan"`) })
	if !strings.Contains(plan, `"sessionId":"run-s1"`) || !strings.Contains(plan, "Prepare workspace") {
		t.Fatalf("bad replayed plan frame: %s", plan)
	}
	notice := v.expect("replayed notice", func(f string) bool { return strings.Contains(f, "status_message") })
	if !strings.Contains(notice, "_goose/unstable/session/update") || !strings.Contains(notice, "stage running") {
		t.Fatalf("bad replayed notice frame: %s", notice)
	}

	// Live emission after attach reaches the viewer too.
	s.EmitRunUpdate(map[string]any{
		"sessionUpdate": "tool_call", "toolCallId": "harness-push-1",
		"title": "git push (final)", "kind": "execute", "status": "in_progress",
	})
	v.expect("live tool_call", func(f string) bool { return strings.Contains(f, "harness-push-1") })
}

func TestUpstreamDialCarriesHeaderNotURL(t *testing.T) {
	g, _, s := startTee(t, Config{})

	if _, err := dialViewer(t, s, testKey); err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	// The viewer's upstream dial (2nd accepted conn) must authenticate
	// via the X-Secret-Key header — never a ?token= URL parameter that a
	// dial error could echo into logs.
	deadline := time.Now().Add(2 * time.Second)
	for {
		g.mu.Lock()
		n := len(g.dialHeaders)
		var hdr string
		if n >= 2 {
			hdr = g.dialHeaders[1]
		}
		g.mu.Unlock()
		if n >= 2 {
			if hdr != testKey {
				t.Fatalf("upstream dial missing X-Secret-Key header (got %q)", hdr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("viewer upstream dial never reached fake goose")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if strings.Contains(s.upstream, "token=") {
		t.Fatalf("upstream URL still carries the key: %s", s.upstream)
	}
}

func TestEmptySecretKeyRejectsEverything(t *testing.T) {
	s := New(Config{UpstreamAddr: "127.0.0.1:1"})
	if err := s.Start(0); err != nil {
		t.Fatalf("start tee: %v", err)
	}
	t.Cleanup(s.Stop)

	// No configured key must mean nothing attaches — including clients
	// presenting empty or non-empty credentials.
	for _, token := range []string{"", "anything"} {
		u := fmt.Sprintf("ws://%s/acp?token=%s", s.Addr(), url.QueryEscape(token))
		if conn, _, err := websocket.DefaultDialer.Dial(u, nil); err == nil {
			conn.Close()
			t.Fatalf("empty-key server accepted token %q", token)
		}
	}
	h := http.Header{}
	h.Set("X-Secret-Key", "")
	if conn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://%s/acp", s.Addr()), h); err == nil {
		conn.Close()
		t.Fatal("empty-key server accepted empty header")
	}
}

func TestForwardPermissionUnblocksOnStop(t *testing.T) {
	_, _, s := startTee(t, Config{HITLTimeout: time.Hour})

	if _, err := dialViewer(t, s, testKey); err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	// An ask parked on a viewer answer must fail closed the moment the
	// tee shuts down — not after its (here: one hour) timeout.
	done := make(chan acp.PermissionForwardOutcome, 1)
	go func() {
		_, outcome := s.ForwardPermission(json.RawMessage(`{"toolCall":{"title":"x"},"options":[]}`))
		done <- outcome
	}()
	// Wait until the ask is registered before stopping.
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		n := len(s.perms)
		s.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ask never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Stop()

	select {
	case outcome := <-done:
		if outcome != acp.ForwardNoViewers {
			t.Fatalf("expected fail-closed NoViewers on shutdown, got %v", outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ForwardPermission still parked after Stop")
	}
}

// The drop policy is a unit property of viewer.enqueue: a full queue
// closes the viewer instead of ever blocking the caller.
func TestSlowViewerDropped(t *testing.T) {
	v := &viewer{out: make(chan outFrame, 2), closed: make(chan struct{})}

	v.enqueue(outFrame{websocket.TextMessage, []byte("1")})
	v.enqueue(outFrame{websocket.TextMessage, []byte("2")})

	select {
	case <-v.closed:
		t.Fatal("viewer dropped before queue was full")
	default:
	}

	v.enqueue(outFrame{websocket.TextMessage, []byte("3")}) // overflow

	select {
	case <-v.closed:
	default:
		t.Fatal("overflowing viewer was not dropped")
	}

	// Further enqueues are no-ops, not panics.
	v.enqueue(outFrame{websocket.TextMessage, []byte("4")})
}

// The run's terminal frames (final push completed, the finished plan
// ladder, the outcome notice) are emitted microseconds before the
// harness returns and Stop() fires. They must still reach the viewer:
// dropping them leaves the UI showing "git push (final) — in_progress"
// forever, with no way to tell a finished run from a hung one.
func TestTerminalFramesSurviveStop(t *testing.T) {
	_, _, s := startTee(t, Config{})

	v, err := dialViewer(t, s, testKey)
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}

	// Emit the exact tail of a real run, then stop immediately — no
	// sleep, mirroring `defer teeSrv.Stop()` firing right after.
	s.EmitRunUpdate(map[string]any{
		"sessionUpdate": "tool_call_update", "toolCallId": "harness-push-1", "status": "completed",
	})
	s.EmitRunNotice("stage succeeded — results pushed to branch konveyor/x")
	s.Stop()

	var sawCompleted, sawOutcome bool
	deadline := time.After(5 * time.Second)
	for !(sawCompleted && sawOutcome) {
		select {
		case f, ok := <-v.recv:
			if !ok {
				t.Fatalf("socket closed before terminal frames arrived (completed=%v outcome=%v)",
					sawCompleted, sawOutcome)
			}
			if strings.Contains(f, "harness-push-1") && strings.Contains(f, `"status":"completed"`) {
				sawCompleted = true
			}
			if strings.Contains(f, "stage succeeded") {
				sawOutcome = true
			}
		case <-deadline:
			t.Fatalf("terminal frames lost on Stop (completed=%v outcome=%v)", sawCompleted, sawOutcome)
		}
	}
}

// Stop's flush wait must stay bounded in TOTAL, however many viewers are
// attached. A single shared time.After channel delivers exactly once, so
// reusing it across viewers left every wait after the first with no
// deadline — one stalled viewer then held the harness open until the pod
// grace period expired.
func TestStopStaysBoundedWithStalledViewers(t *testing.T) {
	_, _, s := startTee(t, Config{})

	// Two viewers whose writers never signal flushed — the shape of a
	// socket stalled inside WriteMessage.
	stalled := make([]*viewer, 0, 2)
	// Release them however this test exits. On t.Fatal the closes below
	// never run, and the deferred cleanup Stop would then wait on these
	// same viewers — a test that verifies Stop is bounded must not rely
	// on Stop being bounded to clean up after itself.
	defer func() {
		for _, v := range stalled {
			select {
			case <-v.flushed:
			default:
				close(v.flushed)
			}
		}
	}()
	for i := 0; i < 2; i++ {
		v := &viewer{
			out:     make(chan outFrame, 1),
			closed:  make(chan struct{}),
			finish:  make(chan struct{}),
			flushed: make(chan struct{}), // deliberately never closed
		}
		stalled = append(stalled, v)
		s.mu.Lock()
		s.viewers[v] = struct{}{}
		s.mu.Unlock()
	}

	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()

	select {
	case <-done:
	case <-time.After(shutdownFlushTimeout + 5*time.Second):
		t.Fatal("Stop exceeded its flush budget — a later viewer waited with no deadline")
	}
}
