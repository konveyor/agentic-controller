// Package tee makes the harness the pod's ACP endpoint on the platform
// port (:4000) and makes the run observable while it executes.
//
// Topology:
//
//	before:  viewer ──(hub WS pipe)──▶ pod:4000 = goose  ◀── harness (ACP client)
//	after:   viewer ──(hub WS pipe)──▶ pod:4000 = harness ──▶ 127.0.0.1:4001 = goose
//
// goose gives every WebSocket connection a private agent with no
// cross-connection fan-out, so a viewer dialing goose directly can never
// see the run's session. The tee fixes that without inventing protocol:
//
//   - each attached client gets a dumb per-connection pipe to goose on
//     loopback — frames pass verbatim in both directions, so interactive
//     chat (the client driving its own session) works exactly as before
//   - the run connection's session/update notifications are teed into
//     every attached client unmodified; they are notifications (no id),
//     so they cannot collide with the client's own request/response pairs
//   - the harness reports its own run lifecycle (stage ladder, git
//     pushes, outcome) as synthetic session/update frames on the run's
//     sessionId, using standard ACP vocabulary (plan, tool_call); a small
//     replay buffer catches late-attaching viewers up
//   - a session/request_permission ask on the run connection is offered
//     to attached viewers under a harness-allocated "kperm-<n>" string id
//     (disjoint from the pipe's verbatim numeric ids); the viewer's
//     answer is intercepted and relayed to goose
//   - redirection: a viewer frame naming the RUN session is routed onto
//     the run connection instead of the viewer's private pipe —
//     `_goose/unstable/session/steer` (inject guidance into the active
//     turn) and `session/cancel` (stop it). goose scopes an active run to
//     the connection that started it, so relaying through the run
//     connection is the only place these can land. A viewer
//     session/prompt on the run session is rejected while the run is
//     active (two connections prompting one session corrupts its
//     history); goose's own error text points clients at steer.
//
// The tee must never affect the run: per-connection goroutines recover
// panics, per-client buffers are bounded and a slow client is dropped (it
// can reconnect), and a listener failure downgrades to a warning.
package tee

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/konveyor/migration-harness/internal/acp"
	"github.com/konveyor/migration-harness/internal/logging"
)

const (
	// DefaultHITLTimeout is how long a permission ask waits for an
	// attached viewer before the harness falls back to its headless
	// policy. goose parks the turn on the reply with no timeout of its
	// own, so this is the only clock on the ask.
	DefaultHITLTimeout = 3 * time.Minute

	// DefaultQueueCap bounds each viewer's outbound frame queue. A viewer
	// that cannot drain this many frames is dropped rather than ever
	// back-pressuring the run.
	DefaultQueueCap = 256

	// rawSubscriberBuffer sizes the tee's intake from the run
	// connection's notification stream.
	rawSubscriberBuffer = 1024

	// replayCap bounds the harness-emitted status frames kept for
	// late-attaching viewers. Harness frames are sparse full-state
	// snapshots (plan ladder, push tool calls, outcome), so a small ring
	// is a complete catch-up in practice.
	replayCap = 32

	// relayTimeout bounds a viewer request relayed onto the run
	// connection (steer). goose answers these immediately — steer only
	// queues a message — so a slow answer means a broken run connection.
	relayTimeout = 10 * time.Second

	// gooseUpdateMethod is goose's custom notification channel
	// (usage_update, status_message), enabled by the harness's
	// customNotifications client capability and teed like session/update.
	gooseUpdateMethod = "_goose/unstable/session/update"

	// steerMethod injects user input into a session's active turn.
	steerMethod = "_goose/unstable/session/steer"

	// pingInterval / pongWait keep half-open viewer sockets from holding
	// their goose connection forever: the writer pings, the read deadline
	// extends on pong, and a silent peer times the read loop out.
	pingInterval = 30 * time.Second
	pongWait     = 90 * time.Second

	// shutdownFlushTimeout bounds how long Stop waits for viewers to
	// drain their queued frames. It runs at process exit, well inside the
	// pod's termination grace period.
	shutdownFlushTimeout = 2 * time.Second
)

