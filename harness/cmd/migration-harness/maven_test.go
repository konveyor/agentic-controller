package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/konveyor/migration-harness/internal/hub"
)

func TestRunStagePreparesMavenSettings(t *testing.T) {
	const settings = `<settings><mirrors><mirror><id>private</id></mirror></mirrors></settings>`
	// Stop at a local clone failure: this exercises real startup without
	// launching a runtime or requiring Git/LLM services.
	source := filepath.Join(t.TempDir(), "missing-source.git")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-hub-token" {
			t.Error("Hub request did not use the harness token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/proxies" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if r.URL.Path == "/applications/42" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 42, "name": "maven-app",
				"repository": map[string]string{"url": source, "branch": "main"},
			})
			return
		}
		if r.URL.Query().Get("decrypted") != "1" {
			t.Error("identity lookup did not request decrypted fields")
		}
		switch r.URL.Path + "?" + r.URL.Query().Get("filter") {
		case "/applications/42/identities?role='source'", "/identities?kind='source',default=true":
			_, _ = w.Write([]byte(`[]`))
		case "/applications/42/identities?role='maven'":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7, "kind": "maven", "settings": settings}})
		default:
			t.Errorf("unexpected Hub request: %s", r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	for _, stage := range []string{"standalone", "1", "2", "3"} {
		t.Run(stage, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("KONVEYOR_LLM_MODEL", "test-model")
			t.Setenv("KONVEYOR_ACP_SECRET_KEY", "test-acp-key")
			t.Setenv("HUB_BASE_URL", server.URL)
			t.Setenv("HUB_TOKEN", "test-hub-token")
			t.Setenv("HUB_TOKEN_ID", "")
			t.Setenv("APP_ID", "42")
			t.Setenv("TARGET_BRANCH", "migration-test")
			t.Setenv("HARNESS_PARAMS_FILE", filepath.Join(home, "missing-params.json"))
			t.Setenv("HARNESS_WORK_DIR", filepath.Join(home, "repo"))
			t.Setenv("HARNESS_TERMINATION_LOG_PATH", filepath.Join(home, "termination-log"))
			t.Setenv("KONVEYOR_WORKFLOW_STAGE", "")
			t.Setenv("KONVEYOR_WORKFLOW_STAGE_COUNT", "")
			if stage != "standalone" {
				t.Setenv("KONVEYOR_WORKFLOW_STAGE", stage)
				t.Setenv("KONVEYOR_WORKFLOW_STAGE_COUNT", "3")
			}

			code, err := runStage(nil, nil)
			if code != 1 || err == nil || !strings.Contains(err.Error(), "clone:") {
				t.Fatalf("runStage = (%d, %v), want the deliberate clone failure", code, err)
			}
			data, err := os.ReadFile(filepath.Join(home, ".m2", "settings.xml"))
			if err != nil {
				t.Fatalf("Maven settings were not prepared during startup: %v", err)
			}
			if string(data) != settings {
				t.Error("Maven settings differ from the application's Hub credential")
			}
			info, err := os.Stat(filepath.Join(home, ".m2", "settings.xml"))
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Errorf("settings permissions = %o, want 600", info.Mode().Perm())
			}
		})
	}
}

func TestPrepareMavenSettingsWithoutUsableCredential(t *testing.T) {
	for _, tt := range []struct {
		name    string
		body    string
		status  int
		wantErr string
	}{
		{name: "no assigned or default credential", body: `[]`},
		{name: "empty assigned settings", body: `[{"id":7,"settings":"  "}]`, wantErr: "empty settings"},
		{name: "lookup forbidden", status: http.StatusForbidden, wantErr: "fetch Maven credential"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.status != 0 {
					w.WriteHeader(tt.status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			err := prepareMavenSettings(hub.NewClient(server.URL, "token"), "42", newRedactor())
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("no Maven credential should retain Maven defaults: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			if _, err := os.Stat(filepath.Join(home, ".m2")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("setup created a Maven directory without usable settings: %v", err)
			}
		})
	}
}

func TestPrepareMavenSettingsPreservesExistingFile(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "Secret mount symlink"}[symlink], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			path := filepath.Join(home, ".m2", "settings.xml")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			const settings = `<settings><localRepository>/custom/repository</localRepository></settings>`
			target := path
			if symlink {
				target = filepath.Join(home, "mounted-settings")
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(target, []byte(settings), 0o400); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("existing settings must take precedence without a Hub lookup")
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()

			if err := prepareMavenSettings(hub.NewClient(server.URL, "token"), "42", newRedactor()); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != settings {
				t.Error("explicit settings were overwritten")
			}
			if symlink {
				if _, err := os.Readlink(path); err != nil {
					t.Errorf("Secret mount symlink was replaced: %v", err)
				}
			}
		})
	}
}

