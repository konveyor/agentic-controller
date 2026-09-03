package kai

import (
	"testing"

	agenticv1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

func TestParseParamFlags(t *testing.T) {
	got, err := parseParamFlags([]string{"A=1", "B=hello=world", " C =x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["A"] != "1" || got["B"] != "hello=world" || got["C"] != "x" {
		t.Errorf("unexpected parse result: %#v", got)
	}
	if _, err := parseParamFlags([]string{"noequals"}); err == nil {
		t.Error("expected error for flag without '='")
	}
}

func TestValidateParamValue(t *testing.T) {
	tests := []struct {
		name    string
		typ     string
		value   string
		wantErr bool
	}{
		{"string anything", paramTypeString, "whatever", false},
		{"number ok", paramTypeNumber, "3.14", false},
		{"number bad", paramTypeNumber, "abc", true},
		{"boolean ok", paramTypeBoolean, "true", false},
		{"boolean bad", paramTypeBoolean, "yesish", true},
		{"empty skipped", paramTypeNumber, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := agenticv1alpha1.Param{Name: "P", Type: agenticv1alpha1.ParamType(tt.typ)}
			err := validateParamValue(p, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateParamValue() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestResolveRunParams_NonInteractive covers the flag-driven (non-terminal) path.
func TestResolveRunParams_NonInteractive(t *testing.T) {
	if isInteractive() {
		t.Skip("test assumes a non-interactive environment")
	}

	declared := []agenticv1alpha1.Param{
		{Name: colRequired, Type: paramTypeString, Required: true},
		{Name: "WITH_DEFAULT", Type: paramTypeString, Default: "def"},
		{Name: "OPTIONAL_EMPTY", Type: paramTypeString},
	}

	// Missing a required param must fail.
	if _, err := resolveRunParams(declared, map[string]string{}); err == nil {
		t.Error("expected error when required param missing")
	}

	// An explicitly-empty required param (--param REQUIRED=) must also fail.
	if _, err := resolveRunParams(declared, map[string]string{colRequired: ""}); err == nil {
		t.Error("expected error when required param provided empty")
	}

	// Whitespace-only is treated as empty for a required param.
	if _, err := resolveRunParams(declared, map[string]string{colRequired: "   "}); err == nil {
		t.Error("expected error when required param provided as whitespace")
	}

	// All provided/defaulted: required from flags, default applied, empty optional dropped.
	out, err := resolveRunParams(declared, map[string]string{colRequired: "val"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := map[string]string{}
	for _, p := range out {
		got[p.Name] = p.Value
	}
	if got[colRequired] != "val" {
		t.Errorf("expected REQUIRED=val, got %q", got[colRequired])
	}
	if got["WITH_DEFAULT"] != "def" {
		t.Errorf("expected WITH_DEFAULT=def, got %q", got["WITH_DEFAULT"])
	}
	if _, ok := got["OPTIONAL_EMPTY"]; ok {
		t.Errorf("expected empty optional param to be dropped, got %q", got["OPTIONAL_EMPTY"])
	}
}