// Config for the tee server. SecretKey and UpstreamAddr are required.
type Config struct {
	// SecretKey authenticates viewers (X-Secret-Key header or ?token=
	// query — the same carriers goose accepts) and is presented upstream
	// when dialing goose.
	SecretKey string
	// UpstreamAddr is goose serve's loopback host:port.
	UpstreamAddr string
	// HITLTimeout overrides DefaultHITLTimeout when > 0.
	HITLTimeout time.Duration
	// QueueCap overrides DefaultQueueCap when > 0.
	QueueCap int
	// SteerEnabled lets attached viewers redirect the run session:
	// steer and cancel frames naming the run session relay onto the run
	// connection. Off makes the run stream watch-only (permission asks
	// still forward).
	SteerEnabled bool
}

// Server is the pod-facing ACP endpoint: auth, pipe, tee, HITL relay.
type Server struct {
	cfg      Config
	ln       net.Listener
	httpSrv  *http.Server
	upstream string // ws URL to goose

	mu      sync.Mutex
	viewers map[*viewer]struct{}
	perms   map[string]chan json.RawMessage
	stopped bool

	// done is closed by Stop so anything parked on a viewer answer
	// (ForwardPermission) fails closed immediately instead of waiting
	// out its timeout during shutdown.
	done     chan struct{}
	stopOnce sync.Once

	// responsive tracks whether attached viewers show signs of a human:
	// set on attach and on any kperm answer, cleared when an ask times
	// out. While false, permission asks resolve NoViewers immediately —
	// one slow timeout, then fast fail-closed denies instead of parking
	// every retried ask for the full window.
	responsive atomic.Bool

	permSeq     atomic.Int64
	unsubscribe func()

	// Run-session wiring, set by AttachRun. runWS is the harness's own
	// connection to goose — the one whose active run steer/cancel must
	// land on. runActive mirrors whether the harness prompt is in flight
	// (SetRunActive), gating viewer prompts against the run session.
	runMu        sync.Mutex
	runWS        *acp.WSClient
	runSessionID string
	runActive    atomic.Bool

	// replay holds the harness's own emitted status frames for viewers
	// that attach later. Guarded by mu.
	replay [][]byte
}

// New creates a tee server. Call Start to listen and AttachRun to begin
// teeing the run connection's stream.
func New(cfg Config) *Server {
	if cfg.HITLTimeout <= 0 {
		cfg.HITLTimeout = DefaultHITLTimeout
	}
	if cfg.QueueCap <= 0 {
		cfg.QueueCap = DefaultQueueCap
	}
	return &Server{
		cfg: cfg,
		// No credential in the URL: the key rides the X-Secret-Key
		// header on dial, so an error that echoes the URL cannot leak it
		// into container logs.
		upstream: fmt.Sprintf("ws://%s/acp", cfg.UpstreamAddr),
		viewers:  make(map[*viewer]struct{}),
		perms:    make(map[string]chan json.RawMessage),
		done:     make(chan struct{}),
	}
}

// Start listens on the given port (0 picks an ephemeral port, for tests).
// Failure is returned, not fatal — the caller downgrades it to a warning;
// the run proceeds without live viewers.
func (s *Server) Start(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("tee listen :%d: %w", port, err)
	}
	s.ln = ln

	mux := http.NewServeMux()
	// Unauthenticated liveness endpoint (ADR 0004 contract).
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/acp", s.handleACP)

	s.httpSrv = &http.Server{Handler: mux}
	go func() {
		defer recoverWarn("tee http server")
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logging.Warn("tee server: %v", err)
		}
	}()
	return nil
}

