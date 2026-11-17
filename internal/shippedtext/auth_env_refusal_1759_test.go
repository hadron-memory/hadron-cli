package shippedtext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hadron-cli#1759 row 5 (Uma UX P3 on #1632): after a refused HADRON_TOKEN the
// how-to names the variable as the thing to check. It does not offer re-login
// as the remedy, since a stored login does not replace an HADRON_TOKEN that is
// set. The auth-status policy of #1752 is not folded in here.
func TestAuthHowTo1759NamesHadronTokenAfterItsRefusal(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "how-to", "authentication.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(b)), " ")
	for _, want := range []string{
		"If `HADRON_TOKEN` is set, it is the credential that was sent, so check that variable's value",
		"`hadron auth login` stores a login that `HADRON_TOKEN` still overrides",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/how-to/authentication.md is missing %q", want)
		}
	}
}