func TestPrepareMavenSettingsRejectsInvalidPath(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "blocked parent", true: "settings is a directory"}[directory], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			path := filepath.Join(home, ".m2")
			if directory {
				if err := os.MkdirAll(filepath.Join(path, "settings.xml"), 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := prepareMavenSettings(nil, "42", newRedactor()); err == nil {
				t.Fatal("expected a filesystem error before fetching a credential")
			}
		})
	}
}

func TestPrepareMavenSettingsRejectsBrokenSymlink(t *testing.T) {
	for _, targetDirectory := range []bool{false, true} {
		t.Run(map[bool]string{false: "dangling Secret mount", true: "symlink to directory"}[targetDirectory], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			path := filepath.Join(home, ".m2", "settings.xml")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(home, "mounted-settings")
			if targetDirectory {
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("an invalid existing settings symlink must fail before a Hub lookup")
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()

			err := prepareMavenSettings(hub.NewClient(server.URL, "token"), "42", newRedactor())
			wantErr := "inspect Maven user settings"
			if targetDirectory {
				wantErr = "not a regular file"
			}
			if err == nil || !strings.Contains(err.Error(), wantErr) {
				t.Fatalf("error = %v, want %q", err, wantErr)
			}
			if !targetDirectory && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("error = %v, want the missing-target error", err)
			}
			if got, err := os.Readlink(path); err != nil || got != target {
				t.Errorf("settings symlink changed: target = %q, error = %v", got, err)
			}
		})
	}
}

func TestWriteMavenSettingsFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	closeErr := errors.New("close failed")
	for _, tt := range []struct {
		name         string
		writeErr     error
		closeErr     error
		blockCleanup bool
	}{
		{name: "write", writeErr: writeErr},
		{name: "close", closeErr: closeErr},
		{name: "write and close", writeErr: writeErr, closeErr: closeErr},
		{name: "write and cleanup", writeErr: writeErr, blockCleanup: true},
		{name: "close and cleanup", closeErr: closeErr, blockCleanup: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.xml")
			if tt.blockCleanup {
				// A nonempty directory makes os.Remove fail deterministically,
				// even when tests run as root.
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "blocker"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("partial settings"), 0o600); err != nil {
				t.Fatal(err)
			}
			f := &failingMavenSettingsWriter{writeErr: tt.writeErr, closeErr: tt.closeErr}
			const settings = "<settings>repository-password</settings>"
			err := writeMavenSettings(f, path, settings)
			if err == nil || !strings.Contains(err.Error(), "write Maven user settings") {
				t.Fatalf("error = %v, want settings write failure", err)
			}
			for _, cause := range []error{tt.writeErr, tt.closeErr} {
				if cause != nil && !errors.Is(err, cause) {
					t.Errorf("error = %v, want wrapped cause %v", err, cause)
				}
			}
			if !f.closed {
				t.Error("settings file was not closed after writing")
			}
			if strings.Contains(err.Error(), settings) {
				t.Error("error exposed the credential contents")
			}
			if tt.blockCleanup {
				var pathErr *os.PathError
				if !errors.As(err, &pathErr) || pathErr.Op != "remove" || pathErr.Path != path {
					t.Errorf("error = %v, want wrapped removal error for %s", err, path)
				}
				if !strings.Contains(err.Error(), "remove incomplete Maven user settings") {
					t.Errorf("error = %v, want explicit cleanup failure", err)
				}
			} else if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("incomplete settings were not removed: %v", err)
			}
		})
	}
}

type failingMavenSettingsWriter struct {
	writeErr error
	closeErr error
	closed   bool
}

func (f *failingMavenSettingsWriter) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}

func (f *failingMavenSettingsWriter) Close() error {
	f.closed = true
	return f.closeErr
}

func TestPrepareMavenSettingsDoesNotOverwriteConcurrentFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".m2", "settings.xml")
	const explicit = "<settings/>"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Materialize a settings file after the initial check but before the
		// credential arrives, exercising exclusive creation in the real setup.
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(path, []byte(explicit), 0o600); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":7,"settings":"<settings>from Hub</settings>"}]`))
	}))
	defer server.Close()

	err := prepareMavenSettings(hub.NewClient(server.URL, "token"), "42", newRedactor())
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("error = %v, want an exclusive-creation failure", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != explicit {
		t.Error("settings created during the Hub lookup were overwritten")
	}
}