// Addr returns the bound listener address (useful with port 0).
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Stop closes the listener and disconnects all viewers. Pending
// permission asks fail closed immediately via the done channel.
func (s *Server) Stop() {
	s.stopOnce.Do(func() { close(s.done) })
	s.mu.Lock()
	s.stopped = true
	views := make([]*viewer, 0, len(s.viewers))
	for v := range s.viewers {
		views = append(views, v)
	}
	s.mu.Unlock()

	if s.unsubscribe != nil {
		s.unsubscribe()
	}
	if s.httpSrv != nil {
		s.httpSrv.Close()
	}

	// Graceful close: let each writer drain what is already queued before
	// the socket goes away. stopped is set above, so nothing new can be
	// enqueued and every writer terminates. Bounded in total — a viewer
	// that cannot drain in time is dropped, exactly as before.
	for _, v := range views {
		v.beginFinish()
	}
	// Absolute expiry with a fresh timer per viewer: a single time.After
	// channel delivers exactly once, so reusing it would leave every wait
	// after the first with no deadline at all. An already-expired timer
	// fires immediately, keeping the TOTAL wait bounded.
	expiry := time.Now().Add(shutdownFlushTimeout)
	for _, v := range views {
		if v.flushed == nil {
			continue
		}
		t := time.NewTimer(time.Until(expiry))
		select {
		case <-v.flushed:
		case <-t.C:
			logging.Warn("tee: viewer did not drain before shutdown — closing anyway")
		}
		t.Stop()
	}

	for _, v := range views {
		v.shutdown(websocket.CloseGoingAway, "harness shutting down")
	}
}

// AttachRun subscribes to the run connection's notification stream and
// tees session/update frames (standard and goose-custom) to every
// attached viewer. runSessionID names the run's session so viewer frames
// targeting it can be routed onto this connection (steer, cancel) instead
// of the viewer's private pipe.
func (s *Server) AttachRun(ws *acp.WSClient, runSessionID string) {
	s.runMu.Lock()
	s.runWS = ws
	s.runSessionID = runSessionID
	s.runMu.Unlock()

	ch, cancel := ws.SubscribeRawNotifications(rawSubscriberBuffer)
	s.unsubscribe = cancel
	go func() {
		defer recoverWarn("tee broadcast")
		for n := range ch {
			if n.Method != "session/update" && n.Method != gooseUpdateMethod {
				continue
			}
			s.broadcast(n.Frame)
		}
	}()
}

// run returns the current run connection and session id.
func (s *Server) run() (*acp.WSClient, string) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.runWS, s.runSessionID
}

// SetRunActive records whether the harness's own prompt is in flight.
// While active, a viewer session/prompt against the run session is
// rejected with the same guidance goose gives for a concurrent prompt on
// one connection: use steer.
func (s *Server) SetRunActive(active bool) {
	s.runActive.Store(active)
}

// EmitRunUpdate broadcasts a harness-authored session/update for the run
// session — standard ACP vocabulary (plan, tool_call, tool_call_update)
// describing harness lifecycle the goose stream cannot see: workspace
// prep, watcher/final git pushes, stage outcome. The frame also enters
// the replay ring so late viewers catch up.
func (s *Server) EmitRunUpdate(update any) {
	s.emitRunFrame("session/update", update)
}

// EmitRunNotice broadcasts a stage-level status line using goose's own
// custom status_message vocabulary (`_goose/unstable/session/update`), so
// a client that renders goose notices renders harness notices too.
func (s *Server) EmitRunNotice(message string) {
	s.emitRunFrame(gooseUpdateMethod, map[string]any{
		"sessionUpdate": "status_message",
		"status":        map[string]any{"type": "notice", "message": message},
	})
}

func (s *Server) emitRunFrame(method string, update any) {
	_, sessionID := s.run()
	if sessionID == "" {
		return
	}
	frame, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  map[string]any{"sessionId": sessionID, "update": update},
	})
	if err != nil {
		logging.Warn("tee: marshal run update: %v", err)
		return
	}

	s.mu.Lock()
	s.replay = append(s.replay, frame)
	if len(s.replay) > replayCap {
		s.replay = s.replay[len(s.replay)-replayCap:]
	}
	s.mu.Unlock()

	s.broadcast(frame)
}

