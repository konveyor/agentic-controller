package hub

import (
	"os"
	"testing"
)

func TestParseAppID(t *testing.T) {
	tests := []struct {
		input   string
		want    uint
		wantErr bool
	}{
		{"42", 42, false},
		{"0", 0, false},
		{"1000000", 1000000, false},
		{"", 0, true},
		{"abc", 0, true},
		{"-1", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseAppID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseAppID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseAppID(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestClearEnvRemovesAllHubVars(t *testing.T) {
	vars := []string{"HUB_BASE_URL", "HUB_TOKEN", "HUB_TOKEN_ID", "APP_ID"}
	for _, k := range vars {
		t.Setenv(k, "test-value")
	}

	ClearEnv()

	for _, k := range vars {
		if v := os.Getenv(k); v != "" {
			t.Errorf("ClearEnv should unset %s, got %q", k, v)
		}
	}
}
