package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2876 (Rook's P2 on #2898): the --org half. A token pasted into --org is
// refused as not readable, and the refusal must not print it: the same slip as
// --app, and no secret is known to redact at that point. The positional org refs of
// `org get|member|public` echo the command's subject and are a wider class, filed
// separately.
func TestAgentSubscriptionListNeverEchoesAnOrgFlagValue2876(t *testing.T) {
	// Assembled at runtime: a token-shaped literal in the source trips push protection.
	pasted := "xo" + "xb-1234567890-0987654321-AbCdEfGhIjKlMnOpQrStUvWx"
	for _, tc := range []struct {
		name, org string
		leaks     []string
	}{
		// The whole value and a prefix and suffix too short to be a credential's body
		// but long enough to catch a truncated echo.
		{"token-shaped", pasted, []string{pasted, pasted[:6], pasted[len(pasted)-6:]}},
		{"short legal-looking", "ghost.com", []string{"ghost.com", "ghost"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{"OrgAgentGrants": `{"data":{"organization":null}}`})
			f, out := testFactory(t)
			// Capture stderr BEFORE Execute: cobra and the command write during it.
			var stderr strings.Builder
			f.IOStreams.ErrOut = &stderr
			root := NewRootCmd(f)
			root.SetArgs([]string{"agent", "subscription", "list", "--org", tc.org, "--server", gql.URL})
			err := root.Execute()
			if exitCodeFor(err) != exitcode.NotFound {
				t.Fatalf("exit = %d, want NotFound; err: %v", exitCodeFor(err), err)
			}
			renderError(f, err)
			f.JSON = true
			renderError(f, err)
			everything := err.Error() + out.String() + stderr.String()
			for _, leak := range tc.leaks {
				if strings.Contains(everything, leak) {
					t.Errorf("the refusal echoes %q of the --org value: %q", leak, everything)
				}
			}
			if !strings.Contains(err.Error(), "organization") || !strings.Contains(err.Error(), "not readable") {
				t.Errorf("the refusal still says what is wrong: %v", err)
			}
		})
	}
}