// ForwardPermission implements acp.PermissionForwarder: broadcast the ask
// to every attached viewer under a kperm-<n> id, first answer wins.
func (s *Server) ForwardPermission(params json.RawMessage) (json.RawMessage, acp.PermissionForwardOutcome) {
	id := fmt.Sprintf("kperm-%d", s.permSeq.Add(1))
	frame, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "session/request_permission",
		"params":  params,
	})
	if err != nil {
		logging.Warn("tee: marshal permission forward: %v", err)
		return nil, acp.ForwardNoViewers
	}

	ch := make(chan json.RawMessage, 1)
	s.mu.Lock()
	if s.stopped || len(s.viewers) == 0 {
		s.mu.Unlock()
		return nil, acp.ForwardNoViewers
	}
	if !s.responsive.Load() {
		// Viewers are attached but a previous ask already timed out and
		// nothing human has happened since — don't park this ask for
		// another full window.
		s.mu.Unlock()
		logging.Info("tee: permission ask %s — viewers unresponsive, fast fail-closed", id)
		return nil, acp.ForwardNoViewers
	}
	s.perms[id] = ch
	s.mu.Unlock()

	targets := s.snapshotViewers()
	for _, v := range targets {
		v.enqueue(outFrame{websocket.TextMessage, frame})
	}
	n := len(targets)

	defer func() {
		s.mu.Lock()
		delete(s.perms, id)
		s.mu.Unlock()
	}()

	logging.Info("tee: permission ask %s offered to %d viewer(s)", id, n)
	// Explicit timer, not time.After: an answered ask must release its
	// timer immediately rather than pinning it for the full window.
	timer := time.NewTimer(s.cfg.HITLTimeout)
	defer timer.Stop()
	select {
	case result := <-ch:
		return result, acp.ForwardAnswered
	case <-s.done:
		return nil, acp.ForwardNoViewers
	case <-timer.C:
		s.responsive.Store(false)
		return nil, acp.ForwardTimeout
	}
}

// ---------------------------------------------------------------- viewers

type outFrame struct {
	messageType int
	data        []byte
}

type viewer struct {
	conn   *websocket.Conn
	out    chan outFrame
	closed chan struct{}
	// finish asks the writer to drain everything already queued and then
	// stop, instead of dropping it. flushed is closed by the writer when
	// it has done so (or given up).
	finish     chan struct{}
	flushed    chan struct{}
	stopOnce   sync.Once
	finishOnce sync.Once
}

// beginFinish starts a graceful close: no more frames will be queued, so
// the writer drains what is left. Safe to call repeatedly.
func (v *viewer) beginFinish() {
	v.finishOnce.Do(func() {
		if v.finish != nil {
			close(v.finish)
		}
	})
}

// enqueue queues a frame for the viewer's writer goroutine. On a full
// queue the viewer is dropped — never block, never affect the run.
func (v *viewer) enqueue(f outFrame) {
	select {
	case v.out <- f:
	case <-v.closed:
	default:
		logging.Warn("tee: viewer send queue full — dropping viewer")
		v.shutdown(websocket.ClosePolicyViolation, "too slow — reconnect to resume")
	}
}

func (v *viewer) shutdown(code int, reason string) {
	v.stopOnce.Do(func() {
		close(v.closed)
		if v.conn == nil {
			return
		}
		deadline := time.Now().Add(2 * time.Second)
		msg := websocket.FormatCloseMessage(code, reason)
		_ = v.conn.WriteControl(websocket.CloseMessage, msg, deadline)
		_ = v.conn.Close()
	})
}

var upgrader = websocket.Upgrader{
	// Auth is the shared secret; the shim (not a browser origin) is the
	// expected peer, so origin checks carry no signal here.
	CheckOrigin: func(*http.Request) bool { return true },
}

