// Package proxy resolves the harness's tool-specific use of Hub proxies.
// It never changes the process environment or the Hub client's transport.
package proxy

import (
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

// Config is an enabled Hub proxy, including its decrypted identity.
// Kind is the destination protocol; Hub uses HTTP forward proxies for both.
type Config struct {
	Kind, Host         string
	Port               int
	Username, Password string
	Excluded           []string
}

// Policy captures configuration once, before any Git operations start.
type Policy struct {
	configs  []Config
	urls     map[string]*url.URL
	excluded []string
	env      map[string]string
	secrets  []string
	gitProxy map[string]func(*url.URL) (*url.URL, error)
}

func New(configs []Config, lookup func(string) (string, bool)) (*Policy, error) {
	p := &Policy{urls: make(map[string]*url.URL), env: make(map[string]string)}
	for _, c := range configs {
		if c.Kind != "http" && c.Kind != "https" {
			return nil, fmt.Errorf("unsupported Hub proxy kind")
		}
		if _, found := p.urls[c.Kind]; found {
			return nil, fmt.Errorf("multiple enabled Hub proxies for %s", c.Kind)
		}
		c.Host = strings.Trim(c.Host, "[]")
		if c.Host == "" || strings.ContainsAny(c.Host, "/@?# \\|\t\r\n") ||
			(strings.Contains(c.Host, ":") && net.ParseIP(c.Host) == nil) || c.Port < 0 || c.Port > 65535 {
			return nil, fmt.Errorf("invalid Hub %s proxy host or port", c.Kind)
		}
		host := c.Host
		if c.Port != 0 {
			host = net.JoinHostPort(host, strconv.Itoa(c.Port))
		} else if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		u := &url.URL{Scheme: "http", Host: host}
		if c.Username != "" || c.Password != "" {
			u.User = url.UserPassword(c.Username, c.Password)
		}
		p.urls[c.Kind] = u
		p.configs = append(p.configs, c)
		p.addSecrets(u)
		for _, raw := range c.Excluded {
			h := strings.ToLower(strings.TrimSpace(raw))
			if h == "" {
				continue
			}
			if strings.ContainsAny(h, "/@?#| \\[]\t\r\n") || strings.Contains(h, ":") {
				// IPv6 literals are valid hosts; CIDRs, URLs and ports are not.
				if net.ParseIP(strings.Trim(h, "[]")) == nil {
					return nil, fmt.Errorf("unsupported Hub proxy exclusion: use a hostname, IP address, or * wildcard")
				}
				h = strings.Trim(h, "[]")
			}
			if !slices.Contains(p.excluded, h) {
				p.excluded = append(p.excluded, h)
			}
		}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		value, ok := lookup(key)
		if value == "" {
			lower, lowerOK := lookup(strings.ToLower(key))
			if lowerOK && lower != "" {
				value = lower
			}
			ok = ok || lowerOK
		}
		if ok {
			p.env[key] = value
		}
		if key != "NO_PROXY" && value != "" {
			if strings.Contains(value, "@") {
				p.secrets = append(p.secrets, value)
			}
			raw := value
			if !strings.Contains(raw, "://") {
				raw = "http://" + raw
			}
			u, err := url.Parse(raw)
			if err == nil {
				p.addSecrets(u)
			}
		}
	}
	p.gitProxy = make(map[string]func(*url.URL) (*url.URL, error))
	var hubExclusions []func(string) bool
	for _, pattern := range p.excluded {
		hubExclusions = append(hubExclusions, compileHubExclusion(pattern))
	}
	for _, scheme := range []string{"http", "https"} {
		key := strings.ToUpper(scheme) + "_PROXY"
		if value, explicit := p.env[key]; explicit {
			p.gitProxy[scheme] = (&httpproxy.Config{HTTPProxy: value, HTTPSProxy: value, NoProxy: p.env["NO_PROXY"]}).ProxyFunc()
			continue
		}
		u := p.urls[scheme]
		if u == nil {
			continue
		}
		if value, explicit := p.env["NO_PROXY"]; explicit {
			p.gitProxy[scheme] = (&httpproxy.Config{HTTPProxy: u.String(), HTTPSProxy: u.String(), NoProxy: value}).ProxyFunc()
			continue
		}
		p.gitProxy[scheme] = func(target *url.URL) (*url.URL, error) {
			host := strings.ToLower(target.Hostname())
			for _, matches := range hubExclusions {
				if matches(host) {
					return nil, nil
				}
			}
			return u, nil
		}
	}
	return p, nil
}

func (p *Policy) addSecrets(u *url.URL) {
	if u.User == nil {
		return
	}
	password, _ := u.User.Password()
	for _, value := range []string{u.User.Username(), password} {
		if value == "" {
			continue
		}
		var escaped strings.Builder
		_ = xml.EscapeText(&escaped, []byte(value))
		p.secrets = append(p.secrets, value, url.PathEscape(value), url.QueryEscape(value), escaped.String())
	}
	p.secrets = append(p.secrets, u.String(), u.User.String())
}

func (p *Policy) Secrets() []string { return slices.Clone(p.secrets) }

// MavenProxies uses one bypass list for all inherited proxies. This matches
// Hub's UI and prevents Maven's HTTP fallback bypassing an HTTPS exclusion.
func (p *Policy) MavenProxies() []Config {
	configs := slices.Clone(p.configs)
	for i := range configs {
		configs[i].Excluded = nil
		for _, h := range p.excluded {
			switch {
			case net.ParseIP(h) != nil:
				configs[i].Excluded = append(configs[i].Excluded, h)
				// Java URL hosts can retain IPv6 brackets.
				if strings.Contains(h, ":") {
					configs[i].Excluded = append(configs[i].Excluded, "["+h+"]")
				}
			case strings.HasPrefix(h, "."):
				configs[i].Excluded = append(configs[i].Excluded, "*"+h)
			case !strings.Contains(h, "*"):
				configs[i].Excluded = append(configs[i].Excluded, h, "*."+h)
			default:
				configs[i].Excluded = append(configs[i].Excluded, h)
			}
		}
	}
	return configs
}

// GitProxy preserves explicit per-scheme environment settings (including an
// explicit empty value for direct access). NO_PROXY replaces inherited host
// exclusions when supplied and retains Go's native environment semantics.
func (p *Policy) GitProxy(r *http.Request) (*url.URL, error) {
	host := strings.ToLower(r.URL.Hostname())
	if host == "localhost" || net.ParseIP(host).IsLoopback() {
		return nil, nil
	}
	selectProxy := p.gitProxy[strings.ToLower(r.URL.Scheme)]
	if selectProxy == nil {
		return nil, nil
	}
	return selectProxy(r.URL)
}

func compileHubExclusion(pattern string) func(string) bool {
	if strings.Contains(pattern, "*") {
		// Only '*' is a wildcard; validation rejects other glob operators.
		expression := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
		return regexp.MustCompile(expression).MatchString
	}
	if net.ParseIP(pattern) != nil {
		return func(host string) bool { return host == pattern }
	}
	if strings.HasPrefix(pattern, ".") {
		return func(host string) bool { return strings.HasSuffix(host, pattern) }
	}
	return func(host string) bool { return host == pattern || strings.HasSuffix(host, "."+pattern) }
}
