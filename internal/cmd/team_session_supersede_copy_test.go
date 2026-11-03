package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #894: the server ENDS the same person's other open sessions on a worker at
// bind (hadron-server#1617 C, "superseded by <new id>"), but leaves another
// driver's alone. The CLI used to say the old session "remains open" and "does
// not end it" — false for the caller's own. These pins hold the copy to what
// the CLI can establish: it cannot tell from the response whether an open row
// is the caller's own, and it must not claim an end before the bind succeeds.

// retired spellings that may not come back in any bind narration.
var retiredSupersedeClaims = []string{
	"does not end it",
	"remains open for its driver",
	"still open and this bind",
}

func assertNoRetiredSupersedeClaims(t *testing.T, note string) {
	t.Helper()
	for _, forbidden := range retiredSupersedeClaims {
		if strings.Contains(note, forbidden) {
			t.Errorf("stderr carries the retired claim %q: %s", forbidden, note)
		}
	}
}

// Pre-bind, other-driver / unidentified row (activeSessionJSON's driver is
// u-holger and the CLI reads no caller identity here): neutral wording that
// names both outcomes, and asserts neither as already done.
func TestTeamSessionStartPreBindNoteIsNeutralAboutWhoseSessionEnds(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + withLiveness(irisWorkerJSON, "false") + `}}`,
		"TeamSessionsPage": `{"data":{"sessionsPage":{"total":1,"hasMore":false,"items":[` + activeSessionJSON + `]}}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	stderr := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	note := stderr()
	assertNoRetiredSupersedeClaims(t, note)
	for _, want := range []string{"s-old", "your own", "supersedes", "another driver"} {
		if !strings.Contains(note, want) {
			t.Errorf("pre-bind note must name both outcomes (%q): %s", want, note)
		}
	}
	for _, forbidden := range []string{"was ended", "has ended", "ended it", "now ended"} {
		if strings.Contains(note, forbidden) {
			t.Errorf("pre-bind note may not claim an end before the bind succeeds (%q): %s", forbidden, note)
		}
	}
}

// Failed bind: the server refuses, so nothing was superseded. No stderr line
// may say the earlier session ended or was replaced.
func TestTeamSessionStartFailedBindClaimsNothingEnded(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + withLiveness(irisWorkerJSON, "false") + `}}`,
		"TeamSessionsPage": `{"data":{"sessionsPage":{"total":1,"hasMore":false,"items":[` + activeSessionJSON + `]}}}`,
		"StartTeamSession": `{"errors":[{"message":"That worker is taken.",
			"extensions":{"code":"WORKER_TAKEN","workerId":"wkr1","sessionId":"s-old"}}]}`,
	})
	f, _ := testFactory(t)
	stderr := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("the refused bind must fail")
	}
	note := stderr()
	assertNoRetiredSupersedeClaims(t, note)
	for _, forbidden := range []string{"was ended", "has ended", "ended it", "now ended", "replaced"} {
		if strings.Contains(note, forbidden) {
			t.Errorf("a failed bind superseded nothing (%q): %s", forbidden, note)
		}
	}
}

// Successful bind over the caller's own lapsed/dormant local binding: the
// post-bind note says what the server does to an own session (ends it as
// superseded, no handoff) without asserting that this row was one, and never
// uses the retired "remains open" claim.
func TestTeamSessionStartLapsedBindingNoteReportsSupersede(t *testing.T) {
	for _, tc := range []struct{ name, startedAt string }{
		{"lapsed", "2026-08-11T10:00:00Z"},
		{"dormant", "2026-05-01T10:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := teamGitDir(t)
			if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			successor := strings.Replace(startedSessionJSON, `"s-new"`, `"s-successor"`, 1)
			gql, _ := captureGraphQL(t, map[string]string{
				"GetTeamSession": `{"data":{"session":` + startedSessionJSON + `}}`,
				"GetBoundSessionLiveness": `{"data":{"session":{"id":"s-new","startedAt":"` + tc.startedAt + `",` +
					`"endedAt":null,"isLive":false}}}`,
				"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
				"TeamSessionsPage": `{"data":{"sessionsPage":{"total":0,"hasMore":false,"items":[]}}}`,
				"StartTeamSession": `{"data":{"startSession":` + successor + `}}`,
			})
			f, _ := testFactory(t)
			stderr := captureErrOut(f)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			note := stderr()
			assertNoRetiredSupersedeClaims(t, note)
			for _, want := range []string{"replaced lapsed local binding", "s-new", "superseded", "no handoff"} {
				if !strings.Contains(note, want) {
					t.Errorf("post-bind note must report the supersede (%q): %s", want, note)
				}
			}
		})
	}
}
