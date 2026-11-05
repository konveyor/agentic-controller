package hub

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchProxies(t *testing.T) {
	for _, tt := range []struct {
		name, body                  string
		listStatus, identityStatus  int
		wantProxies, wantIdentities int
		wantErr                     bool
	}{
		{name: "absent", body: `[]`},
		{name: "disabled skips credentials", body: `[{"id":1,"enabled":false,"kind":"http","identity":{"id":7}}]`},
		{name: "authenticated shared identity", body: `[{"id":1,"enabled":true,"kind":"http","host":"proxy","port":3128,"identity":{"id":7}},{"id":2,"enabled":true,"kind":"https","host":"proxy","port":3128,"identity":{"id":7}}]`, wantProxies: 2, wantIdentities: 1},
		{name: "anonymous", body: `[{"id":1,"enabled":true,"kind":"http","host":"proxy","port":3128}]`, wantProxies: 1},
		{name: "list denied", listStatus: http.StatusForbidden, wantErr: true},
		{name: "identity denied", body: `[{"id":1,"enabled":true,"kind":"http","identity":{"id":7}}]`, identityStatus: http.StatusForbidden, wantIdentities: 1, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			identityCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-token" || r.Method != http.MethodGet {
					t.Error("proxy resolution must use an authenticated GET")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/proxies":
					if tt.listStatus != 0 {
						w.WriteHeader(tt.listStatus)
					}
					_, _ = w.Write([]byte(tt.body))
				case "/identities/7":
					identityCalls++
					if r.URL.Query().Get("decrypted") != "1" {
						t.Error("proxy identity must be decrypted")
					}
					if tt.identityStatus != 0 {
						w.WriteHeader(tt.identityStatus)
					}
					_, _ = w.Write([]byte(`{"id":7,"user":"proxy-user","password":"proxy-password"}`))
				default:
					t.Errorf("unexpected Hub request: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			configs, err := NewClient(server.URL, "test-token").FetchProxies()
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tt.wantErr)
			}
			if len(configs) != tt.wantProxies || identityCalls != tt.wantIdentities {
				t.Fatalf("proxies=%d, identity lookups=%d; want %d, %d", len(configs), identityCalls, tt.wantProxies, tt.wantIdentities)
			}
			if tt.wantIdentities > 0 && !tt.wantErr && configs[0].Password != "proxy-password" {
				t.Error("proxy authentication was not resolved")
			}
		})
	}
}
