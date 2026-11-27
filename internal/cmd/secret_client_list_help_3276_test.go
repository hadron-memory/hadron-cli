package cmd

import (
	"strings"
	"testing"
)

// cli#3276: the end-reason column's dash means the client is live (the server's
// endReason is null while live), and the help names every value the server can
// answer. The help once said the opposite.
func TestSecretClientListHelpEndReasonMeaning3276(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(f.IOStreams.Out)
	root.SetArgs([]string{"secret", "client", "list", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, banned := range []string{"not that the client is live", "no reason reported"} {
		if strings.Contains(help, banned) {
			t.Errorf("help still carries the inverted wording %q:\n%s", banned, help)
		}
	}
	for _, want := range []string{
		"a dash means the client is live",
		"SECRET_DELETED", "PROTECTED", "REVOKED", "PENDING", "SPENT", "EXPIRED", "NOT_STARTED", "ISSUER_GONE", "ENDED",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help missing %q:\n%s", want, help)
		}
	}
}
