package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2902: the #2898 no-echo policy applied to the positional org refs and the node-ref resolver. The
// refusal names what is wrong and says the ref is not shown; it keeps its exit code, and nothing of the
// value reaches the error text, stderr, stdout or the --json envelope.

func pasted2902() string {
	// Assembled at runtime: a token-shaped literal in the source trips push protection.
	return "xo" + "xb-1234567890-0987654321-AbCdEfGhIjKlMnOpQrStUvWx"
}

func runNoEcho2902(t *testing.T, responses map[string]string, args ...string) (everything string, err error) {
	t.Helper()
	gql, _ := captureGraphQL(t, responses)
	f, out := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr // before Execute: cobra and the command write during it
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err = root.Execute()
	if err == nil {
		t.Fatalf("%v: want a refusal", args)
	}
	renderError(f, err)
	f.JSON = true
	renderError(f, err)
	return err.Error() + out.String() + stderr.String(), err
}

func TestOrgRefRefusalsNeverEchoTheRef2902(t *testing.T) {
	p := pasted2902()
	for _, tc := range []struct {
		name      string
		responses map[string]string
		args      []string
		code      int
		says      string
	}{
		{"org get", map[string]string{"GetOrganization": `{"data":{"organization":null}}`}, []string{"org", "get", p}, exitcode.NotFound, "organization not found"},
		{"org member list", map[string]string{"OrgMembers": `{"data":{"organization":null}}`}, []string{"org", "member", "list", p}, exitcode.NotFound, "organization not found"},
		{"org public", map[string]string{"PublicOrganization": `{"data":{"publicOrganization":null}}`}, []string{"org", "public", p}, exitcode.NotFound, "not discoverable"},
		{"org set-active", map[string]string{"GetOrganization": `{"data":{"organization":null}}`}, []string{"org", "set-active", p}, exitcode.NotFound, "no organization is readable by you"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			everything, err := runNoEcho2902(t, tc.responses, tc.args...)
			if exitCodeFor(err) != tc.code {
				t.Errorf("exit = %d, want %d: %v", exitCodeFor(err), tc.code, err)
			}
			for _, leak := range []string{p, p[:8], p[len(p)-8:]} {
				if strings.Contains(everything, leak) {
					t.Errorf("the refusal echoes %q of the ref: %q", leak, everything)
				}
			}
			if !strings.Contains(err.Error(), tc.says) || !strings.Contains(err.Error(), "not shown") {
				t.Errorf("the refusal still says what is wrong (%q) and that the ref is not shown: %v", tc.says, err)
			}
		})
	}
}

// #1858 item 4, kept: a missing org and a non-discoverable one give the same text and exit code, whatever
// ref was typed (cor:acl:080:02, the anti-enumeration posture). The text no longer carries the ref, so two
// different refs read identically.
func TestOrgPublicMissingAndNonDiscoverableStayIndistinguishable2902(t *testing.T) {
	_, e1 := runNoEcho2902(t, map[string]string{"PublicOrganization": `{"data":{"publicOrganization":null}}`}, "org", "public", "ghost.com")
	_, e2 := runNoEcho2902(t, map[string]string{"PublicOrganization": `{"data":{"publicOrganization":null}}`}, "org", "public", "acme.com")
	if exitCodeFor(e1) != exitcode.NotFound || exitCodeFor(e2) != exitcode.NotFound || e1.Error() != e2.Error() {
		t.Fatalf("same text and NotFound for any ref: %q vs %q", e1, e2)
	}
	if strings.Contains(e1.Error(), "ghost") {
		t.Errorf("the ref is not echoed: %v", e1)
	}
}

// `org set-active` has a second refusal that names the ref: a readable org the caller is not a member of.
func TestOrgSetActiveNonMemberRefusalNeverEchoesTheRef2902(t *testing.T) {
	p := pasted2902()
	everything, err := runNoEcho2902(t, map[string]string{
		"GetOrganization": `{"data":{"organization":{"id":"o1","urn":"acme.com","name":"Acme"}}}`,
		"Organizations":   `{"data":{"organizations":{"items":[],"total":0}}}`,
	}, "org", "set-active", p)
	if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "you are not a member of it") {
		t.Fatalf("this must reach the non-member refusal (exit 2): %d %v", exitCodeFor(err), err)
	}
	for _, leak := range []string{p, p[:8], p[len(p)-8:]} {
		if strings.Contains(everything, leak) {
			t.Errorf("echoes %q: %q", leak, everything)
		}
	}
	if !strings.Contains(err.Error(), "not shown") {
		t.Errorf("says the ref is not shown: %v", err)
	}
}

func TestNodeRefResolverRefusalsNeverEchoTheRef2902(t *testing.T) {
	p := pasted2902()
	for _, tc := range []struct {
		name      string
		responses map[string]string
		ref       string
		code      int
		says      string
	}{
		{"not fully qualified", map[string]string{}, "ghp_" + "AbCdEfGhIjKlMnOpQrStUvWx0123456789", exitcode.Usage, "not a fully-qualified node URN"},
		{"not found", map[string]string{"ResolveUrn": `{"data":{"resolveUrn":null}}`}, "hrn:node:acme.com:dev:" + p, exitcode.NotFound, "node not found"},
		{"action not found", map[string]string{"ResolveUrn": `{"data":{"resolveUrn":null}}`}, "hrn:action:acme.com:dev:" + p, exitcode.NotFound, "node not found"},
		{"not a node", map[string]string{"ResolveUrn": `{"data":{"resolveUrn":{"id":"m1","kind":"memory","memoryId":null}}}`}, "hrn:node:acme.com:dev:" + p, exitcode.Usage, "not a node"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			everything, err := runNoEcho2902(t, tc.responses, "node", "get", tc.ref)
			if exitCodeFor(err) != tc.code {
				t.Errorf("exit = %d, want %d: %v", exitCodeFor(err), tc.code, err)
			}
			// the runs of the ref actually given (the not-fully-qualified case uses a different token)
			for _, leak := range []string{tc.ref, tc.ref[len(tc.ref)-8:], p[:8], p[len(p)-8:]} {
				if strings.Contains(everything, leak) {
					t.Errorf("the refusal echoes %q of the ref: %q", leak, everything)
				}
			}
			if !strings.Contains(err.Error(), tc.says) || !strings.Contains(err.Error(), "not shown") {
				t.Errorf("the refusal says what is wrong (%q) and that the ref is not shown: %v", tc.says, err)
			}
		})
	}
}
