package cmd

import (
	"strings"
	"testing"
)

// flat collapses the hard-wrapped help into one line so a pin survives re-wrapping.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

func slackHelp(t *testing.T, args ...string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--help"))
	if e := root.Execute(); e != nil {
		t.Fatal(e)
	}
	return flat(out.String())
}

// cli#1399: the group page must not promise health, must say what ACTIVE does
// and does not mean, say the reason is not shown, and name the one next step
// that exists (an org admin registers the workspace again), with no portal pointer.
func TestSlackGroupHelpDoesNotPromiseHealth(t *testing.T) {
	h := slackHelp(t, "slack")
	for _, bad := range []string{"healthy", "health"} {
		if strings.Contains(strings.ToLower(h), bad) {
			t.Errorf("slack help must not mention %q: %s", bad, h)
		}
	}
	// The only portal mention is the statement that no portal page exists.
	if strings.Count(h, "portal") != 1 || !strings.Contains(h, "No CLI command or portal page does this yet") {
		t.Errorf("slack help must not point at a portal page: %s", h)
	}
	for _, want := range []string{
		"ACTIVE means the connection is registered",
		"does not prove it is working",
		"ERROR means Hadron has recorded this connection as failed",
		"not always recorded here",
		"reason is not shown here",
		"an organization admin registers the workspace again",
		"No CLI command or portal page does this yet",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("slack help missing %q: %s", want, h)
		}
	}
}

func TestSlackLeafHelpStatesTheirContract(t *testing.T) {
	st := slackHelp(t, "slack", "status")
	for _, want := range []string{
		"the list",
		"read-only",
		"default page size is 20",
		"--app does not apply",
		"saved organization is not used",
		"sign in first",
		"exit 3",
		"exit 2",
		"ACTIVE means registered, not proof the connection works",
	} {
		if !strings.Contains(strings.ToLower(st), strings.ToLower(want)) {
			t.Errorf("slack status help missing %q: %s", want, st)
		}
	}
	g := slackHelp(t, "slack", "get")
	for _, want := range []string{
		"read-only",
		"sign in first",
		"exit 3",
		"ACTIVE means registered, not proof the connection works",
		"The text output is one line per field",
	} {
		if !strings.Contains(strings.ToLower(g), strings.ToLower(want)) {
			t.Errorf("slack get help missing %q: %s", want, g)
		}
	}
}
