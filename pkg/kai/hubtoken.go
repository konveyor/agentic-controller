package kai

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// hubTokenFile is the name of the file, under the kai config directory, that
// holds the credentials minted by 'hub login'.
const hubTokenFile = "hub.json"

// hubCredentials is the on-disk record written by 'hub login' and read when a
// run needs to auto-inject HUB_TOKEN. HubURL records which Hub the token was
// minted against (for reference/logout messaging); Token is the durable apikey.
type hubCredentials struct {
	HubURL     string    `json:"hubURL"`
	Token      string    `json:"token"`
	Expiration time.Time `json:"expiration,omitempty"`
}

// hubConfigDir returns the directory where kai stores its state
// (e.g. ~/.config/kai on Linux, ~/Library/Application Support/kai on macOS).
func hubConfigDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine user config directory: %w", err)
	}
	return filepath.Join(dir, "kai"), nil
}

// saveHubToken writes the credentials to hub.json under dir, creating the
// directory if needed. The file is written 0600 so the token is not
// world-readable.
func saveHubToken(dir string, c hubCredentials) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create config directory %q: %w", dir, err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, hubTokenFile)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("cannot write %q: %w", path, err)
	}
	return nil
}

// loadHubToken reads the credentials from hub.json under dir. A missing file is
// not an error: it returns the zero value so callers (runs) stay clean when the
// user has not logged in.
func loadHubToken(dir string) (hubCredentials, error) {
	var c hubCredentials
	path := filepath.Join(dir, hubTokenFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return c, fmt.Errorf("cannot read %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("cannot parse %q: %w", path, err)
	}
	return c, nil
}

// deleteHubToken removes hub.json under dir. A missing file is not an error.
func deleteHubToken(dir string) error {
	path := filepath.Join(dir, hubTokenFile)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot remove %q: %w", path, err)
	}
	return nil
}
