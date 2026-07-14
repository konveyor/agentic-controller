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
type WSClient struct {
	conn      *websocket.Conn
	host      string
	port      int
	secretKey string
	mu        sync.Mutex
	recv      chan *RPCResponse
	done      chan struct{}
	closeOnce sync.Once
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
		conn:      conn,
		host:      host,
		port:      port,
		secretKey: secretKey,
		recv:      make(chan *RPCResponse, 256),
		done:      make(chan struct{}),
	}

	go c.readLoop()

	return c, nil
}

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

		select {
		case c.recv <- &resp:
		default:
			logging.Warn("websocket recv channel full, dropping message")
		}
	}
}

// Send sends a JSON-RPC request over the WebSocket.
func (c *WSClient) Send(req *Request) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// Call sends a JSON-RPC request and waits for the matching response.
// Returns the response and any notifications received while waiting.
func (c *WSClient) Call(ctx context.Context, method string, params any) (json.RawMessage, []*RPCResponse, error) {
	req := newRequest(method, params)
	if err := c.Send(req); err != nil {
		return nil, nil, fmt.Errorf("send %s: %w", method, err)
	}

	var notifications []*RPCResponse

	for {
		select {
		case <-ctx.Done():
			return nil, notifications, ctx.Err()
		case <-c.done:
			return nil, notifications, fmt.Errorf("websocket connection closed")
		case msg := <-c.recv:
			if msg.IsNotification() {
				notifications = append(notifications, msg)
				continue
			}
			if msg.ID != nil && *msg.ID == req.ID {
				if msg.Error != nil {
					return nil, notifications, fmt.Errorf("ACP error %d: %s", msg.Error.Code, msg.Error.Message)
				}
				return msg.Result, notifications, nil
			}
		}
	}
}

// WaitReady polls until goose serve accepts WebSocket connections.
func (c *WSClient) WaitReady(ctx context.Context, timeout time.Duration) error {
	// If we already have a connection, we're ready
	if c.conn != nil {
		return nil
	}
	return fmt.Errorf("no websocket connection")
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
		err = c.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		c.conn.Close()
	})
	return err
}

// Recv returns the receive channel for reading messages directly.
func (c *WSClient) Recv() <-chan *RPCResponse {
	return c.recv
}

// Done returns a channel that is closed when the connection drops.
func (c *WSClient) Done() <-chan struct{} {
	return c.done
}
