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

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// TestVerificationJobImage covers the two ends of the VerificationImage knob
// that cmd/main.go fills from VERIFICATION_IMAGE: unset, the Job keeps the
// compile-time default, so an upgrade that only adds the env var changes
// nothing; set, every verification Job runs the configured image, which is how
// a mirror, a private registry or a locally built arm64 agent image is reached
// (konveyor/agentic-controller#305).
func TestVerificationJobImage(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		want       string
	}{
		{
			name:       "unset falls back to the built-in default",
			configured: "",
			want:       DefaultVerificationImage,
		},
		{
			name:       "configured image overrides the default",
			configured: "registry.example.com/mirror/agentic-controller-agent:v1",
			want:       "registry.example.com/mirror/agentic-controller-agent:v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := clientgoscheme.AddToScheme(scheme); err != nil {
				t.Fatalf("add client-go scheme: %v", err)
			}
			if err := konveyoriov1alpha1.AddToScheme(scheme); err != nil {
				t.Fatalf("add konveyor scheme: %v", err)
			}

			gateway := &konveyoriov1alpha1.Gateway{
				ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "default"},
				Spec: konveyoriov1alpha1.GatewaySpec{
					Provider: providerOpenAI,
					Endpoint: "https://llm.example.com",
					CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{
						SecretName: "gw-creds",
						Key:        "apiKey",
					},
				},
			}

			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gateway).Build()
			r := &GatewayReconciler{Client: c, Scheme: scheme, VerificationImage: tt.configured}

			const jobName = "gw-verify-test"
			// An OpenAI-compatible provider probes its own spec.endpoint;
			// only aws-bedrock derives the address from the credential
			// Secret's AWS_REGION. This test is about the image, so the
			// endpoint is just the one Reconcile would pass here.
			if err := r.createVerificationJob(
				context.Background(), gateway, jobName, gateway.Spec.Endpoint,
			); err != nil {
				t.Fatalf("createVerificationJob: %v", err)
			}

			var job batchv1.Job
			key := client.ObjectKey{Name: jobName, Namespace: gateway.Namespace}
			if err := c.Get(context.Background(), key, &job); err != nil {
				t.Fatalf("get Job %s: %v", jobName, err)
			}
			containers := job.Spec.Template.Spec.Containers
			if len(containers) != 1 {
				t.Fatalf("expected one container on the verification Job, got %d", len(containers))
			}
			if got := containers[0].Image; got != tt.want {
				t.Errorf("verification Job image = %q, want %q", got, tt.want)
			}
		})
	}
}
