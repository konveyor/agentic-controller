package main

import (
	"os"
	"regexp"
	"slices"
	"strings"
)

// redactor masks the secrets the harness holds wherever they appear in text
// the model produced. Logging the agent's closing message opened a channel
// that did not exist before; goose inherits the harness's environment, so a
// model that ends its turn by quoting a key it found in `env` must not put it
// in the pod log.
type redactor struct {
	secrets []string
}

// secretEnvName matches environment variables whose value is a credential,
// by the names the controller, the gateway Secret and the harness use.
var secretEnvName = regexp.MustCompile(`(?i)(SECRET|TOKEN|PASSWORD|PASSWD|API_KEY|ACCESS_KEY|CREDENTIALS)`)

// minSecretLen keeps ids and flags ("7", "true") out of the list: replacing
// those would mangle ordinary prose.
const minSecretLen = 8

// newRedactor collects the given values plus every credential-looking
// variable in the current environment. Build it before the harness clears
// credentials from its environment, so what goose saw is what gets masked.
func newRedactor(known ...string) *redactor {
	var secrets []string
	add := func(v string) {
		if len(v) >= minSecretLen && !slices.Contains(secrets, v) {
			secrets = append(secrets, v)
		}
	}
	for _, v := range known {
		add(v)
	}
	for _, kv := range os.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if ok && secretEnvName.MatchString(name) {
			add(value)
		}
	}
	// Longest first, so a secret that contains another is masked whole.
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	return &redactor{secrets: secrets}
}

// redact returns text with every known secret replaced by "[redacted]".
func (r *redactor) redact(text string) string {
	if r == nil {
		return text
	}
	for _, s := range r.secrets {
		text = strings.ReplaceAll(text, s, "[redacted]")
	}
	return text
}
