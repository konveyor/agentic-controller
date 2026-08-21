package acp

import (
	"encoding/json"
	"testing"
)

// goose keeps the JSON-RPC message at the boilerplate and puts the cause in
// data. A bare "Internal error" in the pod log is all a dead run leaves
// behind unless data travels with it.
func TestRPCErrorCarriesData(t *testing.T) {
	const boilerplate = "Internal error"
	cases := []struct {
		name string
		data string
		want string
	}{
		{"string data", `"Error getting agent reply: Provider not set"`, boilerplate + " — Error getting agent reply: Provider not set"},
		{"object data, as goose sends credits exhaustion", `{"reason":"credits_exhausted"}`, boilerplate + ` — {"reason":"credits_exhausted"}`},
		{"pretty-printed object data", "{\n  \"reason\": \"x\"\n}", boilerplate + ` — {"reason":"x"}`},
		{"no data", ``, boilerplate},
		{"null data", `null`, boilerplate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &RPCError{Code: -32603, Message: boilerplate}
			if tc.data != "" {
				e.Data = json.RawMessage(tc.data)
			}
			if got := e.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}
