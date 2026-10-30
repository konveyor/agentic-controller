package main

import (
	"encoding/json"
	"encoding/xml"
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
			red := newRedactor()
			if err := prepareMavenSettings(hub.NewClient(server.URL, "token"), "42", red, configs...); err != nil {
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
			if len(settings.Proxies) != 1 || settings.Proxies[0].Password != "p&x" || settings.Proxies[0].NonProxyHosts != "*.internal.example|localhost" {
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
