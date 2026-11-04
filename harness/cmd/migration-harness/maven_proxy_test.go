package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konveyor/migration-harness/internal/hub"
	"github.com/konveyor/migration-harness/internal/proxy"
)

func TestPrepareMavenSettingsWithProxies(t *testing.T) {
	configs := []proxy.Config{{Kind: "https", Host: "proxy.example", Port: 3128, Username: "user", Password: "p&x", Excluded: []string{"*.internal.example", "localhost"}}}
	for _, tt := range []struct {
		name, source, wantUnchanged string
		existing, mount             bool
	}{
		{name: "no credential"},
		{name: "Hub settings", source: `<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0"><mirrors><mirror><id>private</id></mirror></mirrors><!-- preserve --><profiles><profile><id>build</id></profile></profiles></settings>`},
		{name: "self-closing root", source: `<?xml version="1.0"?><settings />`},
		{name: "prefixed root", source: `<m:settings xmlns:m="http://maven.apache.org/SETTINGS/1.2.0"><m:profiles/></m:settings>`},
		{name: "Hub explicit proxies win", source: `<settings><proxies><proxy><active>false</active></proxy></proxies></settings>`, wantUnchanged: "explicit"},
		{name: "Hub empty proxies win", source: `<settings><proxies/></settings>`, wantUnchanged: "explicit"},
		{name: "existing settings", source: `<settings><mirrors/></settings>`, existing: true, wantUnchanged: "existing"},
		{name: "Secret mount", source: `<settings><servers/></settings>`, existing: true, mount: true, wantUnchanged: "mount"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			path := filepath.Join(home, ".m2", "settings.xml")
			if tt.existing {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				target := path
				if tt.mount {
					target = filepath.Join(home, "mounted-settings")
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(target, []byte(tt.source), 0o400); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.existing {
					t.Error("existing Maven settings should skip Maven identity lookup")
				}
				w.Header().Set("Content-Type", "application/json")
				if tt.source == "" {
					_, _ = w.Write([]byte(`[]`))
				} else {
					_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7, "settings": tt.source}})
				}
			}))
			defer server.Close()
			inherited := append([]proxy.Config(nil), configs...)
			if tt.wantUnchanged != "" {
				// These exclusions work for Git but cannot be translated to Maven.
				inherited[0].Excluded = []string{"10.0.0.0/8", "private.example:8443"}
			}
			policy, err := proxy.New(inherited, func(string) (string, bool) { return "", false })
			if err != nil {
				t.Fatal(err)
			}
			red := newRedactor()
			if err := prepareMavenSettings(hub.NewClient(server.URL, "token"), "42", red, policy.HubProxies()...); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantUnchanged != "" {
				if string(data) != tt.source {
					t.Error("explicit settings changed")
				}
				if tt.mount {
					if _, err := os.Readlink(path); err != nil {
						t.Error("Secret mount was replaced")
					}
				}
				return
			}
			var settings struct {
				Proxies []mavenProxy `xml:"proxies>proxy"`
			}
			if err := xml.Unmarshal(data, &settings); err != nil {
				t.Fatal(err)
			}
			if len(settings.Proxies) != 1 || settings.Proxies[0].Password != "p&x" || settings.Proxies[0].NonProxyHosts != "*.internal.example|localhost|*.localhost" {
				t.Fatal("generated settings lost proxy authentication or exclusions")
			}
			if strings.Contains(red.redact("p&x p&amp;x"), "p&x") || strings.Contains(red.redact("p&x p&amp;x"), "p&amp;x") {
				t.Error("proxy credentials are not redacted")
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0o600 {
				t.Error("proxy settings must be private")
			}
			if strings.Contains(tt.source, "<!-- preserve -->") && !strings.Contains(string(data), `<mirrors><mirror><id>private</id></mirror></mirrors><!-- preserve --><profiles><profile><id>build</id></profile></profiles>`) {
				t.Error("original Maven settings content was not preserved")
			}
		})
	}
}

func TestMavenProxyRejectsInterpolationCredentials(t *testing.T) {
	for _, config := range []proxy.Config{
		{Kind: "http", Host: "proxy", Password: "private-${env.HOME}"},
		{Kind: "https", Host: "proxy", Password: "private-${user.home}"},
		{Kind: "https", Host: "proxy", Username: "private-${env.USER}"},
	} {
		_, err := withMavenProxies("<settings/>", []proxy.Config{config})
		if !errors.Is(err, errMavenProxyConversion) {
			t.Errorf("expected conversion failure for credentials Maven would interpolate, got %v", err)
		}
		if err != nil && strings.Contains(err.Error(), "private-") {
			t.Error("error revealed credential content")
		}
		const explicit = "<settings><proxies/></settings>"
		settings, err := withMavenProxies(explicit, []proxy.Config{config})
		if err != nil || settings != explicit {
			t.Error("explicit Maven proxy settings must remain authoritative")
		}
	}
}

