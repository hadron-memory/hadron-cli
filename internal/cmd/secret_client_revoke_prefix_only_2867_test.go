package cmd

import (
	"strings"
	"testing"
)

// cli#2867 (Holger's ruling 2026-11-22): the limitation that a manager's prefix-only row cannot be
// revoked through `secret client revoke` is accepted AND documented, and no `--grant` flag is built.
// The user-facing help says it: such a row has no full client id, an exact-id lookup cannot select
// it, the CLI never completes an id from a prefix, and nothing is sent.
func TestSecretClientRevokeHelpDocumentsThePrefixOnlyLimitation2867(t *testing.T) {
	help := strings.Join(strings.Fields(cmdHelpOut(t, "secret", "client", "revoke")), " ")
	for _, want := range []string{
		"prefix of its id",
		"never completes an id from a prefix",
		"exits 4",
		"sends nothing",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("secret client revoke --help must document the prefix-only limitation, lacks %q:\n%s", want, help)
		}
	}
	// The ruling: no revoke-by-grant path is built.
	if strings.Contains(help, "--grant") {
		t.Errorf("no --grant flag is built (ruling 2026-11-22):\n%s", help)
	}
}
