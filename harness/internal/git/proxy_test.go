package git

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/konveyor/migration-harness/internal/proxy"
)

func TestGitLifecycleThroughAuthenticatedProxy(t *testing.T) {
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git http-backend is required for this integration test")
	}
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			remoteDir, remoteRepo := setupBareRemote(t)
			seedBareRepo(t, remoteDir)
			cfg, _ := remoteRepo.Config()
			cfg.Raw.SetOption("http", "", "receivepack", "true")
			if err := remoteRepo.SetConfig(cfg); err != nil {
				t.Fatal(err)
			}
			backend := &cgi.Handler{Path: gitBinary, Args: []string{"http-backend"}, Env: []string{"GIT_PROJECT_ROOT=" + filepath.Dir(remoteDir), "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=test"}}
			var originCalls atomic.Int32
			originHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originCalls.Add(1)
				user, password, ok := r.BasicAuth()
				if !ok || user != "git-user" || password != "git-password" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy authentication reached the Git server")
				}
				backend.ServeHTTP(w, r)
			})
			var origin *httptest.Server
			if scheme == "https" {
				origin = httptest.NewTLSServer(originHandler)
				old := http.DefaultTransport
				tr := old.(*http.Transport).Clone()
				tr.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
				tr.TLSClientConfig.RootCAs = x509.NewCertPool()
				tr.TLSClientConfig.RootCAs.AddCert(origin.Certificate())
				// The fictitious repository host is deliberately unresolvable.
				// Trust the local fixture's IP certificate for this connection.
				tr.TLSClientConfig.ServerName = "127.0.0.1"
				http.DefaultTransport = tr
				defer func() { http.DefaultTransport = old; tr.CloseIdleConnections() }()
			} else {
				origin = httptest.NewServer(originHandler)
			}
			defer origin.Close()
			originURL, _ := url.Parse(origin.URL)
			forward := httputil.NewSingleHostReverseProxy(originURL)
			var proxyCalls atomic.Int32
			proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				proxyCalls.Add(1)
				if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("proxy-user:p@ss:&?")) {
					w.WriteHeader(http.StatusProxyAuthRequired)
					return
				}
				if r.Method == http.MethodConnect {
					target, err := net.DialTimeout("tcp", originURL.Host, 5*time.Second)
					if err != nil {
						http.Error(w, "fixture connection failed", http.StatusBadGateway)
						return
					}
					connection, buffer, err := w.(http.Hijacker).Hijack()
					if err != nil {
						target.Close()
						return
					}
					_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
					_ = buffer.Flush()
					go func() { _, _ = io.Copy(target, buffer); target.Close() }()
					_, _ = io.Copy(connection, target)
					connection.Close()
					return
				}
				r.Header.Del("Proxy-Authorization")
				forward.ServeHTTP(w, r)
			}))
			defer proxyServer.Close()
			address, _ := url.Parse(proxyServer.URL)
			port := proxyServer.Listener.Addr().(*net.TCPAddr).Port
			policy, err := proxy.New([]proxy.Config{{Kind: scheme, Host: address.Hostname(), Port: port, Username: "proxy-user", Password: "p@ss:&?"}}, func(string) (string, bool) { return "", false })
			if err != nil {
				t.Fatal(err)
			}
			restore := ConfigureProxy(policy.GitProxy)
			defer restore()
			cred := &Credentials{RepoURL: scheme + "://repository.invalid/remote.git", Username: "git-user", Token: "git-password", Branch: "migration-proxy"}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cloneDir := filepath.Join(t.TempDir(), "clone")
			repo, err := Clone(ctx, cred, cloneDir)
			if err != nil {
				t.Fatal(err)
			}
			if err := StripCredentials(repo); err != nil {
				t.Fatal(err)
			}
			if err := CheckWriteAccess(ctx, cred); err != nil {
				t.Fatal(err)
			}
			if err := repo.FetchContext(ctx, &gogit.FetchOptions{Auth: cred.Auth()}); err != nil && err != gogit.NoErrAlreadyUpToDate {
				t.Fatal(err)
			}
			if err := CheckoutBranch(repo, cred.Branch); err != nil {
				t.Fatal(err)
			}
			base, _ := HeadSHA(repo)
			if err := os.WriteFile(filepath.Join(cloneDir, "result.txt"), []byte("migration\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			wt, _ := repo.Worktree()
			_, _ = wt.Add("result.txt")
			hash, err := wt.Commit("migration through proxy", &gogit.CommitOptions{Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Push(ctx, &Credentials{RepoURL: cred.RepoURL}, repo, cred.Branch, base); err == nil {
				t.Fatal("agent without push credentials was able to publish")
			}
			if result, err := Push(ctx, cred, repo, cred.Branch, base); err != nil || result != PushAdvanced {
				t.Fatalf("push=%v, error=%v", result, err)
			}
			if result, err := Push(ctx, cred, repo, cred.Branch, base); err != nil || result != PushUpToDate {
				t.Fatalf("final push=%v, error=%v", result, err)
			}
			ref, err := remoteRepo.Reference(plumbing.NewBranchReferenceName(cred.Branch), true)
			if err != nil || ref.Hash() != hash {
				t.Fatal("migration commit did not reach the remote")
			}
			data, _ := os.ReadFile(filepath.Join(cloneDir, ".git", "config"))
			for _, secret := range []string{cred.Token, "p@ss:&?", "proxy-user", proxyServer.URL} {
				if strings.Contains(string(data), secret) {
					t.Error("credentials or proxy configuration persisted in Git config")
				}
			}
			// HTTPS reuses a CONNECT tunnel across several Git requests.
			if proxyCalls.Load() == 0 || originCalls.Load() < 5 {
				t.Error("expected proxied clone/fetch, write discovery and pushes")
			}
		})
	}
}

func TestGitDirectAndExcludedHosts(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		t.Run(map[bool]string{false: "no enabled Hub proxy", true: "excluded host"}[inherited], func(t *testing.T) {
			var originCalls, proxyCalls atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originCalls.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer origin.Close()
			forward := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				proxyCalls.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer forward.Close()
			var configs []proxy.Config
			if inherited {
				configs = []proxy.Config{{Kind: "http", Host: "127.0.0.1", Port: forward.Listener.Addr().(*net.TCPAddr).Port, Excluded: []string{"127.*"}}}
			}
			policy, err := proxy.New(configs, func(string) (string, bool) { return "", false })
			if err != nil {
				t.Fatal(err)
			}
			restore := ConfigureProxy(policy.GitProxy)
			defer restore()
			err = CheckWriteAccess(context.Background(), &Credentials{RepoURL: origin.URL + "/remote.git"})
			if !errors.Is(err, ErrNoWriteAccess) || originCalls.Load() != 1 || proxyCalls.Load() != 0 {
				t.Fatalf("direct access failed: origin=%d proxy=%d err=%v", originCalls.Load(), proxyCalls.Load(), err)
			}
		})
	}
}
