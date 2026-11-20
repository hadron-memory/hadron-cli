package api

import "bytes"

// RedactSecretText replaces every occurrence of each secret in s, and of its
// URL-encoded spellings (see secretSpellings), with a placeholder. It is for a
// command that submits a write-only secret and renders anything the server said
// back: an echoing server, proxy or gateway must never turn the secret into
// output. An empty secret is skipped (it would match everywhere).
func RedactSecretText(s string, secrets []string) string {
	return string(redactSecrets([]byte(s), nonEmpty(secrets)))
}

// HoldsSecret reports whether s contains any secret, in any spelling
// RedactSecretText would replace.
func HoldsSecret(s string, secrets []string) bool {
	for _, secret := range nonEmpty(secrets) {
		for _, spelling := range secretSpellings(secret) {
			if bytes.Contains([]byte(s), []byte(spelling)) {
				return true
			}
		}
	}
	return false
}

func nonEmpty(secrets []string) []string {
	out := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
