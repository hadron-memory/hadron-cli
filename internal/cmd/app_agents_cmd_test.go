package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const appRosterResp = `{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:support","name":"Support App",
	"agents":[{"id":"agt9","urn":"hrn:agent:acme.com:support-bot","name":"Support Bot","description":null,
	"organizationId":"o1","personaName":null,"personaRole":null,"personaPrompt":null,
	"createdAt":"2026-08-11T00:00:00Z"}]}}}`

// #408: the AppAgent join is a general App concept, so it must be reachable
// under the `app` noun — not only via `team roster`. A plain single-agent App
// with no personas is the case that proves it isn't team-specific.
func TestAppAgentListReadsTheAppAgentJoin(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"TeamRoster": appRosterResp})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "list", "hrn:app:acme.com:support", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	if err := json.Unmarshal(captured["TeamRoster"], &vars); err != nil {
		t.Fatalf("captured vars: %v", err)
	}
	if vars["appRef"] != "hrn:app:acme.com:support" {
		t.Errorf("the positional ref must scope the read: %v", vars)
	}
	var members []struct {
		URN         string  `json:"urn"`
		PersonaName *string `json:"personaName"`
	}
	if err := json.Unmarshal([]byte(out.String()), &members); err != nil {
		t.Fatalf("--json: %v (%s)", err, out.String())
	}
	if len(members) != 1 || members[0].URN != "hrn:agent:acme.com:support-bot" {
		t.Fatalf("members: %s", out.String())
	}
	if members[0].PersonaName != nil {
		t.Errorf("a non-persona install must carry a null personaName: %s", out.String())
	}
}

// The App may come from --app / the configured context instead of the argument.
func TestAppAgentListUsesAppContext(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"TeamRoster": appRosterResp})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "list", "--app", "acme.com::support", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["TeamRoster"], &vars)
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
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("exit code = %d, want Usage; err: %v", code, err)
	}
	if err == nil || !strings.Contains(err.Error(), "no App") {
		t.Errorf("message should name the missing App: %v", err)
	}
}
