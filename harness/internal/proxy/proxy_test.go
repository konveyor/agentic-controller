package proxy

import (
	"net/http"
	"strings"
	"testing"
)

func TestGitProxyPrecedenceAndExclusions(t *testing.T) {
	configs := []Config{
		{Kind: "http", Host: "hub-http", Port: 3128, Excluded: []string{"  *.INTERNAL.example  ", ".corp.example.com", "127.0.0.2", "[::1]", "10.0.0.0/8", "2001:db8::/32", "port.example:8443", "[2001:db9::1]:443", "git*.ports.example:443"}},
		{Kind: "https", Host: "hub-https", Port: 8080, Excluded: []string{"private.example"}},
	}
	for _, tt := range []struct {
		name, target, want string
		env                map[string]string
	}{
		{name: "inherited CIDR shared across schemes", target: "https://10.42.1.2", want: ""},
		{name: "outside CIDR", target: "https://11.42.1.2", want: "http://hub-https:8080"},
		{name: "inherited IPv6 CIDR", target: "http://[2001:db8::1234]", want: ""},
		{name: "port-specific host", target: "https://git.port.example:8443", want: ""},
		{name: "other port", target: "https://port.example:443", want: "http://hub-https:8080"},
		{name: "IPv6 default port", target: "https://[2001:db9::1]", want: ""},
		{name: "IPv6 other port", target: "https://[2001:db9::1]:8443", want: "http://hub-https:8080"},
		{name: "wildcard default port", target: "https://gitlab.ports.example", want: ""},
		{name: "wildcard other port", target: "https://gitlab.ports.example:8443", want: "http://hub-https:8080"},
		{name: "explicit bypass replaces inherited CIDR", target: "https://10.42.1.2", env: map[string]string{"NO_PROXY": "other.example"}, want: "http://hub-https:8080"},
		{name: "HTTP", target: "http://repo.example", want: "http://hub-http:3128"},
		{name: "HTTPS", target: "https://repo.example", want: "http://hub-https:8080"},
		{name: "wildcard ignores case and port", target: "https://REPO.internal.EXAMPLE:8443", want: ""},
		{name: "exact exclusions shared across schemes", target: "http://private.example", want: ""},
		{name: "bare domain includes subdomains", target: "http://git.private.example", want: ""},
		{name: "bare domain respects label boundary", target: "http://evilprivate.example", want: "http://hub-http:3128"},
		{name: "leading dot matches subdomains", target: "https://git.corp.example.com", want: ""},
		{name: "leading dot excludes bare domain", target: "https://corp.example.com", want: "http://hub-https:8080"},
		{name: "IP exclusion", target: "http://127.0.0.2:80", want: ""},
		{name: "IPv6 exclusion", target: "https://[::1]:8443", want: ""},
		{name: "localhost always direct", target: "https://localhost:8443", want: ""},
		{name: "unlisted loopback always direct", target: "http://127.42.1.2:8080", want: ""},
		{name: "mapped loopback always direct", target: "https://[::ffff:127.0.0.1]:8443", want: ""},
		{name: "empty explicit bypass still avoids loopback", target: "http://127.0.0.3", env: map[string]string{"NO_PROXY": ""}, want: ""},
		{name: "domain boundary", target: "http://internal.example.attacker.test", want: "http://hub-http:3128"},
		{name: "explicit scheme wins", target: "https://private.example", env: map[string]string{"HTTPS_PROXY": "http://explicit:8081"}, want: "http://explicit:8081"},
		{name: "other scheme still inherited", target: "http://repo.example", env: map[string]string{"HTTPS_PROXY": "http://explicit:8081"}, want: "http://hub-http:3128"},
		{name: "explicit direct", target: "https://repo.example", env: map[string]string{"HTTPS_PROXY": ""}, want: ""},
		{name: "lowercase", target: "https://repo.example", env: map[string]string{"https_proxy": "http://explicit:8081"}, want: "http://explicit:8081"},
		{name: "uppercase wins", target: "https://repo.example", env: map[string]string{"HTTPS_PROXY": "http://upper:81", "https_proxy": "http://lower:82"}, want: "http://upper:81"},
		{name: "empty uppercase falls back to lowercase", target: "https://repo.example", env: map[string]string{"HTTPS_PROXY": "", "https_proxy": "http://lower:82"}, want: "http://lower:82"},
		{name: "empty uppercase bypass falls back to lowercase", target: "https://repo.example", env: map[string]string{"NO_PROXY": "", "no_proxy": "repo.example"}, want: ""},
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
		{Kind: "http", Host: "proxy", Excluded: []string{"10.0.0.0/999"}},
		{Kind: "http", Host: "proxy", Excluded: []string{"example.com:invalid"}},
		{Kind: "http", Host: "proxy", Excluded: []string{"example.com:65536"}},
		{Kind: "http", Host: "proxy", Excluded: []string{"example.com,other.example"}},
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
