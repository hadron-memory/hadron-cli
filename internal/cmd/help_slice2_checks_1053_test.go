package cmd

import (
	"strings"
	"testing"
)

// cli#1053 slice 2 (the remaining app and agent pages): facts the evaluator's
// round 1 found missing or wrong, pinned so a later edit cannot drop them.

func flatHelp1053S2(t *testing.T, path ...string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{}, path...), "--help"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// Each page says the thing round 1 said a reader could not find on it.
func TestSlice2PagesSayWhatRound1FoundMissing1053(t *testing.T) {
	for _, tc := range []struct {
		page []string
		want []string
	}{
		// where a <revision-loc> comes from, and that the CLI cannot list them
		{[]string{"agent", "revision", "publish"}, []string{`"hadron agent revision create" printed it`}},
		{[]string{"agent", "revision", "restore"}, []string{`"hadron agent revision create" printed it`, "REVISION_SLOTS_EXHAUSTED"}},
		{[]string{"agent", "revision"}, []string{"Listing an Agent's revisions is not available from the CLI yet"}},
		// the default view of an Agent, not only its edge cases
		{[]string{"agent", "get"}, []string{"the Agent's name, URN and ID", "offer readiness", "exits 4"}},
		// roles come from the roles page
		{[]string{"app", "member", "add"}, []string{`Run "hadron app member roles"`}},
		// which App, and how to get an App key
		{[]string{"app", "user", "create"}, []string{`mint one with "hadron app key create"`, "there is no --app"}},
		// what a scope is, how to see them, and that nothing is asked first
		{[]string{"app", "key", "scope", "revoke"}, []string{"attention:deliver or host:operate", "--scopes", "no confirmation prompt"}},
		{[]string{"app", "key", "list"}, []string{"a host is a machine that launches a team App's workers", "PREVIEW"}},
		// what a grant is comes before the page's own details
		{[]string{"app", "memory-grant", "list"}, []string{`"hadron app memory-grant add"`, "REGISTER"}},
		// what properties hold, with a minimal object
		{[]string{"app", "properties", "set"}, []string{`{"team": {"note": "hello"}}`, "taskIntegrations"}},
	} {
		help := flatHelp1053S2(t, tc.page...)
		for _, w := range tc.want {
			if !strings.Contains(help, w) {
				t.Errorf("%v help lacks %q", tc.page, w)
			}
		}
	}
}

// API operation names and release-note words are for the maintainers, not the
// reader of a page.
func TestSlice2HelpHasNoOperationNamesOrReleaseNotes1053(t *testing.T) {
	banned := []string{"joinApp", "leaveApp", "revokeAppKey", "dark-worker", "BREAKING:", "AppAgent.", "bootPromptServe"}
	for _, page := range [][]string{
		{"app", "join"}, {"app", "leave"}, {"app", "key", "revoke"}, {"app", "uninstall"}, {"app", "delete"},
		{"app", "reinstall"}, {"app", "agent", "list"}, {"app", "member", "add"}, {"app", "memories"},
	} {
		help := flatHelp1053S2(t, page...)
		for _, b := range banned {
			if strings.Contains(help, b) {
				t.Errorf("%v help carries %q", page, b)
			}
		}
	}
}

// The --instruction explanation is one short block that points at one longer
// explanation, not 25 lines pasted on every page that takes the flag.
func TestInstructionBlockIsShortAndPointsAtTheLongerExplanation1053(t *testing.T) {
	for _, page := range [][]string{
		{"app", "member", "add"}, {"app", "member", "remove"}, {"app", "leave"},
		{"app", "model-group", "set"}, {"app", "properties", "set"},
		{"app", "key", "revoke"}, {"app", "memory-grant", "list"},
	} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{}, page...), "--help"))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		i := strings.Index(text, "--instruction <kind>:<ref> is for an AI worker")
		if i < 0 {
			t.Errorf("%v: no --instruction block", page)
			continue
		}
		block := text[i:]
		if j := strings.Index(block, "\n\n"); j >= 0 {
			// the block is two paragraphs; take through the second
			if k := strings.Index(block[j+2:], "\n\n"); k >= 0 {
				block = block[:j+2+k]
			}
		}
		if n := strings.Count(block, "\n"); n > 20 {
			t.Errorf("%v: the --instruction block is %d lines; keep it short and point at agentic-usage", page, n)
		}
		if !strings.Contains(strings.Join(strings.Fields(block), " "), "hadron agentic-usage") {
			t.Errorf("%v: the --instruction block does not point at hadron agentic-usage", page)
		}
	}
}
