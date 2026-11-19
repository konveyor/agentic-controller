package main

import (
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/konveyor/migration-harness/internal/redaction"
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

// newRedactor collects the given values plus every credential-looking
// variable in the current environment. Build it before the harness clears
// credentials from its environment, so what goose saw is what gets masked.
func newRedactor(known ...string) *redactor {
	var secrets []string
	add := func(v string) {
		if len(v) >= redaction.MinSecretLen && !slices.Contains(secrets, v) {
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

// addSecrets registers explicit credentials, including short passwords that the
// environment heuristic would skip, and keeps overlapping values longest first.
func (r *redactor) addSecrets(secrets ...string) {
	for _, secret := range secrets {
		if secret != "" && !slices.Contains(r.secrets, secret) {
			r.secrets = append(r.secrets, secret)
		}
	}
	slices.SortFunc(r.secrets, func(a, b string) int { return len(b) - len(a) })
}

// redact returns text with every known secret replaced by "[redacted]".
func (r *redactor) redact(text string) string {
	if r == nil {
		return text
	}
	var replacements []string
	for _, secret := range r.secrets {
		replacements = append(replacements, secret, "[redacted]")
	}
	// A single pass prevents short credentials from matching inside a
	// replacement marker inserted for an earlier secret.
	return strings.NewReplacer(replacements...).Replace(text)
}

// privateKeyFragments returns the values to mask for a PEM private key:
// the whole blob, and each line of its body.
//
// The lines are the point. The redactor matches literals, and a key
// never reaches a log line whole — anything that echoes one prints it a
// line at a time — so registering only the blob masks nothing that
// would actually leak.
//
// The PEM markers are excluded: they are identical in every key of that
// type, so masking them would redact the word "-----BEGIN OPENSSH
// PRIVATE KEY-----" out of unrelated text while protecting nothing.
// Short lines (the final, partial base64 line can be a few characters)
// are skipped for the same reason, on the same threshold the
// environment heuristic uses.
func privateKeyFragments(key string) []string {
	fragments := []string{key}
	for _, line := range strings.Split(key, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "-----") || len(line) < redaction.MinSecretLen {
			continue
		}
		fragments = append(fragments, line)
	}
	return fragments
}
