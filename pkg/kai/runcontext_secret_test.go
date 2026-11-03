package kai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agenticv1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const secretTestNS = "test"

// newRun returns an empty run object of the requested kind, with a stable
// name/UID so ownerReferences can be asserted.
func newRun(kind string) client.Object {
	meta := metav1.ObjectMeta{Name: "run", Namespace: secretTestNS, UID: types.UID("run-uid")}
	if kind == kindAgentWorkflowRun {
		return &agenticv1alpha1.AgentWorkflowRun{ObjectMeta: meta}
	}
	return &agenticv1alpha1.AgentRun{ObjectMeta: meta}
}

func runEnv(run client.Object) []corev1.EnvVar {
	switch r := run.(type) {
	case *agenticv1alpha1.AgentRun:
		return r.Spec.Env
	case *agenticv1alpha1.AgentWorkflowRun:
		return r.Spec.Env
	}
	return nil
}

func hubTokenRef(env []corev1.EnvVar) *corev1.EnvVar {
	for i := range env {
		if env[i].Name == hubTokenEnvVar {
			return &env[i]
		}
	}
	return nil
}

func TestCreateRun_StoresTokenInSecret(t *testing.T) {
	for _, kind := range []string{kindAgentRun, kindAgentWorkflowRun} {
		t.Run(kind, func(t *testing.T) {
			run := newRun(kind)
			cl := fake.NewClientBuilder().WithScheme(scheme()).Build()
			rc := runContext{appID: "10", hubToken: testHubToken}
			if err := rc.createRun(context.Background(), cl, run); err != nil {
				t.Fatalf("createRun: %v", err)
			}

			// The PAT must not appear anywhere in the run spec.
			encoded, _ := json.Marshal(run)
			if strings.Contains(string(encoded), testHubToken) {
				t.Fatal("PAT leaked into the run spec")
			}

			ref := hubTokenRef(runEnv(run))
			if ref == nil || ref.Value != "" || ref.ValueFrom == nil || ref.ValueFrom.SecretKeyRef == nil {
				t.Fatalf("HUB_TOKEN is not a SecretKeyRef: %#v", ref)
			}
			if ref.ValueFrom.SecretKeyRef.Key != hubTokenEnvVar {
				t.Errorf("SecretKeyRef key = %q, want %q", ref.ValueFrom.SecretKeyRef.Key, hubTokenEnvVar)
			}

			var secrets corev1.SecretList
			if err := cl.List(context.Background(), &secrets); err != nil {
				t.Fatal(err)
			}
			if len(secrets.Items) != 1 {
				t.Fatalf("expected one token Secret, got %d", len(secrets.Items))
			}
			s := secrets.Items[0]
			if s.Name != ref.ValueFrom.SecretKeyRef.Name {
				t.Errorf("Secret name %q != reference %q", s.Name, ref.ValueFrom.SecretKeyRef.Name)
			}
			if string(s.Data[hubTokenEnvVar]) != testHubToken {
				t.Errorf("Secret token = %q, want %q", s.Data[hubTokenEnvVar], testHubToken)
			}
			if len(s.OwnerReferences) != 1 || s.OwnerReferences[0].UID != run.GetUID() || s.OwnerReferences[0].Kind != kind {
				t.Errorf("Secret owner = %#v, want owned by %s run", s.OwnerReferences, kind)
			}
		})
	}
}

func TestCreateRun_NoTokenNoSecret(t *testing.T) {
	run := newRun(kindAgentRun)
	cl := fake.NewClientBuilder().WithScheme(scheme()).Build()
	rc := runContext{} // no --app, no token
	if err := rc.createRun(context.Background(), cl, run); err != nil {
		t.Fatalf("createRun: %v", err)
	}
	if ref := hubTokenRef(runEnv(run)); ref != nil {
		t.Error("HUB_TOKEN should be absent when no token is resolved")
	}
	var secrets corev1.SecretList
	if err := cl.List(context.Background(), &secrets); err != nil {
		t.Fatal(err)
	}
	if len(secrets.Items) != 0 {
		t.Errorf("expected no Secret, got %d", len(secrets.Items))
	}
}

func TestCreateRun_EnvTokenWithoutApp(t *testing.T) {
	// --env HUB_TOKEN=... with no --app must still go to a Secret, not the spec.
	run := newRun(kindAgentRun)
	cl := fake.NewClientBuilder().WithScheme(scheme()).Build()
	rc := runContext{env: []string{hubTokenEnvVar + "=from-env"}}
	if err := rc.createRun(context.Background(), cl, run); err != nil {
		t.Fatalf("createRun: %v", err)
	}
	ref := hubTokenRef(runEnv(run))
	if ref == nil || ref.ValueFrom == nil || ref.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("HUB_TOKEN from --env is not a SecretKeyRef: %#v", ref)
	}
	var secrets corev1.SecretList
	_ = cl.List(context.Background(), &secrets)
	if len(secrets.Items) != 1 || string(secrets.Items[0].Data[hubTokenEnvVar]) != "from-env" {
		t.Fatalf("env token not stored in Secret: %#v", secrets.Items)
	}
}

func TestCreateRun_RunFailureRollsBackSecret(t *testing.T) {
	run := newRun(kindAgentRun)
	unrelated := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "keep", Namespace: secretTestNS},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	sentinel := errors.New("injected run-create failure")
	cl := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(unrelated).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*agenticv1alpha1.AgentRun); ok {
					return sentinel
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()

	rc := runContext{appID: "10", hubToken: testHubToken}
	err := rc.createRun(context.Background(), cl, run)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the run-create error, got %v", err)
	}
	var secrets corev1.SecretList
	if err := cl.List(context.Background(), &secrets); err != nil {
		t.Fatal(err)
	}
	// Only the unrelated Secret must remain: the token Secret was rolled back.
	if len(secrets.Items) != 1 || secrets.Items[0].Name != "keep" {
		t.Fatalf("token Secret was not rolled back: %#v", secrets.Items)
	}
}

func TestCreateRun_SecretFailureAbortsRun(t *testing.T) {
	run := newRun(kindAgentRun)
	sentinel := errors.New("injected secret-create failure")
	cl := fake.NewClientBuilder().WithScheme(scheme()).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*corev1.Secret); ok {
					return sentinel
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()

	rc := runContext{appID: "10", hubToken: testHubToken}
	if err := rc.createRun(context.Background(), cl, run); !errors.Is(err, sentinel) {
		t.Fatalf("expected the secret-create error, got %v", err)
	}
	// The run must not have been created.
	fetched := &agenticv1alpha1.AgentRun{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(run), fetched); err == nil {
		t.Fatal("run should not exist after Secret creation failed")
	}
}
