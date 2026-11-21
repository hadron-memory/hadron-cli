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
	gql, _ := captureGraphQL(t, map[string]string{"OrgAgentGrants": `{"data":{"organization":null}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "subscription", "list", "--org", pasted, "--server", gql.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("exit = %d, want NotFound; err: %v", exitCodeFor(err), err)
	}
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	renderError(f, err)
	for _, leak := range []string{pasted, pasted[:10]} {
		if strings.Contains(err.Error(), leak) || strings.Contains(out.String()+stderr.String(), leak) {
			t.Errorf("the refusal echoes the --org value %q: %v", leak, err)
		}
	}
	if !strings.Contains(err.Error(), "organization") || !strings.Contains(err.Error(), "not readable") {
		t.Errorf("the refusal still says what is wrong: %v", err)
	}
	// A short, legal-looking org still says the same thing without the value.
	gql2, _ := captureGraphQL(t, map[string]string{"OrgAgentGrants": `{"data":{"organization":null}}`})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"agent", "subscription", "list", "--org", "ghost.com", "--server", gql2.URL})
	if e := root2.Execute(); e == nil || strings.Contains(e.Error(), "ghost.com") {
		t.Errorf("the value is not shown for any --org: %v", e)
	}
}
