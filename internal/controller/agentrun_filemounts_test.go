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
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// Shared file-mount test fixtures, kept as constants so goconst stays
// quiet across the controller package's test files.
const (
	testMountSecretPath    = "/etc/jira/config.yaml"
	testMountCMPath        = "/etc/app"
	testMountBadPath       = "/etc/x"
	testFileMountAgent     = "some-agent"
	testMountCMName        = "app-config"
	testMountKey           = "config.yaml"
	testMountSecretName    = "jira-creds"
	testMountLookupError   = "lookup error"
	testMountSecretSource  = "secret"
	testMountMissingSource = "missing source"
)

func TestValidateFileMountsAcceptsValidMounts(t *testing.T) {
	mounts := []konveyoriov1alpha1.FileMount{
		{SecretName: testMountSecretName, MountPath: testMountSecretPath, SubPath: testMountKey},
		{ConfigMapName: testMountCMName, MountPath: testMountCMPath},
	}
	if err := validateFileMounts(mounts); err != nil {
		t.Fatalf("validateFileMounts() = %v, want nil", err)
	}
}

func TestValidateFileMountsRejectsMissingAndBothSources(t *testing.T) {
	tests := map[string]konveyoriov1alpha1.FileMount{
		"neither source": {MountPath: testMountBadPath},
		"both sources":   {SecretName: "s", ConfigMapName: "c", MountPath: testMountBadPath},
	}
	for name, m := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateFileMounts([]konveyoriov1alpha1.FileMount{m})
			if err == nil {
				t.Fatal("validateFileMounts() = nil, want error")
			}
			if !strings.Contains(err.Error(), "exactly one of") {
				t.Errorf("error = %q, want it to mention 'exactly one of'", err)
			}
		})
	}
}

func TestValidateFileMountsRejectsRelativePath(t *testing.T) {
	err := validateFileMounts([]konveyoriov1alpha1.FileMount{
		{SecretName: "s", MountPath: "etc/relative"},
	})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("validateFileMounts() = %v, want an 'absolute' error", err)
	}
}

// A user mount must not land on, under, or above any controller-managed
// mount — each would shadow the skills root, params.json, or workspace.
func TestValidateFileMountsRejectsReservedCollisions(t *testing.T) {
	tests := map[string]string{
		"service account token": "/var/run/secrets/kubernetes.io/serviceaccount",
		"token ancestor":        "/var/run/secrets",
		"token child":           "/var/run/secrets/kubernetes.io/serviceaccount/token",
		"exact skills root":     skillsDir,
		"under skills root":     "/opt/skills/evil",
		"exact params dir":      "/run/konveyor",
		"under params dir":      ParamsFilePath,
		"exact workspace":       "/workspace",
		"under tmp":             "/tmp/foo",
		"ancestor of a mount":   "/opt", // /opt contains /opt/skills
		"root contains all":     "/",
	}
	for name, mountPath := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateFileMounts([]konveyoriov1alpha1.FileMount{
				{SecretName: "s", MountPath: mountPath},
			})
			if err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Fatalf("validateFileMounts(%q) = %v, want a 'reserved' collision error", mountPath, err)
			}
		})
	}
}

func TestValidateFileMountsAllowsSiblingOfReservedPath(t *testing.T) {
	// /opt/skills-extra is neither under nor above /opt/skills.
	err := validateFileMounts([]konveyoriov1alpha1.FileMount{
		{SecretName: "s", MountPath: "/opt/skills-extra"},
	})
	if err != nil {
		t.Fatalf("validateFileMounts() = %v, want nil for a sibling path", err)
	}
}

