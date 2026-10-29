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

// Package manifests holds tests that guard cross-repo manifest consistency.
package manifests

import (
	"bytes"
	"errors"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// The operator ships its own rendering of the controller Deployment as a Jinja
// template (config/operator/deployment.yaml.j2), synced verbatim into
// konveyor/operator by hack/sync-operator.sh. That template is a hand-maintained
// mirror of the container spec in config/manager/manager.yaml -- the sync copies
// it as-is, it is NOT generated, so the two can silently drift.
//
// This test is the guard: it extracts containers[0] from each Deployment and
// asserts they are equal once the fields that are *intentionally* allowed to
// differ are normalized away. If someone changes a probe, resource limit, port,
// arg, command, env, or securityContext in manager.yaml without updating the
// .j2 (or vice versa), this fails -- turning silent drift into a red build.
// Resource variables are substituted with the operator role defaults; requests
// and limits remain part of the comparison. Operator tests exercise actual
// Ansible rendering with both role defaults and Tackle CR overrides.
//
// The intentional differences (see normalizeContainer) are the templated image,
// the SKILL_LOADER_IMAGE value that tracks it, the serving-certificate mount, and the metrics bind/certificate args
// (a deployment-environment choice: kustomize enables HTTPS metrics via a patch,
// the operator enables it, and the base manager.yaml sets neither).

const (
	managerManifest    = "../../config/manager/manager.yaml"
	operatorDeployment = "../../config/operator/deployment.yaml.j2"
)

// jinjaExpr matches a `{{ ... }}` Jinja substitution so it can be replaced with
// a plain scalar, making the operator template parseable as YAML.
var jinjaExpr = regexp.MustCompile(`\{\{[^}]*\}\}`)

func TestDeploymentContainerParity(t *testing.T) {
	manager := managerContainer(t, managerManifest, false)
	operator := managerContainer(t, operatorDeployment, true)

	got := normalizeContainer(operator)
	want := normalizeContainer(manager)

	if !reflect.DeepEqual(want, got) {
		t.Errorf("controller container spec has drifted between %s and %s.\n"+
			"Update config/operator/deployment.yaml.j2 to match config/manager/manager.yaml "+
			"(or vice versa), keeping the container spec in step.\n\n"+
			"--- manager.yaml (normalized) ---\n%s\n"+
			"--- deployment.yaml.j2 (normalized) ---\n%s",
			managerManifest, operatorDeployment, mustYAML(t, want), mustYAML(t, got))
	}
}

// managerContainer reads a manifest, optionally strips Jinja so an operator
// template parses as YAML, and returns containers[0] of its Deployment doc.
func managerContainer(t *testing.T, path string, stripJinja bool) corev1.Container {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if stripJinja {
		// These defaults match roles/tackle/defaults/main.yml in the operator.
		// Require resource and diagnostics variables so hardcoding a setting
		// cannot silently remove its Tackle CR override.
		templateDefaults := map[string]string{
			"agentic_container_limits_cpu":                                                "500m",
			"agentic_container_limits_memory":                                             "400Mi",
			"agentic_container_requests_cpu":                                              "10m",
			"agentic_container_requests_memory":                                           "64Mi",
			"agentic_metrics_bind_address":                                                ":8443",
			"agentic_pprof_bind_address":                                                  "0",
			"agentic_memory_log_interval":                                                 "30s",
			"(not (openshift_cluster | bool and agentic_metrics_enabled | bool)) | lower": "true",
		}
		raw = jinjaExpr.ReplaceAllFunc(raw, func(expr []byte) []byte {
			name := strings.TrimSpace(string(expr[2 : len(expr)-2]))
			if value, ok := templateDefaults[name]; ok {
				delete(templateDefaults, name)
				return []byte(value)
			}
			return []byte("j2placeholder")
		})
		if len(templateDefaults) != 0 {
			t.Fatalf("operator template must expose all resource and diagnostics settings; missing: %v", templateDefaults)
		}
	}

	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	for {
		var dep appsv1.Deployment
		if err := decoder.Decode(&dep); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("parse a document in %s: %v", path, err)
		}
		if dep.Kind != "Deployment" {
			continue
		}
		containers := dep.Spec.Template.Spec.Containers
		if len(containers) == 0 {
			t.Fatalf("Deployment in %s has no containers", path)
		}
		return containers[0]
	}

	t.Fatalf("no Deployment document found in %s", path)
	return corev1.Container{}
}

// normalizeContainer zeroes the fields that are allowed to differ between the
// kustomize base and the operator template, so a DeepEqual of the result
// catches every *unintended* difference. This is a small allowlist of known
// divergences; each entry weakens the test's discriminating power, so if it
// grows past a handful the right fix is to stop maintaining two copies of the
// container spec — feed both the manager overlay and the operator template from
// one shared kustomize base — rather than adding more normalization here.
func normalizeContainer(c corev1.Container) corev1.Container {
	// The image is templated to a digest in the operator ({{ agentic_fqin }}).
	c.Image = ""
	// SKILL_LOADER_IMAGE deliberately tracks the image, so it is templated too.
	for i := range c.Env {
		if c.Env[i].Name == "SKILL_LOADER_IMAGE" {
			c.Env[i].Value = ""
		}
	}
	// The metrics endpoint is a deployment-environment choice, not part of the
	// controller's spec: kustomize enables HTTPS metrics via a patch (:8443),
	// the operator enables it, and the base manager.yaml sets neither.
	c.Args = dropArgs(c.Args, "--metrics-bind-address")
	c.Args = dropArgs(c.Args, "--metrics-cert-path")
	// The operator uses an OpenShift service-serving certificate. Standalone
	// TLS is configured by the Kustomize overlay instead.
	mounts := c.VolumeMounts[:0]
	for _, mount := range c.VolumeMounts {
		if mount.Name != "metrics-certs" {
			mounts = append(mounts, mount)
		}
	}
	c.VolumeMounts = mounts
	// Treat empty and nil slices as equal so `volumeMounts: []` matches its
	// absence.
	if len(c.VolumeMounts) == 0 {
		c.VolumeMounts = nil
	}
	return c
}

// dropArgs returns args with every entry that has the given prefix removed.
func dropArgs(args []string, prefix string) []string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			continue
		}
		out = append(out, a)
	}
	return out
}

func mustYAML(t *testing.T, v any) string {
	t.Helper()
	out, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshal for diff: %v", err)
	}
	return string(out)
}
