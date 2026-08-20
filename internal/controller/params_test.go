/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	testAppURL      = "https://example.com/app"
	testWorkflowApp = "coolstore"
	testNumParam    = "max_fix"
)

func ptrInt(i int) *int { return &i }

func rawExt(m map[string]any) *runtime.RawExtension {
	raw, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return &runtime.RawExtension{Raw: raw}
}

func TestCoerceParams(t *testing.T) {
	decls := []konveyoriov1alpha1.Param{
		{Name: testParamName, Type: konveyoriov1alpha1.ParamTypeString},
		{Name: testNumParam, Type: konveyoriov1alpha1.ParamTypeNumber},
		{Name: "dry_run", Type: konveyoriov1alpha1.ParamTypeBoolean},
		{Name: "framework", Type: konveyoriov1alpha1.ParamTypeString, Default: "quarkus"},
		{Name: "unset", Type: konveyoriov1alpha1.ParamTypeString},
	}
	supplied := map[string]string{
		testParamName: testAppURL,
		testNumParam:  "5",
		"dry_run":     "true",
	}

	coerced, strs, err := coerceParams(decls, supplied)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if coerced[testParamName] != testAppURL {
		t.Errorf("source_url = %v, want string", coerced[testParamName])
	}
	if coerced[testNumParam] != int64(5) {
		t.Errorf("max_fix = %v (%T), want int64(5)", coerced[testNumParam], coerced[testNumParam])
	}
	if coerced["dry_run"] != true {
		t.Errorf("dry_run = %v (%T), want bool true", coerced["dry_run"], coerced["dry_run"])
	}
	if coerced["framework"] != "quarkus" {
		t.Errorf("framework = %v, want default quarkus", coerced["framework"])
	}
	if _, ok := coerced["unset"]; ok {
		t.Errorf("unset should be omitted, got %v", coerced["unset"])
	}
	// String form preserves the raw value for substitution.
	if strs[testNumParam] != "5" {
		t.Errorf("strs[max_fix] = %q, want \"5\"", strs[testNumParam])
	}
}

func TestCoerceParamsInvalid(t *testing.T) {
	cases := []struct {
		name  string
		decls []konveyoriov1alpha1.Param
		vals  map[string]string
	}{
		{
			name:  "non-numeric number",
			decls: []konveyoriov1alpha1.Param{{Name: "n", Type: konveyoriov1alpha1.ParamTypeNumber}},
			vals:  map[string]string{"n": "abc"},
		},
		{
			name:  "non-boolean boolean",
			decls: []konveyoriov1alpha1.Param{{Name: "b", Type: konveyoriov1alpha1.ParamTypeBoolean}},
			vals:  map[string]string{"b": "maybe"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := coerceParams(tc.decls, tc.vals); err == nil {
				t.Errorf("expected coercion error, got nil")
			}
		})
	}
}

func TestSubstitute(t *testing.T) {
	scopes := map[string]map[string]string{
		"agent":    {testParamName: testAppURL},
		"workflow": {"application_name": testWorkflowApp},
	}

	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"agent ref", "Migrate $(agent.source_url) now", "Migrate https://example.com/app now", false},
		{"workflow ref", "App $(workflow.application_name)", "App coolstore", false},
		{"both", "$(workflow.application_name): $(agent.source_url)", "coolstore: https://example.com/app", false},
		{"no refs", "plain text", "plain text", false},
		{"non-syntax dollar passes through", "cost is $(5) dollars", "cost is $(5) dollars", false},
		{"helm-ish braces pass through", "image: {{ .Values.tag }}", "image: {{ .Values.tag }}", false},
		{"unknown agent param", "$(agent.nope)", "", true},
		{"unknown scope name", "$(workflow.nope)", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := substitute(tc.in, scopes)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (out=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveExecution(t *testing.T) {
	// The Agent base carries only limits — no mode (ADR 0011/0018).
	base := &konveyoriov1alpha1.ExecutionLimits{
		MaxTurns: ptrInt(200),
		MaxCost:  "10.00",
	}

	// Override wins per field; unset limits fall back to base. Mode comes
	// only from the override, since the Agent declares none.
	got := resolveExecution(&konveyoriov1alpha1.ExecutionSpec{
		Mode:            konveyoriov1alpha1.ExecutionModeApprove,
		ExecutionLimits: konveyoriov1alpha1.ExecutionLimits{MaxTurns: ptrInt(50)},
	}, base)
	if got.Mode != konveyoriov1alpha1.ExecutionModeApprove {
		t.Errorf("mode = %v, want approve", got.Mode)
	}
	if got.MaxTurns == nil || *got.MaxTurns != 50 {
		t.Errorf("maxTurns = %v, want 50 (override)", got.MaxTurns)
	}
	if got.MaxCost != "10.00" {
		t.Errorf("maxCost = %q, want 10.00 (base fallback)", got.MaxCost)
	}

	// Both nil -> nil.
	if resolveExecution(nil, nil) != nil {
		t.Errorf("resolveExecution(nil, nil) should be nil")
	}
}

func TestEffectiveModeDefault(t *testing.T) {
	if effectiveMode(nil) != konveyoriov1alpha1.ExecutionModeAuto {
		t.Errorf("nil exec should default to auto")
	}
	if effectiveMode(&konveyoriov1alpha1.ExecutionSpec{}) != konveyoriov1alpha1.ExecutionModeAuto {
		t.Errorf("empty mode should default to auto")
	}
	if effectiveMode(&konveyoriov1alpha1.ExecutionSpec{Mode: konveyoriov1alpha1.ExecutionModeApprove}) != konveyoriov1alpha1.ExecutionModeApprove {
		t.Errorf("explicit approve should be preserved")
	}
}

func TestBuildParamsThreeSections(t *testing.T) {
	agent := &konveyoriov1alpha1.Agent{
		Spec: konveyoriov1alpha1.AgentSpec{
			Params: []konveyoriov1alpha1.Param{
				{Name: testParamName, Type: konveyoriov1alpha1.ParamTypeString},
			},
			Execution: &konveyoriov1alpha1.ExecutionLimits{MaxTurns: ptrInt(200)},
		},
	}
	run := &konveyoriov1alpha1.AgentRun{
		Spec: konveyoriov1alpha1.AgentRunSpec{
			Params: []konveyoriov1alpha1.ParamValue{{Name: testParamName, Value: testAppURL}},
			WorkflowParams: rawExt(map[string]any{
				"application_name": testWorkflowApp,
				testNumParam:       json.Number("5"),
			}),
		},
	}

	f, scopes, err := buildParams(run, agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Agent[testParamName] != testAppURL {
		t.Errorf("agent section wrong: %v", f.Agent)
	}
	if f.Workflow["application_name"] != testWorkflowApp {
		t.Errorf("workflow section wrong: %v", f.Workflow)
	}
	if f.Execution["mode"] != string(konveyoriov1alpha1.ExecutionModeAuto) {
		t.Errorf("execution mode should default to auto, got %v", f.Execution["mode"])
	}
	if f.Execution["maxTurns"] != 200 {
		t.Errorf("execution maxTurns = %v, want 200", f.Execution["maxTurns"])
	}
	if scopes["agent"][testParamName] != testAppURL {
		t.Errorf("agent scope missing source_url")
	}
	if scopes["workflow"][testNumParam] != "5" {
		t.Errorf("workflow scope max_fix = %q, want \"5\"", scopes["workflow"][testNumParam])
	}
}
