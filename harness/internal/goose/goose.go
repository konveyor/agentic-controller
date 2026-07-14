package goose

import (
	"context"
	"encoding/json"
)

// Runner is the interface for invoking goose. ACPRunner is the only
// implementation — it communicates with goose serve over WebSocket.
type Runner interface {
	RunRecipe(ctx context.Context, recipe string, maxTurns int, params map[string]string) (json.RawMessage, error)
}
