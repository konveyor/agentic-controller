package hub

import (
	"os"
	"strconv"

	"github.com/konveyor/tackle2-hub/shared/api"
	"github.com/konveyor/tackle2-hub/shared/binding"
	"github.com/konveyor/tackle2-hub/shared/binding/auth"
)

// Client wraps the Hub RichClient for the subset of operations the harness needs.
type Client struct {
	rich *binding.RichClient
}

// NewClient creates a Hub API client with bearer token auth.
func NewClient(baseURL, token string) *Client {
	rc := binding.New(baseURL)
	rc.Client.Use(auth.NewBearer(token))
	return &Client{rich: rc}
}

// FetchApp retrieves application metadata including the source repository.
func (c *Client) FetchApp(appID uint) (*api.Application, error) {
	return c.rich.Application.Get(appID)
}

// FetchGitCreds retrieves git credentials for an application's source repo.
// Tries direct identity (app-specific, role=source) first, then falls back
// to an indirect identity (global default, kind=source).
func (c *Client) FetchGitCreds(appID uint) (*api.Identity, error) {
	selected := c.rich.Application.Select(appID)
	identity, found, err := selected.Identity.Decrypted().Search().
		Direct("source").
		Indirect("source").
		Find()
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return identity, nil
}

// FetchAnalysis retrieves analysis insights for an application.
// Returns an empty slice (not an error) if no analysis exists.
func (c *Client) FetchAnalysis(appID uint) ([]api.Insight, error) {
	selected := c.rich.Application.Select(appID)
	insights, err := selected.Analysis.ListInsights()
	if err != nil {
		return nil, err
	}
	return insights, nil
}

// ParseAppID converts the APP_ID string to uint.
func ParseAppID(s string) (uint, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(n), nil
}

// ClearEnv removes Hub credentials from the process environment
// so child processes (goose) cannot access them.
func ClearEnv() {
	os.Unsetenv("HUB_BASE_URL")
	os.Unsetenv("HUB_TOKEN")
	os.Unsetenv("APP_ID")
}
