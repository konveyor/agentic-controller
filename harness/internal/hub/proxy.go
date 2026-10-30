package hub

import (
	"fmt"
	"net/http"

	"github.com/konveyor/migration-harness/internal/proxy"
	"github.com/konveyor/tackle2-hub/shared/api"
)

// FetchProxies resolves enabled proxies and their decrypted identities once
// per harness invocation, independent of application credential selection.
func (c *Client) FetchProxies() ([]proxy.Config, error) {
	list, err := c.rich.Proxy.List()
	if err != nil {
		return nil, fmt.Errorf("list Hub proxies: %s", proxyLookupFailure(err))
	}
	var configs []proxy.Config
	identities := make(map[uint]*api.Identity)
	for _, item := range list {
		if !item.Enabled {
			continue
		}
		config := proxy.Config{Kind: item.Kind, Host: item.Host, Port: item.Port, Excluded: item.Excluded}
		if item.Identity != nil {
			identity := identities[item.Identity.ID]
			if identity == nil {
				identity, err = c.rich.Identity.Decrypted().Get(item.Identity.ID)
				if err != nil {
					// Do not include a response body that may contain credentials.
					return nil, fmt.Errorf("read decrypted identity %d for Hub proxy %d: %s", item.Identity.ID, item.ID, proxyLookupFailure(err))
				}
				identities[item.Identity.ID] = identity
			}
			config.Username, config.Password = identity.User, identity.Password
		}
		configs = append(configs, config)
	}
	return configs, nil
}

// Hub REST errors include response bodies; strip those before exposing an
// error to logs, viewers or termination status. Keep the actionable status.
func proxyLookupFailure(err error) string {
	status := 0
	switch e := err.(type) {
	case *api.RestError:
		status = e.Status
	case *api.NotFound:
		status = e.Status
	case *api.Conflict:
		status = e.Status
	}
	if status != 0 {
		return fmt.Sprintf("HTTP %d %s", status, http.StatusText(status))
	}
	return "request failed"
}
