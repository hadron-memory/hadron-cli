package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const appRosterResp = `{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:support","name":"Support App",
	"agents":[{"id":"agt9","urn":"hrn:agent:acme.com:support-bot","name":"Support Bot","description":null,
	"visibility":"ORGANIZATION","organizationId":"o1","personaRole":null,
	"createdAt":"2026-08-11T00:00:00Z"}]}}}`

// #408: the AppAgent join is a general App concept, reachable under the `app`
// noun. Since #428 it is the INSTALL roster (the cast pool) — named workers
// are `team worker list`. A plain single-agent App with no persona dressing
// is the case that proves it isn't team-specific.
func TestAppAgentListReadsTheAppAgentJoin(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"AppAgentRoster": appRosterResp})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "list", "hrn:app:acme.com:support", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	if err := json.Unmarshal(captured["AppAgentRoster"], &vars); err != nil {
		t.Fatalf("captured vars: %v", err)
	}
	if vars["appRef"] != "hrn:app:acme.com:support" {
		t.Errorf("the positional ref must scope the read: %v", vars)
	}
	var members []struct {
		URN         string  `json:"urn"`
		PersonaRole *string `json:"personaRole"`
	}
	if err := json.Unmarshal([]byte(out.String()), &members); err != nil {
		t.Fatalf("--json: %v (%s)", err, out.String())
	}
	if len(members) != 1 || members[0].URN != "hrn:agent:acme.com:support-bot" {
		t.Fatalf("members: %s", out.String())
	}
	if members[0].PersonaRole != nil {
		t.Errorf("an undressed install must carry a null personaRole: %s", out.String())
	}
}

// The App may come from --app / the configured context instead of the argument.
func TestAppAgentListUsesAppContext(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"AppAgentRoster": appRosterResp})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "list", "--app", "acme.com::support", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["AppAgentRoster"], &vars)
	if vars["appRef"] != "acme.com::support" {
		t.Errorf("--app should scope this one (it IS the subject here): %v", vars)
	}
}

// With neither an argument nor an App context, say so rather than guessing.
func TestAppAgentListNeedsAnApp(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "list", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Errorf("exit code = %d, want Usage; err: %v", code, err)
	}
	if err == nil || !strings.Contains(err.Error(), "no App") {
		t.Errorf("message should name the missing App: %v", err)
	}
}
