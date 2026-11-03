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

package ci

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	yaml "sigs.k8s.io/yaml/goyaml.v3"
)

func TestGoModuleDownloadRetries(t *testing.T) {
	script, err := filepath.Abs("../../hack/go-mod-download.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name     string
		failures int
		attempts int
		exitCode int
		backoff  string
	}{
		{name: "warm cache", attempts: 1},
		{name: "transient failure", failures: 2, attempts: 3, backoff: "5\n10\n"},
		{name: "persistent failure", failures: 9, attempts: 3, exitCode: 7, backoff: "5\n10\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "go"), `#!/bin/sh
echo "$*" >> "$RETRY_STATE/attempts"
attempt=$(wc -l < "$RETRY_STATE/attempts")
if [ "$attempt" -le "$DOWNLOAD_FAILURES" ]; then
    echo 'stream error: INTERNAL_ERROR; received from peer' >&2
    exit 7
fi
`, 0o755)
			writeFile(t, filepath.Join(dir, "sleep"), "#!/bin/sh\necho \"$1\" >> \"$RETRY_STATE/backoff\"\n", 0o755)
			cmd := exec.Command("sh", script, "-x")
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"RETRY_STATE="+dir, fmt.Sprintf("DOWNLOAD_FAILURES=%d", tt.failures))
			out, runErr := cmd.CombinedOutput()
			if got := cmd.ProcessState.ExitCode(); got != tt.exitCode {
				t.Fatalf("exit = %d, want %d (%v): %s", got, tt.exitCode, runErr, out)
			}
			if got := readFile(t, filepath.Join(dir, "attempts")); got != strings.Repeat("mod download -x\n", tt.attempts) {
				t.Errorf("unexpected download calls: %q", got)
			}
			if tt.backoff != "" {
				if got := readFile(t, filepath.Join(dir, "backoff")); got != tt.backoff {
					t.Errorf("backoff = %q, want %q", got, tt.backoff)
				}
			} else if _, err := os.Stat(filepath.Join(dir, "backoff")); !os.IsNotExist(err) {
				t.Errorf("successful download should not sleep: %v", err)
			}
		})
	}
}

// Queue refs must run through merge_group exactly once, while ordinary PRs
// and pushes to supported branches retain their checks.
func TestQueueWorkflowTriggers(t *testing.T) {
	for _, name := range []string{"test.yml", "test-e2e.yml", "lint.yml"} {
		t.Run(name, func(t *testing.T) {
			var workflow struct {
				On map[string]struct {
					Branches []string `yaml:"branches"`
				} `yaml:"on"`
			}
			if err := yaml.Unmarshal([]byte(readFile(t, "../../.github/workflows/"+name)), &workflow); err != nil {
				t.Fatal(err)
			}
			for _, event := range []string{"pull_request", "merge_group"} {
				if _, ok := workflow.On[event]; !ok {
					t.Errorf("missing %s trigger", event)
				}
			}
			for branch, want := range map[string]bool{
				"main": true, "release-0.2": true, "feature/example": false,
				"gh-readonly-queue/main/pr-314-abcdef": false,
			} {
				push, ok := workflow.On["push"]
				matches := ok && (len(push.Branches) == 0 || slices.ContainsFunc(push.Branches, func(pattern string) bool {
					match, err := filepath.Match(pattern, branch)
					if err != nil {
						t.Fatal(err)
					}
					return match
				}))
				if matches != want {
					t.Errorf("push to %s triggers workflow = %t, want %t", branch, matches, want)
				}
			}
		})
	}
}

func TestRuleLogAssertionDrainsStream(t *testing.T) {
	// Run the real assertion pipeline without needing a cluster. A marker near
	// the start of a large stream exposes grep -q's SIGPIPE under pipefail.
	var assertion string
	for line := range strings.SplitSeq(readFile(t, "../../hack/run-e2e-rule.sh"), "\n") {
		if strings.HasPrefix(line, "if kc logs ") && strings.Contains(line, "always-loaded rules:") {
			assertion = strings.TrimSuffix(strings.TrimPrefix(line, "if "), "; then")
			break
		}
	}
	if assertion == "" {
		t.Fatal("could not find the rule log assertion")
	}
	for _, tt := range []struct {
		name     string
		marker   string
		producer string
		wantPass bool
	}{
		{name: "marker before large tail", marker: "always-loaded rules: rule-e2e-house-rules", wantPass: true},
		{name: "missing marker"},
		{name: "log retrieval fails", marker: "always-loaded rules: rule-e2e-house-rules", producer: "; return 13"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "logs")
			writeFile(t, logPath, tt.marker+"\n"+strings.Repeat("log output\n", 200000), 0o600)
			cmd := exec.Command("bash", "-o", "pipefail", "-c",
				"kc() { cat \"$LOG_FILE\""+tt.producer+"; }; run_pod() { echo agent-pod; }; "+assertion)
			cmd.Env = append(os.Environ(), "LOG_FILE="+logPath)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tt.wantPass {
				t.Fatalf("assertion error = %v, want pass = %t: %s", err, tt.wantPass, out)
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFile(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}
