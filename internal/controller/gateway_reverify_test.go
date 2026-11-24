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
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	reverifyGatewayName = "gw-reverify"
	reverifySecretName  = "gw-reverify-secret"
	reverifySecretKey   = "api-key"
)

// reverifyFixture builds a Gateway that already reached the given Ready
// reason for its current generation and credential, the way a finished
// verification leaves it, together with the Secret it points at.
func reverifyFixture(reason string, settledAt time.Time) (*konveyoriov1alpha1.Gateway, *corev1.Secret) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: reverifySecretName, Namespace: testPodNamespace},
		Data:       map[string][]byte{reverifySecretKey: []byte("sk-test")},
	}
	gateway := &konveyoriov1alpha1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:       reverifyGatewayName,
			Namespace:  testPodNamespace,
			Generation: 1,
			UID:        types.UID("gw-reverify-uid"),
		},
		Spec: konveyoriov1alpha1.GatewaySpec{
			Provider: "openai",
			Endpoint: "http://llm.example.svc.cluster.local",
			CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{
				SecretName: reverifySecretName,
				Key:        reverifySecretKey,
			},
			Model: konveyoriov1alpha1.GatewayModel{Name: "m", ContextWindow: 1000},
		},
		Status: konveyoriov1alpha1.GatewayStatus{
			ObservedGeneration:     1,
			VerifiedCredentialHash: credentialHash(konveyoriov1alpha1.GatewayCredentialRef{SecretName: reverifySecretName, Key: reverifySecretKey}, secret),
			Conditions: []metav1.Condition{{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: 1,
				Reason:             reason,
				Message:            "settled",
				LastTransitionTime: metav1.NewTime(settledAt),
			}},
		},
	}
	return gateway, secret
}

func reverifyReconciler(t *testing.T, objs ...client.Object) (*GatewayReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, batchv1.AddToScheme, konveyoriov1alpha1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatalf("add to scheme: %v", err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&konveyoriov1alpha1.Gateway{}).
		Build()
	return &GatewayReconciler{Client: c, Scheme: scheme, apiReader: c}, c
}

func verificationJobs(t *testing.T, c client.Client) []batchv1.Job {
	t.Helper()
	var jobs batchv1.JobList
	if err := c.List(context.Background(), &jobs, client.InNamespace(testPodNamespace)); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	return jobs.Items
}

// A Gateway applied alongside the service it points at loses the race: the
// probe runs before the endpoint is up and the Gateway lands on
// EndpointUnreachable. That is not a verdict about the Gateway, so once the
// retry interval has passed the controller probes it again (#246).
func TestGatewayReprobesAnUnreachableEndpoint(t *testing.T) {
	gateway, secret := reverifyFixture(reasonEndpointUnreachable, time.Now().Add(-2*gatewayReverifyInterval))
	r, c := reverifyReconciler(t, gateway, secret)

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gateway)})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0 once a fresh probe has been started", res.RequeueAfter)
	}
	if got := len(verificationJobs(t, c)); got != 1 {
		t.Fatalf("verification Jobs = %d, want 1 new probe", got)
	}
	var fetched konveyoriov1alpha1.Gateway
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(gateway), &fetched); err != nil {
		t.Fatalf("get gateway: %v", err)
	}
	ready := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
	if ready == nil || ready.Reason != reasonVerifying {
		t.Errorf("Ready = %+v, want reason Verifying", ready)
	}
}

// Within the interval the failure stands and no Job is created, but the
// reconcile asks to come back, since nothing else would wake a settled
// Gateway.
func TestGatewayWaitsOutTheReprobeInterval(t *testing.T) {
	gateway, secret := reverifyFixture(reasonEndpointUnreachable, time.Now().Add(-gatewayReverifyInterval/4))
	r, c := reverifyReconciler(t, gateway, secret)

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gateway)})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter <= 0 || res.RequeueAfter > gatewayReverifyInterval {
		t.Errorf("RequeueAfter = %v, want a wait of at most %v", res.RequeueAfter, gatewayReverifyInterval)
	}
	if got := len(verificationJobs(t, c)); got != 0 {
		t.Errorf("verification Jobs = %d, want none before the interval has passed", got)
	}
}

// Verdicts that describe the Gateway rather than the network stay settled:
// a bad credential or an HTTP error from a reachable endpoint will not fix
// itself, and a verified Gateway has nothing to retry.
func TestGatewayKeepsOtherSettledVerdicts(t *testing.T) {
	for _, reason := range []string{
		reasonConnectionVerified, reasonConnectionFailed, reasonAuthenticationFailed,
	} {
		t.Run(reason, func(t *testing.T) {
			gateway, secret := reverifyFixture(reason, time.Now().Add(-24*time.Hour))
			r, c := reverifyReconciler(t, gateway, secret)

			res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gateway)})
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if res.RequeueAfter != 0 {
				t.Errorf("RequeueAfter = %v, want 0", res.RequeueAfter)
			}
			if got := len(verificationJobs(t, c)); got != 0 {
				t.Errorf("verification Jobs = %d, want none for a settled %s", got, reason)
			}
		})
	}
}

// A probe that fails the same way again has to restart the wait. The Ready
// condition keeps its transition time while Status stays False, so without
// stamping it the next reconcile would see an old timestamp and probe again
// straight away, in a loop.
func TestUnreachableOutcomeRestartsTheReprobeWait(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	gateway, secret := reverifyFixture(reasonVerifying, old)
	r, c := reverifyReconciler(t, gateway, secret)

	hash := gateway.Status.VerifiedCredentialHash
	jobName := verificationJobName(gateway, hash)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: testPodNamespace, UID: "job-uid"},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobFailed, Status: corev1.ConditionTrue,
		}}},
	}
	if err := c.Create(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: jobName + "-abc", Namespace: testPodNamespace,
			Labels: map[string]string{
				batchv1.JobNameLabel:       jobName,
				batchv1.ControllerUidLabel: "job-uid",
			},
		},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name: "verify",
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				ExitCode: 1, Message: "unreachable rc=7",
			}},
		}}},
	}
	if err := c.Create(context.Background(), pod); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gateway)}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var fetched konveyoriov1alpha1.Gateway
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(gateway), &fetched); err != nil {
		t.Fatalf("get gateway: %v", err)
	}
	ready := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
	if ready == nil || ready.Reason != reasonEndpointUnreachable {
		t.Fatalf("Ready = %+v, want reason %s", ready, reasonEndpointUnreachable)
	}
	if age := time.Since(ready.LastTransitionTime.Time); age > time.Minute {
		t.Errorf("Ready transition time is %v old, want it stamped when the probe finished", age)
	}
}
