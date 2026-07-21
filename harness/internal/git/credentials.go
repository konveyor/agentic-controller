package git

import (
	"fmt"
	"os"

	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

type Credentials struct {
	Username string
	Token    string
	RepoURL  string
	Branch   string
}

func ReadFromEnv() (*Credentials, error) {
	repoURL := os.Getenv("GIT_REPO_URL")
	if repoURL == "" {
		return nil, nil
	}

	username := os.Getenv("GIT_USERNAME")
	if username == "" {
		username = "x-access-token"
	}

	token := os.Getenv("GIT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("GIT_REPO_URL is set but GIT_TOKEN is missing")
	}

	branch := os.Getenv("GIT_TARGET_BRANCH")
	if branch == "" {
		return nil, fmt.Errorf("GIT_REPO_URL is set but GIT_TARGET_BRANCH is missing")
	}

	return &Credentials{
		Username: username,
		Token:    token,
		RepoURL:  repoURL,
		Branch:   branch,
	}, nil
}

func (c *Credentials) Auth() *http.BasicAuth {
	return &http.BasicAuth{
		Username: c.Username,
		Password: c.Token,
	}
}

func ClearEnvCredentials() {
	os.Unsetenv("GIT_TOKEN")
}
