package acp

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func usageNotification(t *testing.T, method, update string) *RPCResponse {
	t.Helper()
	var msg RPCResponse
	if err := json.Unmarshal([]byte(`{"jsonrpc":"2.0","method":"`+method+`","params":{"sessionId":"s1","update":`+update+`}}`), &msg); err != nil {
		t.Fatal(err)
	}
	return &msg
}

func TestUsageSnapshotsAcrossChannels(t *testing.T) {
	result := &PromptResult{}
	sawCost := false
	update := `{"sessionUpdate":"usage_update","inputTokens":100,"outputTokens":25,"cost":{"amount":1.5,"currency":"USD"}}`
	for _, method := range []string{"session/update", gooseUpdateMethod, "session/update"} {
		trackUsage(usageNotification(t, method, update), result, &sawCost)
	}
	if result.InputTokens == nil || *result.InputTokens != 100 || result.OutputTokens == nil || *result.OutputTokens != 25 || result.Cost != 1.5 || !result.CostReported || !sawCost {
		t.Fatalf("snapshot=%+v", result)
	}
	// Missing values are gaps, not a reset to zero. A reported zero remains
	// distinguishable from an omitted value without changing the wire schema.
	trackUsage(usageNotification(t, "session/update", `{"sessionUpdate":"usage_update","outputTokens":0}`), result, &sawCost)
	if *result.InputTokens != 100 || *result.OutputTokens != 0 || result.Cost != 1.5 {
		t.Fatalf("partial snapshot=%+v", result)
	}
}

func TestUsageUnknownAndInvalidMeasurements(t *testing.T) {
	for _, update := range []string{
		`{"sessionUpdate":"usage_update"}`,
		`{"sessionUpdate":"usage_update","inputTokens":-1,"outputTokens":-1,"cost":{"currency":"USD"}}`,
		`{"sessionUpdate":"usage_update","cost":{"amount":3,"currency":"EUR"}}`,
		`{"sessionUpdate":"usage_update","cost":{"amount":-1,"currency":"USD"}}`,
		`{"sessionUpdate":"tool_call","inputTokens":3,"cost":{"amount":1}}`,
	} {
		result := &PromptResult{}
		sawCost := false
		trackUsage(usageNotification(t, "session/update", update), result, &sawCost)
		if sawCost || result.CostReported || result.InputTokens != nil || result.OutputTokens != nil {
			t.Fatalf("invalid or absent data recorded: %+v", result)
		}
	}
	result := &PromptResult{}
	sawCost := false
	trackUsage(usageNotification(t, "session/update", `{"sessionUpdate":"usage_update","inputTokens":0,"outputTokens":0,"cost":{"amount":0,"currency":"USD"}}`), result, &sawCost)
	if !sawCost || !result.CostReported || result.InputTokens == nil || result.OutputTokens == nil {
		t.Fatal("measured zero lost")
	}
}

func TestStandardUsageCancelsAtReportedCost(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	var result *PromptResult
	var err error
	go func() {
		result, err = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 1)
		close(done)
	}()
	req := s.next()
	s.push(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"usage_update","inputTokens":12,"cost":{"amount":1,"currency":"USD"}}}}`)
	cancelFrame := s.next()
	if cancelFrame["method"] != "session/cancel" {
		t.Fatalf("frame=%v", cancelFrame)
	}
	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(int64(req["id"].(float64))) + `,"result":{"stopReason":"cancelled"}}`)
	<-done
	if err != nil || !result.CostLimitReached || !result.CostReported || result.InputTokens == nil || *result.InputTokens != 12 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestPromptResultUsesRuntimeTurnMeasurement(t *testing.T) {
	result := &PromptResult{TurnsUsed: 7}
	if err := decodePromptResult(json.RawMessage(`{"stopReason":"end_turn","_meta":{"lightspeed":{"turnsUsed":1}}}`), result); err != nil {
		t.Fatal(err)
	}
	if result.TurnsUsed != 1 || !result.StopReasonAuthoritative {
		t.Fatalf("result=%+v", result)
	}
}

func TestAuthoritativeStopReasonSurvivesDisconnect(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)
	sc.SetAuthoritativeStopReasons(true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	var result *PromptResult
	var err error
	go func() {
		result, err = sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0)
		close(done)
	}()
	s.next()
	s.push(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"one"}}}`)
	s.mu.Lock()
	s.conn.Close()
	s.mu.Unlock()
	<-done
	if err != ErrConnectionLost || result == nil || !result.StopReasonAuthoritative || result.TurnsUsed != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
