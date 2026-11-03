package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konveyor/migration-harness/internal/hub"
)

func TestPrepareMavenSettingsRedactsCredentials(t *testing.T) {
	const settings = `<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0">
  <servers><server><id>private-repository</id>
    <password>maven-p&#97;ss&lt;word&gt;</password>
    <passphrase><![CDATA[git-token-value-and-passphrase]]></passphrase>
  </server><server><id>anonymous-repository</id><password> </password><passphrase/></server></servers>
  <proxies><proxy><password>s3!</password></proxy></proxies>
</settings>`
	for _, source := range []string{"assigned", "default", "existing", "Secret mount"} {
		t.Run(source, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			path := filepath.Join(home, ".m2", "settings.xml")
			if source == "existing" || source == "Secret mount" {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				target := path
				if source == "Secret mount" {
					target = filepath.Join(home, "mounted-settings")
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(target, []byte(settings), 0o400); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if source == "existing" || source == "Secret mount" {
					t.Error("existing settings must not fetch a Hub credential")
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if source == "default" && r.URL.Path == "/applications/42/identities" {
					_, _ = w.Write([]byte(`[]`))
					return
				}
				_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7, "settings": settings}})
			}))
			defer server.Close()

			red := newRedactor("git-token-value")
			if err := prepareMavenSettings(hub.NewClient(server.URL, "token"), "42", red); err != nil {
				t.Fatal(err)
			}
			// Closing messages, provider errors, notices, and plan excerpts
			// all use this same redactor after Maven setup in runStage.
			message := "Build finished for private-repository: maven-pass<word>, git-token-value-and-passphrase, s3!"
			if got, want := red.redact(message), "Build finished for private-repository: [redacted], [redacted], [redacted]"; got != want {
				t.Errorf("credential values were not fully redacted: %q", got)
			}
			loggedSettings := red.redact(settings)
			for _, secret := range []string{"maven-p&#97;ss&lt;word&gt;", "git-token-value-and-passphrase", "s3!"} {
				if strings.Contains(loggedSettings, secret) {
					t.Error("raw settings quoted by the agent would leak a credential")
				}
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != settings {
				t.Errorf("redaction changed the Maven settings file: %v", err)
			}
		})
	}
}

func TestPrepareMavenSettingsRejectsUnredactableXML(t *testing.T) {
	const settings = `<settings><servers><server><password>private-password</private-password>`
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "Hub", true: "existing"}[existing], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			path := filepath.Join(home, ".m2", "settings.xml")
			if existing {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7, "settings": settings}})
			}))
			defer server.Close()
			err := prepareMavenSettings(hub.NewClient(server.URL, "token"), "42", newRedactor())
			if err == nil {
				t.Fatal("settings that cannot be parsed for secret redaction must stop setup")
			}
			if strings.Contains(err.Error(), "private-password") || strings.Contains(err.Error(), settings) {
				t.Error("XML parse error exposed credential contents")
			}
			if !existing {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Errorf("invalid Hub settings were installed: %v", err)
				}
			}
		})
	}
}