func TestPathsCollide(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{skillsDir, skillsDir, true},
		{"/opt/skills/x", skillsDir, true},
		{"/opt", skillsDir, true},
		{"/opt/skills-extra", skillsDir, false},
		{"/etc/jira", skillsDir, false},
		{ParamsFilePath, "/run/konveyor", true},
	}
	for _, tt := range tests {
		if got := pathsCollide(tt.a, tt.b); got != tt.want {
			t.Errorf("pathsCollide(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestFileMountVolumesEmpty(t *testing.T) {
	vols, mounts := fileMountVolumes(&konveyoriov1alpha1.AgentRun{})
	if vols != nil || mounts != nil {
		t.Fatalf("fileMountVolumes(empty) = (%v, %v), want (nil, nil)", vols, mounts)
	}
}

func TestFileMountVolumesBuildsSecretAndConfigMapSources(t *testing.T) {
	run := &konveyoriov1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run"},
		Spec: konveyoriov1alpha1.AgentRunSpec{
			FileMounts: []konveyoriov1alpha1.FileMount{
				{
					SecretName: testMountSecretName,
					MountPath:  testMountSecretPath,
					SubPath:    testMountKey,
					Items:      []corev1.KeyToPath{{Key: testMountKey, Path: testMountKey}},
				},
				{
					ConfigMapName: testMountCMName,
					MountPath:     testMountCMPath + "/./",
				},
			},
		},
	}

	vols, mounts := fileMountVolumes(run)
	if len(vols) != 2 || len(mounts) != 2 {
		t.Fatalf("got %d volumes / %d mounts, want 2 / 2", len(vols), len(mounts))
	}

	// Volume names are index-derived and must line up with their mounts.
	if vols[0].Name != mounts[0].Name || vols[1].Name != mounts[1].Name {
		t.Fatalf("volume/mount names misaligned: %+v vs %+v", vols, mounts)
	}

	// First: Secret source with items, mounted single-file via subPath, read-only.
	if vols[0].Secret == nil {
		t.Fatalf("mount 0 volume = %+v, want a Secret source", vols[0].VolumeSource)
	}
	if vols[0].Secret.SecretName != testMountSecretName {
		t.Errorf("secretName = %q, want jira-creds", vols[0].Secret.SecretName)
	}
	if len(vols[0].Secret.Items) != 1 || vols[0].Secret.Items[0].Key != testMountKey {
		t.Errorf("items = %+v, want one config.yaml entry", vols[0].Secret.Items)
	}
	if mounts[0].MountPath != testMountSecretPath || mounts[0].SubPath != testMountKey {
		t.Errorf("mount 0 = %+v, want /etc/jira/config.yaml subPath config.yaml", mounts[0])
	}
	if !mounts[0].ReadOnly {
		t.Error("mount 0 is not read-only")
	}

	// Second: ConfigMap source, whole-object directory mount, read-only.
	if vols[1].ConfigMap == nil {
		t.Fatalf("mount 1 volume = %+v, want a ConfigMap source", vols[1].VolumeSource)
	}
	if vols[1].ConfigMap.Name != testMountCMName {
		t.Errorf("configMapName = %q, want app-config", vols[1].ConfigMap.Name)
	}
	if mounts[1].MountPath != testMountCMPath || mounts[1].SubPath != "" {
		t.Errorf("mount 1 = %+v, want /etc/app whole-directory mount", mounts[1])
	}
	if !mounts[1].ReadOnly {
		t.Error("mount 1 is not read-only")
	}
}

func TestValidateFileMountsRejectsEquivalentPaths(t *testing.T) {
	for _, other := range []string{testMountCMPath, testMountCMPath + "/", "/etc/other/../app"} {
		t.Run(other, func(t *testing.T) {
			err := validateFileMounts([]konveyoriov1alpha1.FileMount{
				{SecretName: "s", MountPath: testMountCMPath},
				{ConfigMapName: "c", MountPath: other},
			})
			if err == nil {
				t.Fatal("equivalent mount paths accepted")
			}
		})
	}
}

func TestReconcileInvalidFileMountSources(t *testing.T) {
	for _, source := range []string{testMountSecretSource, "configmap"} {
		for _, scenario := range []string{testMountMissingSource, "missing item", "missing subPath", "unselected subPath", testMountLookupError} {
			t.Run(source+"/"+scenario, func(t *testing.T) {
				ctx := context.Background()
				scheme := runtime.NewScheme()
				for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, konveyoriov1alpha1.AddToScheme, sandboxv1beta1.AddToScheme} {
					if err := add(scheme); err != nil {
						t.Fatal(err)
					}
				}
				agent := &konveyoriov1alpha1.Agent{
					ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: testNamespace},
					Spec:       konveyoriov1alpha1.AgentSpec{Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: "gateway"}}},
					Status:     konveyoriov1alpha1.AgentStatus{Conditions: []metav1.Condition{{Type: ConditionTypeReady, Status: metav1.ConditionTrue}}},
				}
				mount := konveyoriov1alpha1.FileMount{MountPath: testMountCMPath}
				if source == testMountSecretSource {
					mount.SecretName = testMountCMName
				} else {
					mount.ConfigMapName = testMountCMName
				}
				switch scenario {
				case "missing item":
					mount.Items = []corev1.KeyToPath{{Key: "absent", Path: "file"}}
				case "missing subPath":
					mount.SubPath = "absent"
				case "unselected subPath":
					mount.Items = []corev1.KeyToPath{{Key: testMountKey, Path: "renamed"}}
					mount.SubPath = testMountKey
				}
				run := &konveyoriov1alpha1.AgentRun{
					ObjectMeta: metav1.ObjectMeta{Name: "filemount-run", Namespace: testNamespace},
					Spec:       konveyoriov1alpha1.AgentRunSpec{AgentRef: agent.Name, FileMounts: []konveyoriov1alpha1.FileMount{mount}},
				}
				gateway := &konveyoriov1alpha1.Gateway{
					ObjectMeta: metav1.ObjectMeta{Name: "gateway", Namespace: testNamespace},
					Status:     konveyoriov1alpha1.GatewayStatus{Conditions: []metav1.Condition{{Type: ConditionTypeReady, Status: metav1.ConditionTrue}}},
				}
				objects := []client.Object{agent, run, gateway}
				if scenario != testMountMissingSource {
					objects = append(objects,
						&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: testMountCMName, Namespace: testNamespace}, Data: map[string][]byte{testMountKey: []byte("private-value")}},
						&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: testMountCMName, Namespace: testNamespace}, Data: map[string]string{testMountKey: "private-value"}},
					)
				}
				c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(run).WithObjects(objects...).
					WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if scenario == testMountLookupError && key.Name == testMountCMName {
							return fmt.Errorf("API unavailable")
						}
						return c.Get(ctx, key, obj, opts...)
					}}).Build()
				r := &AgentRunReconciler{Client: c, Scheme: scheme}
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)})
				if scenario == testMountLookupError {
					if err == nil || !strings.Contains(err.Error(), "API unavailable") {
						t.Fatalf("expected retryable lookup error, got %v", err)
					}
				} else if scenario == testMountMissingSource {
					if !apierrors.IsNotFound(err) {
						t.Fatalf("expected retryable NotFound, got %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(run), run); err != nil {
					t.Fatal(err)
				}
				assertFileMountSourceStatus(t, run, scenario)
				var sandboxes sandboxv1beta1.SandboxList
				if err := c.List(ctx, &sandboxes); err != nil {
					t.Fatal(err)
				}
				if len(sandboxes.Items) != 0 {
					t.Fatal("created Sandbox for invalid file mounts")
				}
				if scenario == testMountMissingSource {
					assertFileMountSourceRecovery(t, r, run, source)
				}
			})
		}
	}
}