func (s *Server) authorized(r *http.Request) bool {
	// Defense in depth: with no key configured nothing may attach. (The
	// carrier guards below already refuse empty credentials, so an empty
	// key could never match anyway — this makes the invariant explicit.)
	if s.cfg.SecretKey == "" {
		return false
	}
	key := []byte(s.cfg.SecretKey)
	if h := r.Header.Get("X-Secret-Key"); h != "" {
		return subtle.ConstantTimeCompare([]byte(h), key) == 1
	}
	if t := r.URL.Query().Get("token"); t != "" {
		return subtle.ConstantTimeCompare([]byte(t), key) == 1
	}
	return false
}

func (s *Server) handleACP(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.Warn("tee: upgrade: %v", err)
		return
	}
	go s.serveViewer(conn)
}

func (s *Server) serveViewer(client *websocket.Conn) {
	defer recoverWarn("tee viewer")

	v := &viewer{
		conn:    client,
		out:     make(chan outFrame, s.cfg.QueueCap),
		closed:  make(chan struct{}),
		finish:  make(chan struct{}),
		flushed: make(chan struct{}),
	}

	// Catch-up before registration: replay the harness's own status
	// frames (plan ladder, pushes, outcome) so a late viewer sees where
	// the run stands. Enqueue-then-register keeps replay ordered before
	// live broadcasts; a frame emitted in the microseconds between
	// snapshot and register is lost, which is acceptable for full-state
	// snapshots (never for the chat stream, which goose replays itself
	// via session/load).
	s.mu.Lock()
	backlog := append([][]byte(nil), s.replay...)
	s.mu.Unlock()
	for _, f := range backlog {
		v.enqueue(outFrame{websocket.TextMessage, f})
	}

	// Register before dialing goose: teed frames and permission asks
	// must reach a viewer from the moment its socket is accepted — they
	// queue in v.out while the upstream pipe comes up.
	if !s.addViewer(v) {
		v.shutdown(websocket.CloseGoingAway, "harness shutting down")
		return
	}
	logging.Info("tee: viewer attached")
	defer func() {
		s.removeViewer(v)
		v.shutdown(websocket.CloseNormalClosure, "")
		logging.Info("tee: viewer detached")
	}()

	// Keepalive: ping from the writer, extend the read deadline on pong.
	// Without it a half-open viewer (proxy died, no close handshake)
	// would hold its goose connection for the rest of the run.
	_ = client.SetReadDeadline(time.Now().Add(pongWait))
	client.SetPongHandler(func(string) error {
		return client.SetReadDeadline(time.Now().Add(pongWait))
	})

	// Writer: the sole writer to the client socket — pipe frames and
	// teed frames are serialized through v.out.
	go func() {
		defer recoverWarn("tee viewer writer")
		defer close(v.flushed)
		ping := time.NewTicker(pingInterval)
		defer ping.Stop()
		write := func(f outFrame) bool {
			if err := client.WriteMessage(f.messageType, f.data); err != nil {
				v.shutdown(websocket.CloseInternalServerErr, "write failed")
				return false
			}
			return true
		}
		for {
			select {
			case <-v.closed:
				return
			case <-v.finish:
				// The run's terminal frames — final push status, the
				// finished plan ladder, the outcome notice — are queued
				// microseconds before the harness exits. Drain them, or
				// the viewer is left staring at an in_progress tool call
				// with no way to tell a finished run from a hung one.
				//
				// Bound the drain itself: WriteMessage has no deadline of
				// its own, so a stalled socket would otherwise keep this
				// goroutine (and the flushed signal) alive indefinitely.
				_ = client.SetWriteDeadline(time.Now().Add(shutdownFlushTimeout))
				for {
					select {
					case f := <-v.out:
						if !write(f) {
							return
						}
					default:
						return
					}
				}
			case <-ping.C:
				deadline := time.Now().Add(5 * time.Second)
				if err := client.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
					v.shutdown(websocket.CloseInternalServerErr, "ping failed")
					return
				}
			case f := <-v.out:
				if !write(f) {
					return
				}
			}
		}
	}()

	// Every client gets its own upstream goose connection — goose's
	// per-connection agent keeps interactive chat isolated, exactly as
	// when goose owned the port. The key travels as a header, never in
	// the URL, so dial errors cannot leak it.
	hdr := http.Header{}
	hdr.Set("X-Secret-Key", s.cfg.SecretKey)
	upstream, _, err := websocket.DefaultDialer.Dial(s.upstream, hdr)
	if err != nil {
		logging.Warn("tee: upstream dial to %s: %v", s.cfg.UpstreamAddr, err)
		v.shutdown(websocket.CloseInternalServerErr, "agent unavailable")
		return
	}
	defer upstream.Close()

	// Upstream reader: goose → client, verbatim.
	go func() {
		defer recoverWarn("tee upstream reader")
		for {
			mt, data, err := upstream.ReadMessage()
			if err != nil {
				v.shutdown(websocket.CloseNormalClosure, "agent connection closed")
				return
			}
			v.enqueue(outFrame{mt, data})
		}
	}()

	// Client reader: client → goose, verbatim — except frames that belong
	// to the run connection, not this pipe: answers to kperm-* asks, and
	// steer/cancel/prompt frames naming the run session.
	for {
		mt, data, err := client.ReadMessage()
		if err != nil {
			return
		}
		if id, result, ok := kpermAnswer(data); ok {
			s.resolvePermission(id, result)
			continue
		}
		if s.interceptRunFrame(v, data) {
			continue
		}
		if err := upstream.WriteMessage(mt, data); err != nil {
			return
		}
	}
}