func TestGeneratedMavenProxiesRejectUnsupportedExclusions(t *testing.T) {
	for _, exclusion := range []string{"10.0.0.0/8", "2001:db8::/32", "private.example:8443", "[2001:db8::1]:443", "*.private.example:443"} {
		t.Run(exclusion, func(t *testing.T) {
			policy, err := proxy.New([]proxy.Config{{Kind: "https", Host: "proxy", Excluded: []string{exclusion}}}, func(string) (string, bool) { return "", false })
			if err != nil {
				t.Fatalf("Git-compatible exclusion rejected before Maven setup: %v", err)
			}
			for _, source := range []string{"", "<settings><servers/></settings>"} {
				_, err := withMavenProxies(source, policy.HubProxies())
				if !errors.Is(err, errMavenProxyConversion) {
					t.Errorf("expected Maven conversion failure, got %v", err)
				}
			}
		})
	}
}

func TestMavenExclusionTranslation(t *testing.T) {
	got, err := mavenNonProxyHosts([]string{" EXAMPLE.com ", ".corp.example", "git*.example", "10.1.2.3", "[2001:db8::1]"})
	const want = "example.com|*.example.com|*.corp.example|git*.example|10.1.2.3|2001:db8::1|[2001:db8::1]"
	if err != nil || got != want {
		t.Fatalf("nonProxyHosts = %q, %v; want %q", got, err, want)
	}
}

func TestPrepareMavenSettingsSkipsUnsupportedProxyConversion(t *testing.T) {
	const supplied = `<settings><servers><server><password>repository-secret</password><passphrase>s3!</passphrase></server></servers><!-- preserve --><profiles><profile><id>custom</id></profile></profiles></settings>`
	for _, tt := range []struct {
		name, username, password string
		excluded                 []string
	}{
		{name: "CIDR", excluded: []string{"10.0.0.0/8"}},
		{name: "IPv6 CIDR", excluded: []string{"2001:db8::/32"}},
		{name: "host port", excluded: []string{"private.example:8443"}},
		{name: "IPv6 port", excluded: []string{"[2001:db8::1]:443"}},
		{name: "password interpolation", username: "user", password: "private-${env.HOME}"},
		{name: "username interpolation", username: "private-${env.USER}"},
	} {
		for _, source := range []string{"absent", "assigned", "default"} {
			t.Run(tt.name+"/"+source, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if source == "absent" || (source == "default" && r.URL.Path == "/applications/42/identities") {
						_, _ = w.Write([]byte(`[]`))
						return
					}
					_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7, "settings": supplied}})
				}))
				defer server.Close()
				// A valid first proxy must not be installed when the second
				// cannot be converted: the entire automatic addition is skipped.
				policy, err := proxy.New([]proxy.Config{
					{Kind: "http", Host: "proxy.example", Username: "user", Password: "first-secret"},
					{Kind: "https", Host: "proxy2.example", Username: tt.username, Password: tt.password, Excluded: tt.excluded},
				}, func(string) (string, bool) { return "", false })
				if err != nil {
					t.Fatal(err)
				}
				red := newRedactor()
				if err := prepareMavenSettings(hub.NewClient(server.URL, "token"), "42", red, policy.HubProxies()...); err != nil {
					t.Fatalf("automatic proxy conversion stopped setup: %v", err)
				}
				request, _ := http.NewRequest(http.MethodGet, "http://git.example/repo.git", nil)
				gitProxy, err := policy.GitProxy(request)
				if err != nil || gitProxy == nil || gitProxy.Hostname() != "proxy.example" {
					t.Fatalf("Maven fallback changed Git routing: %v, %v", gitProxy, err)
				}
				path := filepath.Join(home, ".m2", "settings.xml")
				data, err := os.ReadFile(path)
				if source == "absent" {
					if !os.IsNotExist(err) {
						t.Errorf("proxy-only fallback must not create a settings file: %v", err)
					}
					return
				}
				if err != nil || string(data) != supplied {
					t.Fatalf("fallback changed supplied Maven settings: %v", err)
				}
				if got := red.redact("repository-secret s3!"); got != "[redacted] [redacted]" {
					t.Errorf("fallback lost Maven credential redaction: %q", got)
				}
				if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
					t.Errorf("fallback settings must remain private: %v", err)
				}
			})
		}
	}
}

func TestMavenProxyFallbackDoesNotIgnoreMalformedSuppliedSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7, "settings": `<settings><servers><server><password>private-settings-password</server>`}})
	}))
	defer server.Close()
	err := prepareMavenSettings(hub.NewClient(server.URL, "token"), "42", newRedactor(), proxy.Config{Kind: "http", Host: "proxy", Excluded: []string{"10.0.0.0/8"}})
	if err == nil || errors.Is(err, errMavenProxyConversion) {
		t.Fatalf("malformed supplied settings must remain an error: %v", err)
	}
	if strings.Contains(err.Error(), "private-settings-password") {
		t.Error("supplied settings leaked into diagnostics")
	}
	if _, err := os.Stat(filepath.Join(home, ".m2", "settings.xml")); !os.IsNotExist(err) {
		t.Errorf("malformed supplied settings were written: %v", err)
	}
}
