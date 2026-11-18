package acp

import (
	"context"
	"errors"
	"testing"
	"time"
)

// usageUpdateFrame builds goose's custom usage_update notification, in the
// shape goose v1.55.0 serializes (SessionUsageUpdate in goose-sdk-types).
// costJSON is inlined as-is (empty string omits the cost field entirely,
// which is what goose does for a model it has no price for).
func usageUpdateFrame(sessionID string, used, size int, costJSON string) string {
	cost := ""
	if costJSON != "" {
		cost = `,"accumulatedCost":` + costJSON
	}
	return `{"jsonrpc":"2.0","method":"_goose/unstable/session/update","params":{` +
		`"sessionId":"` + sessionID + `","update":{"sessionUpdate":"usage_update",` +
		`"used":` + itoa(used) + `,"contextLimit":` + itoa(size) +
		`,"accumulatedInputTokens":1000,"accumulatedOutputTokens":100` + cost + `}}}`
}

// standardUsageUpdateFrame builds the standard ACP usage_update goose sends
// alongside its custom one for the same usage event.
func standardUsageUpdateFrame(sessionID string, used, size int, costJSON string) string {
	cost := ""
	if costJSON != "" {
		cost = `,"cost":{"amount":` + costJSON + `,"currency":"USD"}`
	}
	return `{"jsonrpc":"2.0","method":"session/update","params":{` +
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

// TestSendPromptEnforcesCostFromEitherUsageFrame proves the ceiling acts on
// each of the two frames goose sends for one usage event, read on its own.
// The custom frame's accumulatedCost is the one the harness used to miss,
// which left maxCost with no effect on any run.
func TestSendPromptEnforcesCostFromEitherUsageFrame(t *testing.T) {
	frames := map[string]func(sessionID string, used, size int, costJSON string) string{
		"custom accumulatedCost":   usageUpdateFrame,
		"standard ACP cost.amount": standardUsageUpdateFrame,
	}
	for name, frame := range frames {
		t.Run(name, func(t *testing.T) {
			s := newDemuxServer(t)
			c := s.dial(t)
			sc := NewSessionClient(c)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			done := make(chan struct{})
			var result *PromptResult
			var sendErr error
			go func() {
				result, sendErr = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0.85)
				close(done)
			}()

			promptReq := s.next()
			promptID := int64(promptReq["id"].(float64))

			s.push(frame("s1", 50000, 200000, "0.90"))
			if m := s.next(); m["method"] != "session/cancel" {
				t.Fatalf("expected session/cancel, got %v", m)
			}

			s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"cancelled"}}`)
			<-done
			if sendErr != nil {
				t.Fatalf("SendPrompt: %v", sendErr)
			}
			if !result.CostLimitReached || !result.CostReported || result.Cost != 0.90 {
				t.Errorf("result = %+v, want CostLimitReached, CostReported and Cost=0.90", result)
			}
			if result.ContextUsed != 50000 || result.ContextSize != 200000 {
				t.Errorf("context = (%d, %d), want (50000, 200000)", result.ContextUsed, result.ContextSize)
			}
		})
	}
}

// TestSendPromptCancelsOnceForPairedUsageFrames proves the pair goose sends
// for a single usage event produces one cancel, not one per frame.
func TestSendPromptCancelsOnceForPairedUsageFrames(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_, _ = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0.85)
		close(done)
	}()

	promptReq := s.next()
	promptID := int64(promptReq["id"].(float64))

	s.push(usageUpdateFrame("s1", 50000, 200000, "0.90"))
	s.push(standardUsageUpdateFrame("s1", 50000, 200000, "0.90"))
	if m := s.next(); m["method"] != "session/cancel" {
		t.Fatalf("expected session/cancel, got %v", m)
	}
	select {
	case m := <-s.inbound:
		t.Fatalf("expected exactly one cancel, got a second client frame: %v", m)
	case <-time.After(200 * time.Millisecond):
	}

	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"cancelled"}}`)
	<-done
}