func TestValidateFileMountSourcesAcceptsProjectedPaths(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: testMountCMName, Namespace: testNamespace}, Data: map[string][]byte{testMountKey: []byte("private")}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: testMountCMName, Namespace: testNamespace}, BinaryData: map[string][]byte{testMountKey: {0, 1}}},
	).Build()
	r := &AgentRunReconciler{Client: c}
	for _, source := range []string{testMountSecretSource, "configmap"} {
		for _, subPath := range []string{"", testMountKey, "nested/renamed", "nested"} {
			t.Run(source+"/"+subPath, func(t *testing.T) {
				mount := konveyoriov1alpha1.FileMount{MountPath: testMountCMPath, SubPath: subPath}
				if source == testMountSecretSource {
					mount.SecretName = testMountCMName
				} else {
					mount.ConfigMapName = testMountCMName
				}
				if strings.HasPrefix(subPath, "nested") {
					mount.Items = []corev1.KeyToPath{{Key: testMountKey, Path: "nested/renamed"}}
				}
				run := &konveyoriov1alpha1.AgentRun{
					ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace},
					Spec:       konveyoriov1alpha1.AgentRunSpec{FileMounts: []konveyoriov1alpha1.FileMount{mount}},
				}
				message, err := r.validateFileMountSources(context.Background(), run)
				if err != nil || message != "" {
					t.Fatalf("valid projection rejected: %s, %v", message, err)
				}
			})
		}
	}
}

