package hub

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
)

func TestFetchMavenCreds(t *testing.T) {
	const assigned = `[{"id":7,"kind":"maven","settings":"<settings/>"}]`
	const fallback = `[{"id":8,"kind":"maven","default":true,"settings":"<settings/>"}]`
	for _, tt := range []struct {
		name           string
		direct         string
		indirect       string
		directStatus   int
		indirectStatus int
		wantID         uint
		wantFallback   bool
		wantErr        bool
	}{
		{name: "assigned credential", direct: assigned, wantID: 7},
		{name: "default credential", direct: `[]`, indirect: fallback, wantID: 8, wantFallback: true},
		{name: "no credential", direct: `[]`, indirect: `[]`, wantFallback: true},
		{name: "assigned lookup forbidden", directStatus: http.StatusForbidden, wantErr: true},
		{name: "default lookup forbidden", direct: `[]`, indirectStatus: http.StatusForbidden, wantFallback: true, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("expected an authenticated GET")
				}
				if r.URL.Query().Get("decrypted") != "1" {
					t.Error("expected decrypted Maven settings")
				}
				request := r.URL.Path + "?" + r.URL.Query().Get("filter")
				requests = append(requests, request)
				body, status := tt.direct, tt.directStatus
				if len(requests) > 1 {
					body, status = tt.indirect, tt.indirectStatus
				}
				if status == 0 {
					status = http.StatusOK
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			identity, err := NewClient(server.URL, "test-token").FetchMavenCreds(42)
			if (err != nil) != tt.wantErr {
				t.Fatalf("FetchMavenCreds() error = %v, wantErr %v", err, tt.wantErr)
			}
			wantRequests := []string{"/applications/42/identities?role='maven'"}
			if tt.wantFallback {
				wantRequests = append(wantRequests, "/identities?kind='maven',default=true")
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(requests, wantRequests) {
				t.Errorf("requests = %v, want %v", requests, wantRequests)
			}
			if tt.wantID == 0 {
				if identity != nil {
					t.Error("expected no Maven credential")
				}
			} else if identity == nil || identity.ID != tt.wantID || identity.Settings != "<settings/>" {
				t.Error("did not return the expected Maven credential and settings")
			}
		})
	}
}
