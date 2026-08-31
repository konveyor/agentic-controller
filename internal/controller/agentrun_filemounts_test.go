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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// Shared file-mount test fixtures, kept as constants so goconst stays
// quiet across the controller package's test files.
const (
	testMountSecretPath = "/etc/jira/config.yaml"
	testMountCMPath     = "/etc/app"
	testMountBadPath    = "/etc/x"
	testFileMountAgent  = "some-agent"
	testMountCMName     = "app-config"
	testMountKey        = "config.yaml"
	testMountSecretName = "jira-creds"
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
		"exact skills root":   skillsDir,
		"under skills root":   "/opt/skills/evil",
		"exact params dir":    "/run/konveyor",
		"under params dir":    ParamsFilePath,
		"exact workspace":     "/workspace",
		"under tmp":           "/tmp/foo",
		"ancestor of a mount": "/opt", // /opt contains /opt/skills
		"root contains all":   "/",
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
					MountPath:     testMountCMPath,
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
