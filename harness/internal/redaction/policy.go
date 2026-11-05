// Package redaction defines shared credential-redaction policy.
package redaction

// MinSecretLen is the heuristic floor for potential secrets and ordinary
// usernames. Explicit passwords and username-only tokens have no length floor.
const MinSecretLen = 8
