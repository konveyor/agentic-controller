package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/konveyor/migration-harness/internal/logging"
)

// WSClient communicates with goose serve over WebSocket using JSON-RPC 2.0.
//
// A single demux goroutine (readLoop) owns the inbound side of the socket
// and routes each frame by kind:
//
//   - responses go to the per-request channel Call/SendPrompt registered
//     for that id; a response with no registered id is a protocol error
//     and is logged loudly, never silently dropped
//   - notifications fan out to every active sink (in-flight calls collect
//     them parsed) and to every raw subscriber (the ACP tee pipes the
//     wire bytes to attached viewers untouched)
//   - agent-initiated requests (id AND method — e.g.
//     session/request_permission) dispatch to the registered handler;
//     goose parks the turn on the reply with no timeout, so they must
//     always be answered
type WSClient struct {
	conn      *websocket.Conn
	host      string
	port      int
	secretKey string

	writeMu   sync.Mutex
	done      chan struct{}
	closeOnce sync.Once

	mu             sync.Mutex
	pending        map[int64]chan *RPCResponse
	notifSinks     map[int]chan *RPCResponse
	rawSubs        map[int]chan RawNotification
	nextSubID      int
	seq            int64
	onAgentRequest func(*RPCResponse)
}

// RawNotification is a notification frame exactly as it arrived on the
// wire, plus read-time metadata. Frame bytes are shared, not copied —
// subscribers must treat them as read-only.
type RawNotification struct {
	Seq    int64
	Time   time.Time
	Method string
	Frame  []byte
}

// NewWSClient connects to goose serve via WebSocket.
func NewWSClient(host string, port int, secretKey string) (*WSClient, error) {
	u := url.URL{
		Scheme:   "ws",
		Host:     fmt.Sprintf("%s:%d", host, port),
		Path:     "/acp",
		RawQuery: "token=" + url.QueryEscape(secretKey),
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("websocket dial %s: %w", u.Host, err)
	}

	c := &WSClient{
		conn:       conn,
		host:       host,
		port:       port,
		secretKey:  secretKey,
		done:       make(chan struct{}),
		pending:    make(map[int64]chan *RPCResponse),
		notifSinks: make(map[int]chan *RPCResponse),
		rawSubs:    make(map[int]chan RawNotification),
	}

	go c.readLoop()

	return c, nil
}

// readLoop is the demux goroutine: the only reader of the socket.
func (c *WSClient) readLoop() {
	defer close(c.done)
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				logging.Warn("websocket read: %v", err)
			}
			return
		}

		var resp RPCResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			logging.Warn("websocket unmarshal: %v", err)
			continue
		}

		switch {
		case resp.IsNotification():
			c.fanOutNotification(&resp, message)
		case resp.IsAgentRequest():
			c.dispatchAgentRequest(&resp)
		case resp.HasID():
			c.routeResponse(&resp)
		default:
			logging.Warn("ACP frame with neither id nor method — dropping")
		}
	}
}

func (c *WSClient) fanOutNotification(resp *RPCResponse, frame []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	for id, sink := range c.notifSinks {
		select {
		case sink <- resp:
		default:
			logging.Warn("notification sink %d full, dropping %s", id, resp.Method)
		}
	}
	if len(c.rawSubs) == 0 {
		return
	}
	raw := RawNotification{Seq: c.seq, Time: time.Now(), Method: resp.Method, Frame: frame}
	for id, sub := range c.rawSubs {
		select {
		case sub <- raw:
		default:
			logging.Warn("raw subscriber %d full, dropping %s", id, resp.Method)
		}
	}
}

func (c *WSClient) dispatchAgentRequest(resp *RPCResponse) {
	c.mu.Lock()
	handler := c.onAgentRequest
	c.mu.Unlock()

	if handler == nil {
		// No handler registered (bare WSClient use). Fail closed but
		// always reply — an unanswered request parks goose forever.
		logging.Warn("agent request %q with no handler — rejecting", resp.Method)
		if err := c.SendResponse(resp.ID, nil, &RPCError{Code: -32601, Message: "method not supported by harness"}); err != nil {
			logging.Warn("reply to %s: %v", resp.Method, err)
		}
		return
	}

	// Handlers may block (HITL forwarding waits on a human); keep the
	// demux loop free.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logging.Warn("agent request handler panic: %v", r)
				// The request must still be answered — goose parks the
				// turn on the reply with no timeout.
				if err := c.SendResponse(resp.ID, nil, &RPCError{Code: -32603, Message: "internal error in harness handler"}); err != nil {
					logging.Warn("reply to %s after panic: %v", resp.Method, err)
				}
			}
		}()
		handler(resp)
	}()
}

func (c *WSClient) routeResponse(resp *RPCResponse) {
	id, numeric := resp.IntID()
	if !numeric {
		// Our outbound ids are always numeric; a string-id response
		// answers a request we never sent.
		logging.Warn("ACP response with non-numeric id %s — dropping (protocol error)", resp.ID)
		return
	}

	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()

	if !ok {
		logging.Warn("ACP response with unmatched id %d — dropping (protocol error)", id)
		return
	}
	ch <- resp // cap 1, registered before send; never blocks
}

