package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/konveyor/migration-harness/internal/hub"
	"github.com/konveyor/migration-harness/internal/logging"
	"github.com/konveyor/migration-harness/internal/proxy"
)

// prepareMavenSettings runs in every invocation, independently of skills or
// workflow stage. An existing user settings file (including a Secret mount)
// takes precedence over Hub's assigned or default Maven credential. Broken
// symlinks fail setup instead of falling back to a Hub credential. The returned
// warning explains skipped proxy conversion for viewer and termination reporting.
func prepareMavenSettings(client *hub.Client, appID string, red *redactor, proxies ...proxy.Config) (warning string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return warning, fmt.Errorf("resolve home directory: %w", err)
	}
	path := filepath.Join(home, ".m2", "settings.xml")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			info, err = os.Stat(path)
			if err != nil {
				return warning, fmt.Errorf("inspect Maven user settings: %w", err)
			}
		}
		if !info.Mode().IsRegular() {
			return warning, fmt.Errorf("Maven user settings path %s is not a regular file", path)
		}
		settings, err := os.ReadFile(path)
		if err != nil {
			return warning, fmt.Errorf("read Maven user settings for secret redaction: %w", err)
		}
		if err := red.addMavenSettings(string(settings)); err != nil {
			return warning, err
		}
		logging.Info("using existing Maven user settings at %s", path)
		return warning, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return warning, fmt.Errorf("inspect Maven user settings: %w", err)
	}

	id, err := hub.ParseAppID(appID)
	if err != nil {
		return warning, fmt.Errorf("invalid APP_ID: %w", err)
	}
	identity, err := client.FetchMavenCreds(id)
	if err != nil {
		return warning, fmt.Errorf("fetch Maven credential: %w", err)
	}
	if identity == nil && len(proxies) == 0 {
		return warning, nil
	}
	settings := ""
	if identity != nil {
		if strings.TrimSpace(identity.Settings) == "" {
			return warning, fmt.Errorf("Maven credential %d has empty settings", identity.ID)
		}
		settings = identity.Settings
	}
	generated, err := withMavenProxies(settings, proxies)
	if errors.Is(err, errMavenProxyConversion) {
		warning = fmt.Sprintf("skipping automatic Maven proxy configuration: %v; continuing with supplied/default Maven settings; supply explicit Maven proxy settings if needed", err)
		logging.Warn("%s", red.redact(warning))
		if identity == nil {
			return warning, nil
		}
	} else if err != nil {
		return warning, err
	} else {
		settings = generated
	}
	if err := red.addMavenSettings(settings); err != nil {
		return warning, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return warning, fmt.Errorf("create Maven settings directory: %w", err)
	}
	// Exclusive creation also preserves a file or symlink created while the
	// Hub request was in flight. Settings can contain repository passwords.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return warning, fmt.Errorf("create Maven user settings: %w", err)
	}
	if err := writeMavenSettings(f, path, settings); err != nil {
		return warning, err
	}
	logging.Ok("Maven settings prepared at %s", path)
	return warning, nil
}

// addMavenSettings registers both decoded and literal XML credential values so
// an agent quoting a password or the settings file cannot expose either form.
func (r *redactor) addMavenSettings(settings string) error {
	decoder := xml.NewDecoder(strings.NewReader(settings))
	var secrets []string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			r.addSecrets(secrets...)
			return nil
		}
		if err != nil {
			// XML errors can quote a tag containing credential material.
			return errors.New("parse Maven user settings for secret redaction")
		}
		start, ok := token.(xml.StartElement)
		if !ok || (start.Name.Local != "password" && start.Name.Local != "passphrase") {
			continue
		}
		var value struct {
			Decoded string `xml:",chardata"`
			Literal string `xml:",innerxml"`
		}
		if err := decoder.DecodeElement(&value, &start); err != nil {
			return errors.New("parse Maven user settings for secret redaction")
		}
		if strings.TrimSpace(value.Decoded) == "" {
			continue
		}
		secrets = append(secrets, value.Decoded, strings.TrimSpace(value.Decoded), value.Literal, strings.TrimSpace(value.Literal))
	}
}

// writeMavenSettings closes the file and removes incomplete settings on failure.
func writeMavenSettings(f io.WriteCloser, path, settings string) error {
	_, writeErr := io.WriteString(f, settings)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		if removeErr := os.Remove(path); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("remove incomplete Maven user settings: %w", removeErr))
		}
		return fmt.Errorf("write Maven user settings: %w", err)
	}
	return nil
}