// TestSendPromptCostReported proves CostReported separates a run goose
// priced, even at zero, from one it could not price.
func TestSendPromptCostReported(t *testing.T) {
	tests := []struct {
		name string
		cost string
		want bool
	}{
		{name: "priced model", cost: "0.25", want: true},
		{name: "reported zero is still reported", cost: "0", want: true},
		{name: "unpriced model omits cost", cost: "", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newDemuxServer(t)
			c := s.dial(t)
			sc := NewSessionClient(c)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			done := make(chan struct{})
			var result *PromptResult
			go func() {
				result, _ = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 5.0)
				close(done)
			}()

			promptReq := s.next()
			promptID := int64(promptReq["id"].(float64))

			s.push(usageUpdateFrame("s1", 1000, 200000, tc.cost))
			s.push(standardUsageUpdateFrame("s1", 1000, 200000, tc.cost))
			s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"end_turn"}}`)
			<-done
			if result == nil {
				t.Fatal("SendPrompt returned no result")
			}
			if result.CostReported != tc.want {
				t.Errorf("CostReported = %v, want %v", result.CostReported, tc.want)
			}
		})
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

// TestSendPromptAcceptsContextLimitFieldName proves trackUsage reads
// "contextLimit", the field name on goose's custom usage_update; the
// standard ACP frame calls the same thing "size".
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

// TestSendPromptHandoffUsesFullBudgetAfterReservedCancel is a regression
// test for the harness's two-prompt handoff flow (ADR 0011): the primary
// prompt is cancelled at the reserved fraction of the budget (e.g. 8.5 of
// a 10.0 maxCost), and the follow-up handoff prompt on the same session
// must be allowed to spend the rest of the full 10.0 ceiling — not get
// cancelled immediately because cumulative cost already exceeds the
// reserved fraction. Cancellation must fire only once cumulative cost
// reaches the full budget passed to that call.
func TestSendPromptHandoffUsesFullBudgetAfterReservedCancel(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Primary prompt: costLimit is the reserved fraction (8.5 of 10.0).
	done := make(chan struct{})
	var primary *PromptResult
	var primaryErr error
	go func() {
		primary, primaryErr = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 8.5)
		close(done)
	}()

	primaryReq := s.next()
	primaryID := int64(primaryReq["id"].(float64))

	s.push(usageUpdateFrame("s1", 50000, 200000, "9.00"))
	cancelFrame := s.next()
	if cancelFrame["method"] != "session/cancel" {
		t.Fatalf("expected session/cancel on the primary prompt, got %v", cancelFrame)
	}
	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(primaryID) + `,"result":{"stopReason":"cancelled"}}`)
	<-done
	if primaryErr != nil {
		t.Fatalf("primary SendPrompt: %v", primaryErr)
	}
	if !primary.CostLimitReached || primary.Cost != 9.00 {
		t.Fatalf("primary result = %+v, want CostLimitReached=true Cost=9.00", primary)
	}

	// Handoff prompt on the same session: costLimit is the full,
	// unreserved budget (10.0). ACP cost is session-cumulative, so the
	// first usage_update here reports the same 9.00 already spent by the
	// primary — that must NOT trigger an immediate cancel.
	done = make(chan struct{})
	var handoff *PromptResult
	var handoffErr error
	go func() {
		handoff, handoffErr = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "handoff"}}, 10.0)
		close(done)
	}()

	handoffReq := s.next()
	handoffID := int64(handoffReq["id"].(float64))

	s.push(usageUpdateFrame("s1", 50100, 200000, "9.10"))
	select {
	case m := <-s.inbound:
		t.Fatalf("handoff cancelled before reaching the full budget: %v", m)
	case <-time.After(200 * time.Millisecond):
	}

	// Cumulative cost now reaches the full budget — this must cancel.
	s.push(usageUpdateFrame("s1", 50200, 200000, "10.00"))
	cancelFrame = s.next()
	if cancelFrame["method"] != "session/cancel" {
		t.Fatalf("expected session/cancel once the handoff reaches the full budget, got %v", cancelFrame)
	}

	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(handoffID) + `,"result":{"stopReason":"cancelled"}}`)
	<-done
	if handoffErr != nil {
		t.Fatalf("handoff SendPrompt: %v", handoffErr)
	}
	if !handoff.CostLimitReached || handoff.Cost != 10.00 {
		t.Fatalf("handoff result = %+v, want CostLimitReached=true Cost=10.00", handoff)
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
	err := s.conn.Close()
	s.mu.Unlock()
	if err != nil {
		t.Errorf("close test connection: %v", err)
	}

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

// TestSendPromptTurnHandler proves the turn handler fires once per
// tool_call notification with the running count, and not for other
// updates, so a caller can re-render progress as the turn advances.
func TestSendPromptTurnHandler(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)
	var seen []int
	sc.SetTurnHandler(func(n int) { seen = append(seen, n) })

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

	toolCall := func(id string) string {
		return `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"` + id + `","title":"read","status":"pending"}}}`
	}
	s.push(toolCall("t1"))
	s.push(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed"}}}`)
	s.push(usageUpdateFrame("s1", 1000, 200000, "0.10"))
	s.push(toolCall("t2"))
	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"end_turn"}}`)
	<-done
	if sendErr != nil {
		t.Fatalf("SendPrompt: %v", sendErr)
	}
	if result.TurnsUsed != 2 {
		t.Errorf("TurnsUsed = %d, want 2", result.TurnsUsed)
	}
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 {
		t.Errorf("turn handler saw %v, want [1 2]", seen)
	}
}