// Creating an ordinary user source must unblock the original run on retry;
// these sources do not carry controller management labels or owner references.
func assertFileMountSourceRecovery(t *testing.T, r *AgentRunReconciler, run *konveyoriov1alpha1.AgentRun, source string) {
	t.Helper()
	ctx := context.Background()
	metadata := metav1.ObjectMeta{Name: testMountCMName, Namespace: run.Namespace}
	var obj client.Object = &corev1.ConfigMap{ObjectMeta: metadata}
	if source == testMountSecretSource {
		obj = &corev1.Secret{ObjectMeta: metadata}
	}
	if err := r.Create(ctx, obj); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(run), run); err != nil {
		t.Fatal(err)
	}
	if run.Status.SandboxName == "" || isTerminalPhase(run.Status.Phase) {
		t.Fatalf("run did not recover after source creation: %+v", run.Status)
	}
}

func TestValidateFileMountsRejectsNestedUserMounts(t *testing.T) {
	for _, paths := range [][]string{
		{testMountCMPath, testMountCMPath + "/creds.json"},
		{testMountCMPath + "/creds.json", testMountCMPath},
		{testMountCMPath, testMountCMPath + "/sub/../creds.json"},
	} {
		t.Run(strings.Join(paths, ","), func(t *testing.T) {
			err := validateFileMounts([]konveyoriov1alpha1.FileMount{
				{ConfigMapName: testMountCMName, MountPath: paths[0]},
				{SecretName: testMountSecretName, MountPath: paths[1]},
			})
			if err == nil {
				t.Fatal("nested user mounts accepted")
			}
		})
	}
}

func TestValidateFileMountsItemPaths(t *testing.T) {
	for _, itemPath := range []string{"../../escape", "/abs/path", "nested/../escape", ""} {
		t.Run(itemPath, func(t *testing.T) {
			err := validateFileMounts([]konveyoriov1alpha1.FileMount{{
				ConfigMapName: testMountCMName, MountPath: testMountCMPath,
				Items: []corev1.KeyToPath{{Key: testMountKey, Path: itemPath}},
			}})
			if err == nil || !strings.Contains(err.Error(), "items path") {
				t.Fatalf("expected invalid items path, got %v", err)
			}
		})
	}
}

func TestValidateFileMountsAllowsSiblingUserMounts(t *testing.T) {
	err := validateFileMounts([]konveyoriov1alpha1.FileMount{
		{ConfigMapName: testMountCMName, MountPath: testMountCMPath},
		{SecretName: testMountSecretName, MountPath: testMountCMPath + "-credentials"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertFileMountSourceStatus(t *testing.T, run *konveyoriov1alpha1.AgentRun, scenario string) {
	t.Helper()
	cond := meta.FindStatusCondition(run.Status.Conditions, konveyoriov1alpha1.AgentRunConditionSucceeded)
	switch scenario {
	case testMountLookupError:
		if isTerminalPhase(run.Status.Phase) || cond == nil || cond.Status != metav1.ConditionUnknown || cond.Reason != "FileMountSourceUnavailable" {
			t.Fatalf("expected non-terminal source lookup condition, got %+v", run.Status)
		}
	case testMountMissingSource:
		if isTerminalPhase(run.Status.Phase) || cond == nil || cond.Status != metav1.ConditionUnknown || cond.Reason != "FileMountSourceNotFound" {
			t.Fatalf("expected non-terminal missing-source condition, got %+v", run.Status)
		}
	default:
		if run.Status.Phase != konveyoriov1alpha1.AgentRunPhaseFailed || cond == nil || cond.Reason != "InvalidFileMounts" || cond.Status != metav1.ConditionFalse {
			t.Fatalf("expected terminal InvalidFileMounts, got %+v", run.Status)
		}
		if strings.Contains(cond.Message, "private-value") {
			t.Fatal("source value leaked into status")
		}
	}
}