// interceptRunFrame handles a viewer frame that names the run session and
// must not travel down the viewer's private pipe (whose goose agent has no
// active run and would corrupt or reject it). Reports whether the frame
// was consumed.
func (s *Server) interceptRunFrame(v *viewer, data []byte) bool {
	ws, runSession := s.run()
	if ws == nil || runSession == "" {
		return false
	}

	var frame struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &frame); err != nil || frame.Method == "" {
		return false
	}
	var sid struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(frame.Params, &sid); err != nil || sid.SessionID != runSession {
		return false
	}

	switch frame.Method {
	case steerMethod:
		if len(frame.ID) == 0 {
			return false // steer is a request; a malformed id-less one is inert either way
		}
		if !s.cfg.SteerEnabled {
			s.respond(v, frame.ID, nil, &acp.RPCError{
				Code:    -32601,
				Message: "viewer steering is disabled by harness policy (HARNESS_HITL_STEER=off)",
			})
			return true
		}
		go s.relaySteer(v, frame.ID, frame.Params)
		return true

	case "session/cancel":
		if !s.cfg.SteerEnabled {
			logging.Warn("tee: viewer cancel for the run session dropped — steering disabled")
			return true
		}
		s.responsive.Store(true)
		go func() {
			defer recoverWarn("tee cancel relay")
			if err := ws.Notify("session/cancel", frame.Params); err != nil {
				logging.Warn("tee: relay cancel: %v", err)
			}
		}()
		logging.Info("tee: viewer cancelled the run session")
		return true

	case "session/prompt":
		if !s.runActive.Load() || len(frame.ID) == 0 {
			// Run finished: chatting with the run session over the
			// viewer's own pipe is goose's lazy-activation feature, not
			// a conflict.
			return false
		}
		// Mirror goose's own concurrent-prompt guidance: two connections
		// prompting one session would interleave its history.
		s.respond(v, frame.ID, nil, &acp.RPCError{
			Code:    -32602,
			Message: fmt.Sprintf("session already has an active run; use %s", steerMethod),
		})
		return true
	}
	return false
}

