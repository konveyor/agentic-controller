package hub

import (
	"errors"
	"os"
	"testing"

	"github.com/konveyor/tackle2-hub/shared/api"
)

func TestValidateSourceRepository(t *testing.T) {
	tests := []struct {
		name    string
		repo    *api.Repository
		wantErr bool
	}{
		{"git kind", &api.Repository{Kind: "git", URL: "https://github.com/acme/app.git"}, false},
		{"empty kind defaults to git", &api.Repository{Kind: "", URL: "https://github.com/acme/app.git"}, false},
		{"git kind uppercase", &api.Repository{Kind: "GIT", URL: "https://github.com/acme/app.git"}, false},
		{"git kind padded", &api.Repository{Kind: " git ", URL: "https://github.com/acme/app.git"}, false},
		{"subversion rejected", &api.Repository{Kind: "subversion", URL: "https://svn.example/repo"}, true},
		{"unknown kind rejected", &api.Repository{Kind: "mercurial", URL: "https://hg.example/repo"}, true},
		{"empty URL rejected", &api.Repository{Kind: "git", URL: ""}, true},
		{"nil repository rejected", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSourceRepository(tt.repo)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSourceRepository() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrUnsupportedSourceSCM) {
				t.Errorf("error %v does not wrap ErrUnsupportedSourceSCM", err)
			}
		})
	}
}

func TestParseAppID(t *testing.T) {
	tests := []struct {
		input   string
		want    uint
		wantErr bool
	}{
		{"42", 42, false},
		{"0", 0, false},
		{"1000000", 1000000, false},
		{"", 0, true},
		{"abc", 0, true},
		{"-1", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseAppID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseAppID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseAppID(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestClearEnvRemovesAllHubVars(t *testing.T) {
	vars := []string{"HUB_BASE_URL", "HUB_TOKEN", "HUB_TOKEN_ID", "APP_ID"}
	for _, k := range vars {
		t.Setenv(k, "test-value")
	}

	ClearEnv()

	for _, k := range vars {
		if v := os.Getenv(k); v != "" {
			t.Errorf("ClearEnv should unset %s, got %q", k, v)
		}
	}
}

// ValidateSourceRepository gates on Kind, not on the URL scheme, so SSH
// remotes are accepted. That is now correct: the harness builds
// public-key auth for them from the Hub identity's key (see
// internal/git/credentials.go). It was not correct before that fix,
// when an accepted SSH URL went on to die in the clone with "invalid
// auth method".
func TestValidateSourceRepositoryAcceptsSSHURLs(t *testing.T) {
	for _, url := range []string{
		"ssh://git@git-ssh.example.com:443/acme/app.git",
		"git@git-ssh.example.com:acme/app.git",
	} {
		t.Run(url, func(t *testing.T) {
			err := ValidateSourceRepository(&api.Repository{Kind: "git", URL: url})
			if err != nil {
				t.Fatalf("ValidateSourceRepository(%q) = %v; SSH remotes are supported", url, err)
			}
		})
	}
}
