package acp

import (
	"context"
	"errors"
	"testing"
	"time"
)

// usageUpdateFrame builds a goose-custom usage_update notification.
// costJSON is inlined as-is (empty string omits the cost field
// entirely, exercising the "provider never reports cost" path).
func usageUpdateFrame(sessionID string, used, size int, costJSON string) string {
	cost := ""
	if costJSON != "" {
		cost = `,"cost":{"amount":` + costJSON + `,"currency":"USD"}`
	}
	return `{"jsonrpc":"2.0","method":"_goose/unstable/session/update","params":{` +
		`"sessionId":"` + sessionID + `","update":{"sessionUpdate":"usage_update",` +
		`"used":` + itoa(used) + `,"size":` + itoa(size) + cost + `}}}`
}

func itoa(n int) string {
	// Avoids importing strconv just for one conversion in test fixtures.
	return fmtInt(n)
}

func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

// TestSendPromptCostUnderLimitDoesNotCancel proves a usage_update below
// costLimit does not trigger session/cancel — the prompt runs to its
// natural completion untouched.
func TestSendPromptCostUnderLimitDoesNotCancel(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	var result *PromptResult
	var sendErr error
	go func() {
		result, sendErr = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 8.5)
		close(done)
	}()

	promptReq := s.next()
	promptID := int64(promptReq["id"].(float64))

	s.push(usageUpdateFrame("s1", 1000, 200000, "2.00"))

	// No cancel should arrive before the final response — a short,
	// deliberate wait for an absence, not a race: the client only ever
	// sends the cancel synchronously in response to the usage_update
	// already pushed above.
	select {
	case m := <-s.inbound:
		t.Fatalf("unexpected client frame (expected none): %v", m)
	case <-time.After(200 * time.Millisecond):
	}

	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"end_turn"}}`)
	<-done
	if sendErr != nil {
		t.Fatalf("SendPrompt: %v", sendErr)
	}
	if result.CostLimitReached {
		t.Error("CostLimitReached should be false when cost stays under the limit")
	}
	if result.Cost != 2.00 {
		t.Errorf("Cost = %v, want 2.00", result.Cost)
	}
}

// TestSendPromptCancelsOnceWhenCostLimitReached proves the harness
// sends exactly one session/cancel when cumulative cost crosses
// costLimit, and that the result reports CostLimitReached so the
// caller can distinguish this from a viewer-initiated cancel.
func TestSendPromptCancelsOnceWhenCostLimitReached(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	var result *PromptResult
	var sendErr error
	go func() {
		result, sendErr = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 8.5)
		close(done)
	}()

	promptReq := s.next()
	promptID := int64(promptReq["id"].(float64))

	s.push(usageUpdateFrame("s1", 50000, 200000, "9.00"))

	cancelFrame := s.next()
	if cancelFrame["method"] != "session/cancel" {
		t.Fatalf("expected session/cancel, got %v", cancelFrame)
	}

	// A second usage_update still over the limit must not trigger a
	// second cancel.
	s.push(usageUpdateFrame("s1", 51000, 200000, "9.50"))
	select {
	case m := <-s.inbound:
		t.Fatalf("expected exactly one cancel, got a second client frame: %v", m)
	case <-time.After(200 * time.Millisecond):
	}

	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"cancelled"}}`)
	<-done
	if sendErr != nil {
		t.Fatalf("SendPrompt: %v", sendErr)
	}
	if !result.CostLimitReached {
		t.Error("CostLimitReached should be true")
	}
	if result.StopReason != "cancelled" {
		t.Errorf("StopReason = %q, want cancelled", result.StopReason)
	}
}

