package proxy

import (
	"net/http"
	"strings"
	"testing"
)

func TestGitProxyPrecedenceAndExclusions(t *testing.T) {
	configs := []Config{
		{Kind: "http", Host: "hub-http", Port: 3128, Excluded: []string{"  *.INTERNAL.example  ", "127.0.0.2", "[::1]"}},
		{Kind: "https", Host: "hub-https", Port: 8080, Excluded: []string{"private.example"}},
	}
	for _, tt := range []struct {
		name, target, want string
		env                map[string]string
	}{
		{name: "HTTP", target: "http://repo.example", want: "http://hub-http:3128"},
		{name: "HTTPS", target: "https://repo.example", want: "http://hub-https:8080"},
		{name: "wildcard ignores case and port", target: "https://REPO.internal.EXAMPLE:8443", want: ""},
		{name: "exact exclusions shared across schemes", target: "http://private.example", want: ""},
		{name: "IP exclusion", target: "http://127.0.0.2:80", want: ""},
		{name: "IPv6 exclusion", target: "https://[::1]:8443", want: ""},
		{name: "domain boundary", target: "http://internal.example.attacker.test", want: "http://hub-http:3128"},
		{name: "explicit scheme wins", target: "https://private.example", env: map[string]string{"HTTPS_PROXY": "http://explicit:8081"}, want: "http://explicit:8081"},
		{name: "other scheme still inherited", target: "http://repo.example", env: map[string]string{"HTTPS_PROXY": "http://explicit:8081"}, want: "http://hub-http:3128"},
		{name: "explicit direct", target: "https://repo.example", env: map[string]string{"HTTPS_PROXY": ""}, want: ""},
		{name: "lowercase", target: "https://repo.example", env: map[string]string{"https_proxy": "http://explicit:8081"}, want: "http://explicit:8081"},
		{name: "uppercase wins", target: "https://repo.example", env: map[string]string{"HTTPS_PROXY": "http://upper:81", "https_proxy": "http://lower:82"}, want: "http://upper:81"},
		{name: "explicit native CIDR bypass", target: "http://10.42.1.2", env: map[string]string{"NO_PROXY": "10.0.0.0/8"}, want: ""},
		{name: "explicit bypass replaces inherited", target: "https://private.example", env: map[string]string{"NO_PROXY": "other.example"}, want: "http://hub-https:8080"},
		{name: "explicit empty bypass", target: "http://private.example", env: map[string]string{"NO_PROXY": ""}, want: "http://hub-http:3128"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(configs, func(k string) (string, bool) { v, ok := tt.env[k]; return v, ok })
			if err != nil {
				t.Fatal(err)
			}
			r, _ := http.NewRequest(http.MethodGet, tt.target, nil)
			u, err := p.GitProxy(r)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if u != nil {
				got = u.String()
			}
			if got != tt.want {
				t.Errorf("proxy = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInvalidHubProxyConfiguration(t *testing.T) {
	for _, c := range []Config{
		{Kind: "http", Host: "proxy", Excluded: []string{"10.0.0.0/8"}},
		{Kind: "http", Host: "proxy", Excluded: []string{"example.com:8080"}},
		{Kind: "http", Host: "proxy", Excluded: []string{"https://example.com"}},
		{Kind: "http", Host: "proxy", Excluded: []string{"foo?bar"}},
		{Kind: "http", Host: "user:secret@proxy"},
		{Kind: "http", Host: "proxy", Port: -1},
		{Kind: "http", Host: "proxy", Port: 65536},
		{Kind: "socks", Host: "proxy"},
	} {
		if _, err := New([]Config{c}, func(string) (string, bool) { return "", false }); err == nil {
			t.Errorf("expected invalid config to fail: kind=%s", c.Kind)
		}
	}
}

func TestProxyAuthenticationEncoding(t *testing.T) {
	p, err := New([]Config{{Kind: "https", Host: "::1", Port: 3128, Username: "user@realm", Password: "p@ss:&?"}}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	r, _ := http.NewRequest(http.MethodGet, "https://repo.example", nil)
	u, err := p.GitProxy(r)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	if u.Host != "[::1]:3128" || u.User.Username() != "user@realm" || password != "p@ss:&?" {
		t.Error("proxy URL did not preserve authentication and IPv6 host")
	}
	if !strings.Contains(strings.Join(p.Secrets(), " "), "p@ss:&?") {
		t.Error("proxy password is missing from redaction secrets")
	}
}
