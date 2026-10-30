package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konveyor/migration-harness/internal/hub"
	"github.com/konveyor/migration-harness/internal/proxy"
)

// Opt-in because this uses a local JDK, Maven and pre-cached Maven plugin
// artifacts as an offline fixture. All downloads are served locally; Maven's
// own repository is fresh, and the proxied repository name cannot resolve.
func TestMavenProxyBuild(t *testing.T) {
	if os.Getenv("HARNESS_PROXY_E2E") != "on" {
		t.Skip("set HARNESS_PROXY_E2E=on to run the real Maven proxy build")
	}
	mvn, err := exec.LookPath("mvn")
	if err != nil {
		t.Fatal("Maven and a JDK are required")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cache := os.Getenv("HARNESS_MAVEN_FIXTURE_REPOSITORY")
	if cache == "" {
		cache = filepath.Join(home, ".m2", "repository")
	}
	for _, plugin := range []string{"maven-resources-plugin/3.3.1/maven-resources-plugin-3.3.1.jar", "maven-compiler-plugin/3.13.0/maven-compiler-plugin-3.13.0.jar"} {
		if _, err := os.Stat(filepath.Join(cache, "org/apache/maven/plugins", plugin)); err != nil {
			t.Fatalf("offline fixture needs cached plugin %s: %v", plugin, err)
		}
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, _ := zw.Create("fixture.txt")
	_, _ = w.Write([]byte("proxy-only dependency\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	const artifact = "org/konveyor/proxy-fixture/1.0/proxy-fixture-1.0"
	fixture := map[string][]byte{
		artifact + ".jar": archive.Bytes(),
		artifact + ".pom": []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>org.konveyor</groupId><artifactId>proxy-fixture</artifactId><version>1.0</version></project>`),
	}
	for _, mode := range []string{"authenticated proxy-only repository", "excluded direct repository", "HTTPS exclusion blocks HTTP fallback"} {
		t.Run(mode, func(t *testing.T) {
			excluded := mode != "authenticated proxy-only repository"
			https := mode == "HTTPS exclusion blocks HTTP fallback"
			var proxyCalls, artifactCalls atomic.Int32
			repository := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
				checksum := strings.HasSuffix(name, ".sha1")
				if checksum {
					name = strings.TrimSuffix(name, ".sha1")
				}
				data, found := fixture[name]
				if found {
					artifactCalls.Add(1)
				} else {
					var err error
					data, err = os.ReadFile(filepath.Join(cache, filepath.FromSlash(name)))
					if err != nil {
						http.NotFound(w, r)
						return
					}
				}
				if checksum {
					sum := sha1.Sum(data)
					data = []byte(hex.EncodeToString(sum[:]))
				}
				_, _ = w.Write(data)
			})
			var direct *httptest.Server
			if https {
				direct = httptest.NewTLSServer(repository)
			} else {
				direct = httptest.NewServer(repository)
			}
			defer direct.Close()
			forward := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				proxyCalls.Add(1)
				if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-password")) {
					w.Header().Set("Proxy-Authenticate", `Basic realm="fixture"`)
					w.WriteHeader(http.StatusProxyAuthRequired)
					return
				}
				repository.ServeHTTP(w, r)
			}))
			defer forward.Close()
			repoURL := "http://maven.proxy-only.invalid"
			if excluded {
				repoURL = direct.URL
			}
			settings := fmt.Sprintf(`<settings><mirrors><mirror><id>fixture</id><mirrorOf>*</mirrorOf><url>%s</url></mirror></mirrors></settings>`, repoURL)
			clientServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7, "settings": settings}})
			}))
			defer clientServer.Close()
			work := t.TempDir()
			t.Setenv("HOME", filepath.Join(work, "home"))
			for _, key := range []string{"MAVEN_ARGS", "MAVEN_CONFIG", "MAVEN_OPTS", "JAVA_TOOL_OPTIONS"} {
				t.Setenv(key, "")
			}
			configs := []proxy.Config{{Kind: "http", Host: "127.0.0.1", Port: forward.Listener.Addr().(*net.TCPAddr).Port, Username: "proxy-user", Password: "proxy-password", Excluded: []string{"127.0.0.1"}}}
			if https {
				// Hub's API can supply unequal per-scheme lists even though
				// its UI shares one. A shared bypass prevents HTTP fallback.
				configs[0].Excluded = nil
				configs = append(configs, proxy.Config{Kind: "https", Host: "127.0.0.1", Port: configs[0].Port, Excluded: []string{"127.0.0.1"}})
				certPath := filepath.Join(work, "fixture.pem")
				if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: direct.Certificate().Raw}), 0o600); err != nil {
					t.Fatal(err)
				}
				store := filepath.Join(work, "fixture.p12")
				cmd := exec.Command("keytool", "-importcert", "-noprompt", "-alias", "fixture", "-file", certPath, "-keystore", store, "-storepass", "fixture-password", "-storetype", "PKCS12")
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("create local Maven truststore: %v\n%s", err, output)
				}
				t.Setenv("MAVEN_OPTS", "-Djavax.net.ssl.trustStore="+store+" -Djavax.net.ssl.trustStorePassword=fixture-password")
			}
			policy, err := proxy.New(configs, func(string) (string, bool) { return "", false })
			if err != nil {
				t.Fatal(err)
			}
			if err := prepareMavenSettings(hub.NewClient(clientServer.URL, "test-token"), "42", newRedactor(), policy.MavenProxies()...); err != nil {
				t.Fatal(err)
			}
			pom := `<project><modelVersion>4.0.0</modelVersion><groupId>org.konveyor</groupId><artifactId>proxy-build</artifactId><version>1.0</version><properties><maven.compiler.release>17</maven.compiler.release></properties><dependencies><dependency><groupId>org.konveyor</groupId><artifactId>proxy-fixture</artifactId><version>1.0</version></dependency></dependencies><build><plugins><plugin><artifactId>maven-resources-plugin</artifactId><version>3.3.1</version></plugin><plugin><artifactId>maven-compiler-plugin</artifactId><version>3.13.0</version></plugin></plugins></build></project>`
			files := map[string]string{"pom.xml": pom, "global-settings.xml": "<settings/>", "src/main/java/Example.java": "public class Example {}\n"}
			for name, contents := range files {
				filename := filepath.Join(work, name)
				if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filename, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			// Older Maven Resolver versions have a concurrent proxy-auth cache
			// bug (apache/maven-resolver#1768). Keep this settings-delivery
			// regression deterministic without changing production concurrency.
			cmd := exec.CommandContext(ctx, mvn, "-B", "-ntp", "-s", filepath.Join(work, "home/.m2/settings.xml"), "-gs", filepath.Join(work, "global-settings.xml"), "-Dmaven.repo.local="+filepath.Join(work, "repository"), "-Dmaven.artifact.threads=1", "compile")
			cmd.Dir = work
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Maven proxy build failed: %v\n%s", err, output)
			}
			if _, err := os.Stat(filepath.Join(work, "target/classes/Example.class")); err != nil {
				t.Fatal("Maven did not compile the fixture application")
			}
			if artifactCalls.Load() == 0 || (proxyCalls.Load() > 0) == excluded {
				t.Fatalf("artifact requests=%d, proxy requests=%d, excluded=%v", artifactCalls.Load(), proxyCalls.Load(), excluded)
			}
		})
	}
}