// registerPending reserves a response channel for a request id about to be
// sent. The caller must either receive the response or unregister.
func (c *WSClient) registerPending(id int64) chan *RPCResponse {
	ch := make(chan *RPCResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	return ch
}

func (c *WSClient) unregisterPending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// addNotifSink registers a parsed-notification sink for an in-flight call.
func (c *WSClient) addNotifSink() (int, chan *RPCResponse) {
	ch := make(chan *RPCResponse, 256)
	c.mu.Lock()
	c.nextSubID++
	id := c.nextSubID
	c.notifSinks[id] = ch
	c.mu.Unlock()
	return id, ch
}

func (c *WSClient) removeNotifSink(id int) {
	c.mu.Lock()
	delete(c.notifSinks, id)
	c.mu.Unlock()
}

// SubscribeRawNotifications returns a channel of notification frames as
// raw wire bytes. The ACP tee subscribes here to fan the run's stream out
// to attached viewers. On overflow frames are dropped (with a warning) —
// the run path never blocks on a subscriber. The returned cancel func
// unsubscribes and closes the channel.
func (c *WSClient) SubscribeRawNotifications(buffer int) (<-chan RawNotification, func()) {
	ch := make(chan RawNotification, buffer)
	c.mu.Lock()
	c.nextSubID++
	id := c.nextSubID
	c.rawSubs[id] = ch
	c.mu.Unlock()

	cancel := func() {
		c.mu.Lock()
		if _, ok := c.rawSubs[id]; ok {
			delete(c.rawSubs, id)
			close(ch)
		}
		c.mu.Unlock()
	}
	return ch, cancel
}

// SetAgentRequestHandler installs the handler for agent-initiated requests
// (session/request_permission, elicitation/create, fs/*). The handler runs
// on its own goroutine per request and MUST reply via SendResponse — goose
// blocks the turn on the reply with no timeout.
func (c *WSClient) SetAgentRequestHandler(fn func(*RPCResponse)) {
	c.mu.Lock()
	c.onAgentRequest = fn
	c.mu.Unlock()
}

// Notify sends a JSON-RPC notification (no id, no response expected).
func (c *WSClient) Notify(method string, params any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	data, err := json.Marshal(&struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// Send sends a JSON-RPC request over the WebSocket.
func (c *WSClient) Send(req *Request) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// SendResponse sends a JSON-RPC response for an agent-initiated request,
// echoing the request's id bytes exactly (goose uses string ids for its
// agent→client requests). Exactly one of result and rpcErr should be set.
func (c *WSClient) SendResponse(id json.RawMessage, result any, rpcErr *RPCError) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	data, err := json.Marshal(&Response{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr})
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// Call sends a JSON-RPC request and waits for the matching response.
// Returns the response and any notifications received while waiting.
func (c *WSClient) Call(ctx context.Context, method string, params any) (json.RawMessage, []*RPCResponse, error) {
	req := newRequest(method, params)

	respCh := c.registerPending(req.ID)
	defer c.unregisterPending(req.ID)
	sinkID, notifCh := c.addNotifSink()
	defer c.removeNotifSink(sinkID)

	if err := c.Send(req); err != nil {
		return nil, nil, fmt.Errorf("send %s: %w", method, err)
	}

	var notifications []*RPCResponse

	for {
		select {
		case <-ctx.Done():
			return nil, notifications, ctx.Err()
		case <-c.done:
			// readLoop exited. The response may already be routed to
			// respCh — drain it before giving up.
			select {
			case msg := <-respCh:
				notifications = append(notifications, drainNotifications(notifCh)...)
				if msg.Error != nil {
					return nil, notifications, fmt.Errorf("ACP error %d: %w", msg.Error.Code, msg.Error)
				}
				return msg.Result, notifications, nil
			default:
				return nil, notifications, fmt.Errorf("websocket connection closed")
			}
		case n := <-notifCh:
			notifications = append(notifications, n)
		case msg := <-respCh:
			// Drain notifications that arrived before the response but
			// are still buffered — session/new's sessionId can ride one.
			notifications = append(notifications, drainNotifications(notifCh)...)
			if msg.Error != nil {
				return nil, notifications, fmt.Errorf("ACP error %d: %w", msg.Error.Code, msg.Error)
			}
			return msg.Result, notifications, nil
		}
	}
}

// CallRPC sends a request and waits for the matching response, returning
// the agent's error object intact instead of flattening it into a Go
// error. The ACP tee relays viewer requests through here so goose's error
// (code, message, data) reaches the viewer verbatim.
func (c *WSClient) CallRPC(ctx context.Context, method string, params any) (json.RawMessage, *RPCError, error) {
	req := newRequest(method, params)

	respCh := c.registerPending(req.ID)
	defer c.unregisterPending(req.ID)

	if err := c.Send(req); err != nil {
		return nil, nil, fmt.Errorf("send %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-c.done:
		return nil, nil, fmt.Errorf("websocket connection closed")
	case msg := <-respCh:
		return msg.Result, msg.Error, nil
	}
}

func drainNotifications(ch <-chan *RPCResponse) []*RPCResponse {
	var out []*RPCResponse
	for {
		select {
		case n := <-ch:
			out = append(out, n)
		default:
			return out
		}
	}
}

// WaitReadyDial attempts to connect to goose serve with retries.
func WaitReadyDial(ctx context.Context, host string, port int, secretKey string, timeout time.Duration) (*WSClient, error) {
	deadline := time.Now().Add(timeout)
	delay := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		client, err := NewWSClient(host, port, secretKey)
		if err == nil {
			return client, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}

		if delay < 4*time.Second {
			delay *= 2
		}
	}

	return nil, fmt.Errorf("goose serve not ready on %s:%d after %v", host, port, timeout)
}

// Close closes the WebSocket connection.
func (c *WSClient) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.writeMu.Lock()
		err = c.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		c.writeMu.Unlock()
		c.conn.Close()
	})
	return err
}

// Done returns a channel that is closed when the connection drops.
func (c *WSClient) Done() <-chan struct{} {
	return c.done
}
