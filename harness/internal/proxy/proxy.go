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

	"github.com/konveyor/migration-harness/internal/redaction"

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
			return nil, fmt.Errorf("unsupported Hub proxy kind %q (expected http or https)", c.Kind)
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
			var err error
			h, err = normalizeHubExclusion(h)
			if err != nil {
				return nil, err
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
	var hubExclusions []func(*url.URL) bool
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
			for _, matches := range hubExclusions {
				if matches(target) {
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
	username := u.User.Username()
	if password != "" && len(username) < redaction.MinSecretLen {
		// A short ordinary username is not a token. Mask combined forms
		// below without replacing words such as "proxy" in diagnostics.
		username = ""
	}
	for _, value := range []string{username, password} {
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

// HubProxies returns inherited proxies with their shared bypass list. Maven
// validates and translates exclusions only when it generates proxy settings.
func (p *Policy) HubProxies() []Config {
	configs := slices.Clone(p.configs)
	for i := range configs {
		configs[i].Excluded = slices.Clone(p.excluded)
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

func normalizeHubExclusion(pattern string) (string, error) {
	if ip := net.ParseIP(strings.Trim(pattern, "[]")); ip != nil {
		return ip.String(), nil
	}
	if _, _, err := net.ParseCIDR(pattern); err == nil {
		return pattern, nil
	}
	host := pattern
	if strings.Contains(pattern, ":") {
		var port string
		var err error
		host, port, err = net.SplitHostPort(pattern)
		n, portErr := strconv.Atoi(port)
		if err != nil || portErr != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("unsupported Hub proxy exclusion: use a hostname, IP address, CIDR, host:port, or * wildcard")
		}
		if net.ParseIP(host) != nil {
			return pattern, nil
		}
	}
	if host == "" || strings.ContainsAny(host, "/@?#|, \\[]\t\r\n") {
		return "", fmt.Errorf("unsupported Hub proxy exclusion: use a hostname, IP address, CIDR, host:port, or * wildcard")
	}
	return pattern, nil
}

func compileHubExclusion(pattern string) func(*url.URL) bool {
	if !strings.Contains(pattern, "*") {
		// Native matching covers domains, IPs, CIDRs and optional ports.
		selectProxy := (&httpproxy.Config{HTTPProxy: "http://proxy", HTTPSProxy: "http://proxy", NoProxy: pattern}).ProxyFunc()
		return func(target *url.URL) bool {
			u, _ := selectProxy(target)
			return u == nil
		}
	}
	host, port, err := net.SplitHostPort(pattern)
	if err != nil {
		host, port = pattern, ""
	}
	// Hub supports '*' anywhere in a hostname, beyond native NO_PROXY syntax.
	expression := "^" + strings.ReplaceAll(regexp.QuoteMeta(host), `\*`, ".*") + "$"
	matches := regexp.MustCompile(expression).MatchString
	return func(target *url.URL) bool {
		targetPort := target.Port()
		if targetPort == "" {
			if target.Scheme == "https" {
				targetPort = "443"
			} else {
				targetPort = "80"
			}
		}
		return (port == "" || port == targetPort) && matches(strings.ToLower(target.Hostname()))
	}
}
