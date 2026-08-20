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
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

func gitCfg(name, email string) *konveyoriov1alpha1.GitConfig {
	return &konveyoriov1alpha1.GitConfig{UserName: name, UserEmail: email}
}

func TestResolveGitIdentity(t *testing.T) {
	tests := []struct {
		name      string
		agent     *konveyoriov1alpha1.GitConfig
		run       *konveyoriov1alpha1.GitConfig
		wantName  string
		wantEmail string
	}{
		{name: "both unset"},
		{
			name:      "agent only",
			agent:     gitCfg("Coolstore Bot", "bot@myorg.com"),
			wantName:  "Coolstore Bot",
			wantEmail: "bot@myorg.com",
		},
		{
			name:      "run only",
			run:       gitCfg("Jane Dev", "jane@myorg.com"),
			wantName:  "Jane Dev",
			wantEmail: "jane@myorg.com",
		},
		{
			name:      "run overrides agent",
			agent:     gitCfg("Coolstore Bot", "bot@myorg.com"),
			run:       gitCfg("Jane Dev", "jane@myorg.com"),
			wantName:  "Jane Dev",
			wantEmail: "jane@myorg.com",
		},
		{
			name:      "run overrides name only, agent email kept",
			agent:     gitCfg("Coolstore Bot", "bot@myorg.com"),
			run:       gitCfg("Jane Dev", ""),
			wantName:  "Jane Dev",
			wantEmail: "bot@myorg.com",
		},
		{
			name:      "run overrides email only, agent name kept",
			agent:     gitCfg("Coolstore Bot", "bot@myorg.com"),
			run:       gitCfg("", "jane@myorg.com"),
			wantName:  "Coolstore Bot",
			wantEmail: "jane@myorg.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &konveyoriov1alpha1.Agent{}
			agent.Spec.GitConfig = tt.agent
			run := &konveyoriov1alpha1.AgentRun{}
			run.Spec.GitConfig = tt.run

			gotName, gotEmail := resolveGitIdentity(agent, run)
			if gotName != tt.wantName {
				t.Errorf("name = %q, want %q", gotName, tt.wantName)
			}
			if gotEmail != tt.wantEmail {
				t.Errorf("email = %q, want %q", gotEmail, tt.wantEmail)
			}
		})
	}
}

func TestBuildEnvVarsGitIdentity(t *testing.T) {
	r := &AgentRunReconciler{}

	findEnv := func(env []corev1.EnvVar, name string) (string, bool) {
		for _, e := range env {
			if e.Name == name {
				return e.Value, true
			}
		}
		return "", false
	}

	t.Run("emits git identity from agent, overridden by run", func(t *testing.T) {
		agent := &konveyoriov1alpha1.Agent{}
		agent.Spec.GitConfig = gitCfg("Coolstore Bot", "bot@myorg.com")
		run := &konveyoriov1alpha1.AgentRun{}
		run.Spec.GitConfig = gitCfg("Jane Dev", "jane@myorg.com")

		env, _, err := r.buildEnvVars(context.Background(), run, agent, "acp-secret")
		if err != nil {
			t.Fatalf("buildEnvVars: %v", err)
		}
		if v, ok := findEnv(env, "KONVEYOR_GIT_AUTHOR_NAME"); !ok || v != "Jane Dev" {
			t.Errorf("KONVEYOR_GIT_AUTHOR_NAME = %q (present=%v), want %q", v, ok, "Jane Dev")
		}
		if v, ok := findEnv(env, "KONVEYOR_GIT_AUTHOR_EMAIL"); !ok || v != "jane@myorg.com" {
			t.Errorf("KONVEYOR_GIT_AUTHOR_EMAIL = %q (present=%v), want %q", v, ok, "jane@myorg.com")
		}
	})

	t.Run("omits git identity when unset", func(t *testing.T) {
		agent := &konveyoriov1alpha1.Agent{}
		run := &konveyoriov1alpha1.AgentRun{}

		env, _, err := r.buildEnvVars(context.Background(), run, agent, "acp-secret")
		if err != nil {
			t.Fatalf("buildEnvVars: %v", err)
		}
		if _, ok := findEnv(env, "KONVEYOR_GIT_AUTHOR_NAME"); ok {
			t.Error("KONVEYOR_GIT_AUTHOR_NAME should be absent when unset")
		}
		if _, ok := findEnv(env, "KONVEYOR_GIT_AUTHOR_EMAIL"); ok {
			t.Error("KONVEYOR_GIT_AUTHOR_EMAIL should be absent when unset")
		}
	})
}
