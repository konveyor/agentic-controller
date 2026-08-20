package askuser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// mcpClient plays goose: it drives the server over pipes and answers the
// elicitation the ask_user tool raises.
type mcpClient struct {
	t      *testing.T
	w      io.Writer
	frames chan map[string]any
}

func startServer(t *testing.T) (*mcpClient, func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	srv := New(outW, func(format string, args ...any) { t.Logf(format, args...) })
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, inR) }()

	c := &mcpClient{t: t, w: inW, frames: make(chan map[string]any, 16)}
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 64*1024), maxLine)
		for sc.Scan() {
			var f map[string]any
			if err := json.Unmarshal(sc.Bytes(), &f); err != nil {
				t.Errorf("server wrote non-JSON: %s", sc.Text())
				continue
			}
			c.frames <- f
		}
	}()
	stop := func() {
		cancel()
		inW.Close()
		outW.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("server did not stop")
		}
	}
	return c, stop
}

func (c *mcpClient) send(frame string) {
	c.t.Helper()
	if _, err := io.WriteString(c.w, frame+"\n"); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *mcpClient) next() map[string]any {
	c.t.Helper()
	select {
	case f := <-c.frames:
		return f
	case <-time.After(3 * time.Second):
		c.t.Fatal("timed out waiting for a server frame")
		return nil
	}
}

func TestInitializeListAndAskRoundTrip(t *testing.T) {
	c, stop := startServer(t)
	defer stop()

	c.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"elicitation":{}},"clientInfo":{"name":"goose","version":"1.45.0"}}}`)
	init := c.next()
	result, _ := init["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize result: %v", init)
	}
	c.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	c.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	list := c.next()
	if !strings.Contains(fmt.Sprintf("%v", list["result"]), ToolName) {
		t.Fatalf("tools/list missing %s: %v", ToolName, list)
	}

	// The tool call blocks on an elicitation/create request to us.
	c.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ask_user","arguments":{"question":"Which database should the migration target?","options":["postgres","mysql"]}}}`)
	ask := c.next()
	if ask["method"] != "elicitation/create" {
		t.Fatalf("expected an elicitation/create request, got %v", ask)
	}
	params, _ := ask["params"].(map[string]any)
	if params["message"] != "Which database should the migration target?" {
		t.Fatalf("question not carried: %v", params)
	}
	schema, _ := params["requestedSchema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	answer, _ := props["answer"].(map[string]any)
	if answer["type"] != "string" || fmt.Sprintf("%v", answer["enum"]) != "[postgres mysql]" {
		t.Fatalf("schema not built from the options: %v", schema)
	}

	// Answer it; the tool result carries the human's choice.
	c.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":{"action":"accept","content":{"answer":"postgres"}}}`, ask["id"]))
	toolRes := c.next()
	if id, _ := toolRes["id"].(float64); id != 3 {
		t.Fatalf("tool result id %v, want 3", toolRes["id"])
	}
	text := fmt.Sprintf("%v", toolRes["result"])
	if !strings.Contains(text, "The human answered: postgres") {
		t.Fatalf("tool result does not carry the answer: %s", text)
	}
	if strings.Contains(text, "isError:true") {
		t.Fatalf("an answered question is not an error: %s", text)
	}
}

func TestInitializeNeverAgreesToPreElicitationRevisions(t *testing.T) {
	c, stop := startServer(t)
	defer stop()

	// 2025-03-26 predates elicitation, so agreeing to it would promise a
	// revision in which elicitation/create does not exist. The server must
	// answer its own version and leave the disconnect decision to the client.
	c.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"goose","version":"1.0.0"}}}`)
	init := c.next()
	result, _ := init["result"].(map[string]any)
	if result["protocolVersion"] != protocolVersion {
		t.Fatalf("a pre-elicitation offer must be answered with %s, got %v", protocolVersion, init)
	}
}

func TestDeclineCancelAndEOF(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want string
	}{
		{"decline", `"result":{"action":"decline"}`, "declined"},
		{"cancel", `"result":{"action":"cancel"}`, "No human answered"},
		{"error", `"error":{"code":-32603,"message":"boom"}`, "could not be delivered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, stop := startServer(t)
			defer stop()
			c.send(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"ask_user","arguments":{"question":"Go on?"}}}`)
			ask := c.next()
			c.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,%s}`, ask["id"], tc.reply))
			res := c.next()
			if !strings.Contains(fmt.Sprintf("%v", res["result"]), tc.want) {
				t.Fatalf("%s: tool result %v should mention %q", tc.name, res["result"], tc.want)
			}
		})
	}

	// stdin closing while a question waits must not hang the server.
	t.Run("eof", func(t *testing.T) {
		c, stop := startServer(t)
		c.send(`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"ask_user","arguments":{"question":"Still there?"}}}`)
		_ = c.next() // the elicitation request
		stop()       // closes stdin: Serve must return promptly (asserted inside stop)
	})
}

func TestBadCalls(t *testing.T) {
	c, stop := startServer(t)
	defer stop()
	c.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if res := c.next(); res["error"] == nil {
		t.Fatalf("unknown tool should error: %v", res)
	}
	c.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ask_user","arguments":{}}}`)
	res := c.next()
	if !strings.Contains(fmt.Sprintf("%v", res["result"]), "needs a question") {
		t.Fatalf("empty question should be reported: %v", res)
	}
	c.send(`{"jsonrpc":"2.0","id":3,"method":"resources/list"}`)
	if res := c.next(); res["error"] == nil {
		t.Fatalf("unsupported method should error: %v", res)
	}
}
