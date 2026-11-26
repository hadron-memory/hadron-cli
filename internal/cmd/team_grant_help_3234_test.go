package cmd

import (
	"strings"
	"testing"
)

// cli#3234: the signing help follow-ups from the #3231 review. Every pin here
// is red on a main before them.

func help3234(t *testing.T, argv ...string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(argv, "--help"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// Item 1: the server also answers a person for a signing request (SDL of
// requestGrantSignature and grantSigningRequest), so the pages that read, ask
// for or withdraw one no longer say only a worker session is answered.
func TestGrantHelp3234SigningCallers(t *testing.T) {
	const workerOnly = "answers only for a worker session"
	for _, argv := range [][]string{
		{"team", "grant", "request-signature"},
		{"team", "grant", "signing-request"},
		{"team", "grant", "signing-request", "get"},
		{"team", "grant", "signing-request", "withdraw"},
	} {
		text := strings.Join(strings.Fields(help3234(t, argv...)), " ")
		if strings.Contains(text, workerOnly) || strings.Contains(text, "this worker made") {
			t.Errorf("%v help still says only a worker is answered:\n%s", argv, text)
		}
		for _, s := range []string{
			"a bound worker session in its own App",
			"a person acting as themselves (no worker binding, not impersonating) who manages the grant's live, installed App",
			"the person who issued the client, while a participant of the App",
			"Only the account that asked may withdraw",
			"prints the server's answer",
		} {
			if !strings.Contains(text, s) {
				t.Errorf("%v help lacks %q:\n%s", argv, s, text)
			}
		}
	}
	// `request` sends no App ref, so only a worker session is answered there.
	text := strings.Join(strings.Fields(help3234(t, "team", "grant", "request")), " ")
	if !strings.Contains(text, workerOnly) {
		t.Errorf("request help lost its worker-only line:\n%s", text)
	}
}

// Item 2: the grant group page is wrapped like the rest of it.
func TestGrantHelp3234GroupPageWrapped(t *testing.T) {
	long, _, _ := strings.Cut(help3234(t, "team", "grant"), "\nUsage:")
	for _, line := range strings.Split(long, "\n") {
		if len(line) > 80 {
			t.Errorf("line of %d columns: %q", len(line), line)
		}
	}
}

// Item 3: the selection's states as the SDL's GrantSigningSelectionState has
// them, and that there is no STALE one.
func TestGrantHelp3234SelectionStates(t *testing.T) {
	text := strings.Join(strings.Fields(help3234(t, "team", "grant", "signing-request", "get-selection")), " ")
	for _, s := range []string{
		"OPEN the person can still sign",
		"COMPLETED one touch was verified",
		"DECLINED the signer turned the whole selection down",
		"EXPIRED its lifetime passed",
		"WITHDRAWN the requester withdrew it",
		"A selection has no STALE state: a grant that changes ends only its own item",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("get-selection help lacks %q:\n%s", s, text)
		}
	}
}
