package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/konveyor/migration-harness/internal/proxy"
)

type mavenProxy struct {
	ID            string `xml:"id"`
	Active        bool   `xml:"active"`
	Protocol      string `xml:"protocol"`
	Host          string `xml:"host"`
	Port          int    `xml:"port"`
	Username      string `xml:"username,omitempty"`
	Password      string `xml:"password,omitempty"`
	NonProxyHosts string `xml:"nonProxyHosts,omitempty"`
}

// withMavenProxies augments only harness-generated settings. An explicit
// proxies section in the Hub Maven credential wins, even if empty or inactive.
// Preserve all original XML bytes rather than round-tripping a partial model
// that might discard profiles, extensions, namespaces or unknown fields.
func withMavenProxies(settings string, configs []proxy.Config) (string, error) {
	if len(configs) == 0 {
		return settings, nil
	}
	if settings == "" {
		settings = "<settings/>"
	}
	decoder := xml.NewDecoder(strings.NewReader(settings))
	depth, rootEnd, rootClose := 0, 0, 0
	var root xml.StartElement
	var rootName string
	hasProxies := false
	for {
		before := int(decoder.InputOffset())
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", errors.New("parse Maven settings for Hub proxy configuration")
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				if rootEnd != 0 || token.Name.Local != "settings" {
					return "", errors.New("Hub Maven credential must contain one settings element")
				}
				root, rootEnd = token, int(decoder.InputOffset())
				raw := settings[before+1 : rootEnd]
				rootName = raw[:strings.IndexAny(raw, " \t\r\n/>")]
			}
			if depth == 1 && token.Name.Local == "proxies" {
				hasProxies = true
			}
			depth++
		case xml.EndElement:
			depth--
			if depth == 0 {
				rootClose = before
			}
		}
	}
	if rootEnd == 0 || depth != 0 {
		return "", errors.New("Hub Maven credential must contain one settings element")
	}
	if hasProxies {
		return settings, nil
	}
	section := struct {
		XMLName xml.Name
		Proxies []mavenProxy `xml:"proxy"`
	}{XMLName: xml.Name{Space: root.Name.Space, Local: "proxies"}}
	for _, c := range configs {
		// Maven interpolates settings before applying proxy authentication and
		// provides no literal escape here. Never silently change a Hub secret.
		if strings.Contains(c.Username, "${") || strings.Contains(c.Password, "${") {
			return "", errors.New("Hub proxy credentials contain Maven interpolation syntax; supply explicit Maven proxy settings with encrypted credentials or set HARNESS_HUB_PROXY=off")
		}
		port := c.Port
		if port == 0 {
			port = 80
		}
		nonProxyHosts, err := mavenNonProxyHosts(c.Excluded)
		if err != nil {
			return "", err
		}
		section.Proxies = append(section.Proxies, mavenProxy{
			ID: "konveyor-hub-" + c.Kind, Active: true, Protocol: c.Kind,
			Host: c.Host, Port: port, Username: c.Username, Password: c.Password,
			NonProxyHosts: nonProxyHosts,
		})
	}
	data, err := xml.Marshal(section)
	if err != nil {
		return "", fmt.Errorf("encode Hub Maven proxy configuration: %w", err)
	}
	if strings.HasSuffix(settings[:rootEnd], "/>") {
		return settings[:rootEnd-2] + ">" + string(data) + "</" + rootName + ">" + settings[rootEnd:], nil
	}
	return settings[:rootClose] + string(data) + settings[rootClose:], nil
}

// Maven has no CIDR or port-specific bypass syntax. Validate only after all
// supplied settings overrides have won, rather than blocking Git configuration.
func mavenNonProxyHosts(excluded []string) (string, error) {
	var hosts []string
	for _, raw := range excluded {
		h := strings.ToLower(strings.TrimSpace(raw))
		if h == "" {
			continue
		}
		if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
			h = ip.String()
			hosts = append(hosts, h)
			if strings.Contains(h, ":") {
				hosts = append(hosts, "["+h+"]")
			}
			continue
		}
		if strings.ContainsAny(h, "/:@?#| \\[]\t\r\n") {
			return "", errors.New("Hub proxy exclusions cannot be represented in Maven settings; supply explicit Maven proxy settings or set HARNESS_HUB_PROXY=off")
		}
		switch {
		case strings.HasPrefix(h, "."):
			hosts = append(hosts, "*"+h)
		case !strings.Contains(h, "*"):
			hosts = append(hosts, h, "*."+h)
		default:
			hosts = append(hosts, h)
		}
	}
	return strings.Join(hosts, "|"), nil
}