// relaySteer forwards a viewer's steer onto the run connection and routes
// goose's reply back under the viewer's own request id.
func (s *Server) relaySteer(v *viewer, viewerID, params json.RawMessage) {
	defer recoverWarn("tee steer relay")
	ws, _ := s.run()
	if ws == nil {
		s.respond(v, viewerID, nil, &acp.RPCError{Code: -32603, Message: "run connection unavailable"})
		return
	}

	// A steer attempt is a human at the controls; resume forwarding asks.
	s.responsive.Store(true)

	ctx, cancel := context.WithTimeout(context.Background(), relayTimeout)
	defer cancel()
	result, rpcErr, err := ws.CallRPC(ctx, steerMethod, params)
	switch {
	case err != nil:
		logging.Warn("tee: steer relay: %v", err)
		s.respond(v, viewerID, nil, &acp.RPCError{Code: -32603, Message: fmt.Sprintf("steer relay failed: %v", err)})
	case rpcErr != nil:
		s.respond(v, viewerID, nil, rpcErr)
	default:
		logging.Info("tee: viewer steered the run session")
		s.respond(v, viewerID, result, nil)
	}
}

// respond enqueues a JSON-RPC response to the viewer under its original
// request id (kept as raw bytes — viewers may use string or numeric ids).
func (s *Server) respond(v *viewer, id, result json.RawMessage, rpcErr *acp.RPCError) {
	payload := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		payload["error"] = rpcErr
	} else {
		if len(result) == 0 {
			result = json.RawMessage("null")
		}
		payload["result"] = result
	}
	frame, err := json.Marshal(payload)
	if err != nil {
		logging.Warn("tee: marshal viewer response: %v", err)
		return
	}
	v.enqueue(outFrame{websocket.TextMessage, frame})
}

func (s *Server) addViewer(v *viewer) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	s.viewers[v] = struct{}{}
	// A fresh attach is a sign of a human; resume forwarding asks.
	s.responsive.Store(true)
	return true
}

func (s *Server) removeViewer(v *viewer) {
	s.mu.Lock()
	delete(s.viewers, v)
	s.mu.Unlock()
}

// snapshotViewers copies the attached set under the lock. Callers enqueue
// outside it: enqueue's drop path shuts the viewer down (a WriteControl
// with a deadline) and must never run while the tee's mutex is held. A
// viewer removed in between is harmless — its enqueue is a no-op.
func (s *Server) snapshotViewers() []*viewer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*viewer, 0, len(s.viewers))
	for v := range s.viewers {
		out = append(out, v)
	}
	return out
}

func (s *Server) broadcast(frame []byte) {
	for _, v := range s.snapshotViewers() {
		v.enqueue(outFrame{websocket.TextMessage, frame})
	}
}

// kpermAnswer reports whether the frame is a JSON-RPC *response* to a
// harness-allocated kperm-* permission ask, and extracts its result.
// Error responses are not answers — the ask stays pending for another
// viewer or the timeout.
func kpermAnswer(frame []byte) (string, json.RawMessage, bool) {
	var probe struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(frame, &probe); err != nil {
		return "", nil, false
	}
	if probe.Method != "" || len(probe.ID) == 0 {
		return "", nil, false
	}
	var id string
	if err := json.Unmarshal(probe.ID, &id); err != nil {
		return "", nil, false
	}
	if !strings.HasPrefix(id, "kperm-") {
		return "", nil, false
	}
	if len(probe.Result) == 0 {
		logging.Warn("tee: viewer errored permission ask %s — leaving it pending", id)
		return id, nil, true // consumed (never forwarded upstream), but not resolved
	}
	return id, probe.Result, true
}

func (s *Server) resolvePermission(id string, result json.RawMessage) {
	// Any kperm frame — even a late or error answer — proves a human is
	// at the controls; resume forwarding asks.
	s.responsive.Store(true)
	if result == nil {
		return
	}
	s.mu.Lock()
	ch, ok := s.perms[id]
	if ok {
		delete(s.perms, id)
	}
	s.mu.Unlock()
	if !ok {
		logging.Info("tee: late/duplicate answer for %s — ignoring", id)
		return
	}
	ch <- result // cap 1; sole sender after map removal
}

func recoverWarn(what string) {
	if r := recover(); r != nil {
		logging.Warn("tee: %s panic: %v\n%s", what, r, debug.Stack())
	}
}
