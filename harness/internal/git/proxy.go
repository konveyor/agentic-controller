package git

import (
	"net/http"
	"net/url"

	"github.com/go-git/go-git/v5/plumbing/transport/client"
	gitHTTP "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// ConfigureProxy installs the harness's HTTP policy for all go-git traffic,
// including clone/fetch, write-access discovery and every push. Call once at
// startup, before any Git goroutines; restore only after the watcher stops.
// The transport is private to go-git: Hub, the runtime environment and on-disk
// Git configuration never receive Hub proxy credentials.
func ConfigureProxy(selectProxy func(*http.Request) (*url.URL, error)) func() {
	oldHTTP, oldHTTPS := client.Protocols["http"], client.Protocols["https"]
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = selectProxy
	transport := gitHTTP.NewClient(&http.Client{Transport: tr})
	client.InstallProtocol("http", transport)
	client.InstallProtocol("https", transport)
	return func() {
		tr.CloseIdleConnections()
		client.InstallProtocol("http", oldHTTP)
		client.InstallProtocol("https", oldHTTPS)
	}
}
