package git

import (
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

type Credentials struct {
	Username string
	Token    string
	RepoURL  string
	Branch   string
}

func (c *Credentials) Auth() *http.BasicAuth {
	return &http.BasicAuth{
		Username: c.Username,
		Password: c.Token,
	}
}
