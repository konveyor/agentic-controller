// Package proxy resolves the harness's tool-specific use of Hub proxies.
// It never changes the process environment or the Hub client's transport.
package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
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
		if !ok {
			value, ok = lookup(strings.ToLower(key))
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
	return p, nil
}

func (p *Policy) addSecrets(u *url.URL) {
	if u.User == nil {
		return
	}
	password, _ := u.User.Password()
	if password != "" {
		p.secrets = append(p.secrets, password, url.PathEscape(password), url.QueryEscape(password), u.String(), u.User.String())
	}
}

func (p *Policy) Secrets() []string { return slices.Clone(p.secrets) }

// MavenProxies uses one bypass list for all inherited proxies. This matches
// Hub's UI and prevents Maven's HTTP fallback bypassing an HTTPS exclusion.
func (p *Policy) MavenProxies() []Config {
	configs := slices.Clone(p.configs)
	for i := range configs {
		configs[i].Excluded = slices.Clone(p.excluded)
		// Java URL hosts can retain the brackets around IPv6 addresses.
		for _, h := range p.excluded {
			if strings.Contains(h, ":") {
				configs[i].Excluded = append(configs[i].Excluded, "["+h+"]")
			}
		}
	}
	return configs
}

// GitProxy preserves explicit per-scheme environment settings (including an
// explicit empty value for direct access). NO_PROXY replaces inherited host
// exclusions when supplied and retains Go's native environment semantics.
func (p *Policy) GitProxy(r *http.Request) (*url.URL, error) {
	scheme := strings.ToLower(r.URL.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, nil
	}
	key := strings.ToUpper(scheme) + "_PROXY"
	if value, explicit := p.env[key]; explicit {
		return (&httpproxy.Config{HTTPProxy: value, HTTPSProxy: value, NoProxy: p.env["NO_PROXY"]}).ProxyFunc()(r.URL)
	}
	u := p.urls[scheme]
	if u == nil {
		return nil, nil
	}
	if value, explicit := p.env["NO_PROXY"]; explicit {
		if value == "" {
			return u, nil
		}
		return (&httpproxy.Config{HTTPProxy: u.String(), HTTPSProxy: u.String(), NoProxy: value}).ProxyFunc()(r.URL)
	}
	host := strings.ToLower(r.URL.Hostname())
	for _, pattern := range p.excluded {
		if pattern == host {
			return nil, nil
		}
		if match, _ := path.Match(pattern, host); match {
			return nil, nil
		}
	}
	return u, nil
}
