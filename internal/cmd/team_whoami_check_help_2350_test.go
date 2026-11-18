package cmd

import (
	"strings"
	"testing"
)

// cli#2350 item 2: the --check help says what the fallback does. With a binding
// it checks the bound session and exits 14 if that session ended; with no
// binding it checks every open candidate (a STATE column, candidateStates in
// --json) and does not exit 14, because the fallback lists only open sessions.
// The exit-14 guarantee stays scoped to the bound session.
func TestWhoamiCheckHelpDescribesTheFallback2350(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	cmd, _, err := root.Find([]string{"team", "session", "whoami"})
	if err != nil {
		t.Fatal(err)
	}
	usage := cmd.Flags().Lookup("check").Usage
	for _, want := range []string{
		"bound session",
		"exits 14 if it has ended",
		"no binding",
		"each candidate",
		"never exits 14",
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("the --check flag help lacks %q: %s", want, usage)
		}
	}
	root.SetArgs([]string{"team", "session", "whoami", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	long := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{
		"With no binding here, the server fallback runs the same check on every candidate session it lists",
		"a STATE column in the table, `candidateStates` in --json, and one state read per candidate",
		"Exit 14 belongs to the bound session only: the fallback lists only open sessions and never exits 14.",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("whoami help lacks the sentence %q", want)
		}
	}
}

// The help promises the fallback never exits 14, so the promise is pinned: even
// when the one candidate's state read says ENDED (a server that lists a session
// as open yet reports it ended), the fallback prints its answer and exits 0.
func TestWhoamiCheckFallbackNeverExits142350(t *testing.T) {
	teamGitDir(t)
	fk := newStateFake(t, "", map[string]string{
		"AuthContext": whoamiAuthJSON,
		"TeamSessionsPage": `{"data":{"sessionsPage":{"total":1,"hasMore":false,"items":[` +
			whoamiSession("s-a", "wkr1", "u-me", "Jonas", false) + `]}}}`,
	}, func(string) string { return whoamiStateJSON("ENDED", false, "") })
	for _, args := range [][]string{{"team", "session", "whoami", "--check"}, {"team", "session", "whoami", "--check", "--json"}} {
		out, err := runState815(t, fk, args...)
		if err != nil {
			t.Fatalf("%v: the fallback must not fail or exit non-zero (exit %d): %v", args, exitCodeFor(err), err)
		}
		if !strings.Contains(strings.ToLower(out), "ended") {
			t.Errorf("%v: the answer still reports the state: %s", args, out)
		}
	}
}