// TestSendPromptCostLimitZeroDisablesMonitoring proves costLimit == 0
// never triggers a cancel regardless of reported cost.
func TestSendPromptCostLimitZeroDisablesMonitoring(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_, _ = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0)
		close(done)
	}()

	promptReq := s.next()
	promptID := int64(promptReq["id"].(float64))

	s.push(usageUpdateFrame("s1", 1000, 200000, "999.00"))
	select {
	case m := <-s.inbound:
		t.Fatalf("costLimit=0 must disable monitoring entirely, got: %v", m)
	case <-time.After(200 * time.Millisecond):
	}

	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"end_turn"}}`)
	<-done
}

// TestSendPromptDoesNotEnforceTurnsClientSide is a regression test for
// ADR 0011: the runtime is the sole turn counter now. A large number of
// tool_call notifications must never make SendPrompt terminate the
// prompt itself — only TurnsUsed increments, purely for reporting.
func TestSendPromptDoesNotEnforceTurnsClientSide(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	var result *PromptResult
	var sendErr error
	go func() {
		result, sendErr = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0)
		close(done)
	}()

	promptReq := s.next()
	promptID := int64(promptReq["id"].(float64))

	const toolCalls = 50
	for i := 0; i < toolCalls; i++ {
		s.push(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call"}}}`)
	}

	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"end_turn"}}`)
	<-done
	if sendErr != nil {
		t.Fatalf("SendPrompt terminated early at %d tool calls — turn limits must be runtime-enforced, not client-side: %v", toolCalls, sendErr)
	}
	if result.TurnsUsed != toolCalls {
		t.Errorf("TurnsUsed = %d, want %d", result.TurnsUsed, toolCalls)
	}
}

// TestSendPromptMissingCostLeavesValueUnchanged proves a usage_update
// with no cost field (ADR 0011: cost is optional) does not reset Cost
// to zero, and does not trigger a spurious cancel.
func TestSendPromptMissingCostLeavesValueUnchanged(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	var result *PromptResult
	var sendErr error
	go func() {
		result, sendErr = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 5.0)
		close(done)
	}()

	promptReq := s.next()
	promptID := int64(promptReq["id"].(float64))

	// used/size present, cost absent entirely.
	s.push(usageUpdateFrame("s1", 1000, 200000, ""))
	select {
	case m := <-s.inbound:
		t.Fatalf("no cost ever reported — must not cancel: %v", m)
	case <-time.After(200 * time.Millisecond):
	}

	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"end_turn"}}`)
	<-done
	if sendErr != nil {
		t.Fatalf("SendPrompt: %v", sendErr)
	}
	if result.Cost != 0 {
		t.Errorf("Cost = %v, want 0 (never reported)", result.Cost)
	}
	if result.ContextUsed != 1000 || result.ContextSize != 200000 {
		t.Errorf("context fields = (%d, %d), want (1000, 200000) — usage_update without cost should still update context", result.ContextUsed, result.ContextSize)
	}
}

// TestSendPromptAcceptsContextLimitFieldName proves trackUsage also
// recognizes "contextLimit" (the field name this repo's own tee test
// fixture uses) in addition to ADR 0011's documented "size" — the real
// field name goose sends is unverified in this environment.
func TestSendPromptAcceptsContextLimitFieldName(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	var result *PromptResult
	go func() {
		result, _ = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0)
		close(done)
	}()

	promptReq := s.next()
	promptID := int64(promptReq["id"].(float64))

	s.push(`{"jsonrpc":"2.0","method":"_goose/unstable/session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"usage_update","used":1234,"contextLimit":200000}}}`)
	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"end_turn"}}`)
	<-done

	if result.ContextUsed != 1234 || result.ContextSize != 200000 {
		t.Errorf("contextLimit field name not recognized: got (%d, %d)", result.ContextUsed, result.ContextSize)
	}
}

// TestSendPromptReturnsConnectionLostWithPartialResultOnAbruptClose is a
// regression test for live-observed goose 1.36.0 behavior: hitting the
// native GOOSE_MAX_TURNS limit does not produce a graceful JSON-RPC
// response — the websocket connection just closes. SendPrompt must
// surface this as ErrConnectionLost while still returning whatever
// partial result (TurnsUsed etc.) it had accumulated, so the caller can
// tell "real progress then disconnect" apart from "no response at all."
func TestSendPromptReturnsConnectionLostWithPartialResultOnAbruptClose(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct {
		result *PromptResult
		err    error
	}, 1)
	go func() {
		result, err := sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0)
		done <- struct {
			result *PromptResult
			err    error
		}{result, err}
	}()

	s.next() // the session/prompt request

	// Simulate goose hitting its native turn limit mid-generation: some
	// real tool_call notifications arrive, then the connection drops with
	// no final response — never a graceful stopReason.
	for i := 0; i < 3; i++ {
		s.push(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call"}}}`)
	}
	s.mu.Lock()
	s.conn.Close()
	s.mu.Unlock()

	out := <-done
	if !errors.Is(out.err, ErrConnectionLost) {
		t.Fatalf("err = %v, want ErrConnectionLost", out.err)
	}
	if out.result == nil {
		t.Fatal("result should not be nil — partial progress must survive the disconnect")
	}
	// At least one of the 3 pushed notifications is guaranteed to be
	// processed before the close is observed (the exact count racing
	// against the close isn't the point — surviving partial progress is).
	if out.result.TurnsUsed < 1 {
		t.Errorf("TurnsUsed = %d, want >= 1 (some progress made before the disconnect)", out.result.TurnsUsed)
	}
}
