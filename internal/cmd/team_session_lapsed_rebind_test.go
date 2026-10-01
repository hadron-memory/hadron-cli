package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// #791: an open session can be outside the server's liveness window. Its
// endedAt is still null, but the same worktree may bind again without ending
// that old stint or claiming that another driver is active.
func TestTeamSessionStartRebindsInactiveLocalSessionWithoutForce(t *testing.T) {
	for _, tc := range []struct {
		name, session, liveness string
		wantLapsedNote          bool
	}{
		{"lapsed but open", startedSessionJSON, `{"data":{"session":{"id":"s-new","startedAt":"2026-08-11T10:00:00Z",` +
			`"endedAt":null,"isLive":false}}}`, true},
		{"ended", strings.Replace(endedSessionJSON, `"s-done"`, `"s-new"`, 1), `{"data":{"session":{"id":"s-new","startedAt":"2026-08-11T10:00:00Z",` +
			`"endedAt":"2026-08-11T11:00:00Z","isLive":false}}}`, false},
		{"ended on older server", strings.Replace(endedSessionJSON, `"s-done"`, `"s-new"`, 1), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := teamGitDir(t)
			if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			successor := strings.Replace(startedSessionJSON, `"s-new"`, `"s-successor"`, 1)
			responses := map[string]string{
				"GetTeamSession":   `{"data":{"session":` + tc.session + `}}`,
				"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
				"TeamSessions":     `{"data":{"sessions":[]}}`,
				"StartTeamSession": `{"data":{"startSession":` + successor + `}}`,
			}
			if tc.liveness != "" {
				responses["GetBoundSessionLiveness"] = tc.liveness
			}
			gql, captured := captureGraphQL(t, responses)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("an inactive local binding should permit an ordinary rebind: %v", err)
			}
			if note := strings.Contains(f.IOStreams.ErrOut.(*strings.Builder).String(), "replaced lapsed local binding"); note != tc.wantLapsedNote {
				t.Errorf("lapsed note = %v, want %v", note, tc.wantLapsedNote)
			}
			if _, ended := captured["EndTeamSession"]; ended {
				t.Error("ordinary rebind must not end the old stint")
			}
			if _, started := captured["StartTeamSession"]; !started {
				t.Error("ordinary rebind did not reach the server bind")
			}
			data, err := os.ReadFile(filepath.Join(dir, "hadron-team-session.json"))
			if err != nil || !strings.Contains(string(data), "s-successor") {
				t.Fatalf("the new binding must replace the local one: %s, %v", data, err)
			}
		})
	}
}

func TestTeamSessionStartOlderServerCannotCallOpenBindingLive(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"GetTeamSession": `{"data":{"session":` + startedSessionJSON + `}}`,
		// GetBoundSessionLiveness gets the old-server validation refusal from
		// unstubbedDefault. An open row is not proof of a live session.
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "could not be checked") || strings.Contains(err.Error(), "still live") {
		t.Fatalf("older server must report unknown liveness, not claim live: %v", err)
	}
	if _, started := captured["StartTeamSession"]; started {
		t.Error("the unknown-state guard must preserve the old binding")
	}
	if _, fallback := captured["GetTeamSession"]; !fallback {
		t.Error("older-server validation refusal must fall back to the ordinary session read")
	}
	selectsIsLive := func(operation string) bool {
		for _, line := range strings.Split(operation, "\n") {
			if strings.TrimSpace(line) == "isLive" {
				return true
			}
		}
		return false
	}
	if selectsIsLive(gen.GetTeamSession_Operation) || selectsIsLive(gen.TeamSessions_Operation) ||
		!selectsIsLive(gen.GetBoundSessionLiveness_Operation) {
		t.Error("only the optional bound-session operation may select Session.isLive")
	}
}

func TestTeamSessionStartCannotUseAnotherServersLivenessToReplaceBinding(t *testing.T) {
	dir := teamGitDir(t)
	otherServerBinding := strings.Replace(bindingFixture, `"repo":`, `"server":"https://other.example","repo":`, 1)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(otherServerBinding), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"GetBoundSessionLiveness": `{"data":{"session":{"id":"s-new","startedAt":"2026-08-11T10:00:00Z",` +
			`"endedAt":null,"isLive":false}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "https://other.example") {
		t.Fatalf("the binding's server mismatch must be reported: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("mismatched server must be refused before a GraphQL read: %v", captured)
	}
}
