package goose

import (
	"encoding/json"
	"testing"
)

func TestStripBanner(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no banner", `{"messages":[]}`, `{"messages":[]}`},
		{"with banner", "Welcome to goose!\n\n{\"messages\":[]}", `{"messages":[]}`},
		{"ascii art", `
   ____
  / ___| ___   ___  ___  ___
 | |  _ / _ \ / _ \/ __|/ _ \
 | |_| | (_) | (_) \__ \  __/
  \____|\___/ \___/|___/\___|
{"messages":[]}`, `{"messages":[]}`},
		{"empty", "", ""},
		{"no json", "just text", "just text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(StripBanner([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("StripBanner = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckAPIErrors(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{`{"messages":[]}`, false},
		{`{"error":"credit balance is too low"}`, true},
		{`{"error":"Rate Limit exceeded"}`, true},
		{`{"error":"Quota Exceeded for this model"}`, true},
		{`{"messages":[{"content":"all good"}]}`, false},
	}

	for _, tt := range tests {
		err := CheckAPIErrors([]byte(tt.input))
		if (err != nil) != tt.wantErr {
			t.Errorf("CheckAPIErrors(%q) err=%v, wantErr=%v", tt.input[:30], err, tt.wantErr)
		}
	}
}

const sampleConversation = `{
  "messages": [
    {
      "role": "assistant",
      "content": [
        {
          "type": "text",
          "text": "I'll analyze the project now."
        }
      ]
    },
    {
      "role": "assistant",
      "content": [
        {
          "type": "toolRequest",
          "toolCall": {
            "value": {
              "name": "developer__read_file",
              "arguments": {"path": "pom.xml"}
            }
          }
        }
      ]
    },
    {
      "role": "assistant",
      "content": [
        {
          "type": "toolRequest",
          "toolCall": {
            "value": {
              "name": "recipe__final_output",
              "arguments": {
                "n": 1,
                "path": "pom.xml",
                "status": "ok",
                "files_touched": ["pom.xml"],
                "lesson": "updated deps",
                "error_log": "",
                "summary": "migrated pom.xml"
              }
            }
          }
        }
      ]
    }
  ]
}`

func TestExtractFinalOutput(t *testing.T) {
	result, err := ExtractFinalOutput([]byte(sampleConversation))
	if err != nil {
		t.Fatalf("ExtractFinalOutput: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if parsed["status"] != "ok" {
		t.Errorf("status = %v, want ok", parsed["status"])
	}
	if parsed["path"] != "pom.xml" {
		t.Errorf("path = %v, want pom.xml", parsed["path"])
	}
	if parsed["lesson"] != "updated deps" {
		t.Errorf("lesson = %v", parsed["lesson"])
	}
}

func TestExtractFinalOutputNoResult(t *testing.T) {
	conv := `{"messages":[{"role":"assistant","content":[{"type":"text","text":"hello"}]}]}`
	_, err := ExtractFinalOutput([]byte(conv))
	if err == nil {
		t.Error("expected error for missing final output")
	}
}

func TestExtractFinalOutputMultiple(t *testing.T) {
	conv := `{
		"messages": [
			{"role":"assistant","content":[
				{"type":"toolRequest","toolCall":{"value":{"name":"recipe__final_output","arguments":{"status":"draft"}}}}
			]},
			{"role":"assistant","content":[
				{"type":"toolRequest","toolCall":{"value":{"name":"recipe__final_output","arguments":{"status":"final"}}}}
			]}
		]
	}`
	result, err := ExtractFinalOutput([]byte(conv))
	if err != nil {
		t.Fatalf("ExtractFinalOutput: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)
	if parsed["status"] != "final" {
		t.Errorf("expected last output, got status = %v", parsed["status"])
	}
}

func TestExtractFinalOutputBadJSON(t *testing.T) {
	_, err := ExtractFinalOutput([]byte("not json"))
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}
