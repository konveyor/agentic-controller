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

package diagnostics_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/client-go/rest"

	// Initialize the same controller-runtime collectors as the application.
	_ "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func TestHTTPSMetricsRequireAuthorizedIdentity(t *testing.T) {
	const readerToken = "reader"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/apis/authentication.k8s.io/v1/tokenreviews":
			var review authenticationv1.TokenReview
			if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			review.Status.Authenticated = review.Spec.Token == readerToken || review.Spec.Token == "denied"
			review.Status.User.Username = review.Spec.Token
			review.Status.Audiences = review.Spec.Audiences
			_ = json.NewEncoder(w).Encode(review)
		case "/apis/authorization.k8s.io/v1/subjectaccessreviews":
			var review authorizationv1.SubjectAccessReview
			if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			attr := review.Spec.NonResourceAttributes
			review.Status.Allowed = review.Spec.User == readerToken && attr != nil && attr.Path == "/metrics" && attr.Verb == "get"
			_ = json.NewEncoder(w).Encode(review)
		default:
			t.Errorf("unexpected API request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()
	server, err := metricsserver.NewServer(metricsserver.Options{
		BindAddress: "127.0.0.1:0", SecureServing: true,
		FilterProvider: filters.WithAuthenticationAndAuthorization,
	}, &rest.Config{Host: api.URL}, api.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("metrics server did not stop")
		}
	})
	listener := server.(interface{ GetBindAddr() string })
	deadline := time.Now().Add(5 * time.Second)
	for listener.GetBindAddr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("metrics server did not bind")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The test server creates a throwaway self-signed certificate. Production
	// scrapes verify the mounted OpenShift serving certificate using its CA.
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- isolated test server
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for _, tc := range []struct {
		token  string
		status int
	}{
		{"", http.StatusUnauthorized}, {readerToken, http.StatusOK},
		{"denied", http.StatusForbidden},
	} {
		t.Run(tc.token, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "https://"+listener.GetBindAddr()+"/metrics", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Error(err)
				}
			}()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tc.status, body)
			}
			if tc.status == http.StatusOK {
				for _, name := range []string{"go_memstats_heap_alloc_bytes", "go_goroutines", "go_gc_duration_seconds"} {
					if !strings.Contains(string(body), name) {
						t.Errorf("missing runtime metric %s", name)
					}
				}
			}
		})
	}
}
