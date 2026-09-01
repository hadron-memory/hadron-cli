package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const installResp = `{"data":{"installAgentIntoApp":{"appAgent":{
	"createdAt":"2026-08-14T00:00:00Z",
	"agent":{"id":"agt1","urn":"hrn:agent:acme.com:iris","name":"Iris",
	         "personaRole":"backend-engineer"},
	"app":{"id":"app1","urn":"hrn:app:acme.com:eng-team","name":"Eng Team"}}}}}`

// #389: `app install` creates a NEW App from an Agent; nothing joined an Agent
// to an App you already have, which is what makes cor:dmo:050:03's re-attach
// promise reachable.
func TestAppAgentAddInstallsIntoAnExistingApp(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"InstallAgentIntoApp": installResp})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "add", "hrn:app:acme.com:eng-team",
		"hrn:agent:acme.com:iris", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	if err := json.Unmarshal(captured["InstallAgentIntoApp"], &vars); err != nil {
		t.Fatalf("captured vars: %v", err)
	}
	if vars["appRef"] != "hrn:app:acme.com:eng-team" || vars["agentRef"] != "hrn:agent:acme.com:iris" {
		t.Errorf("both refs must pass through verbatim (id or URN): %v", vars)
	}
	// trainingMode is PER-APP: sending it unasked would re-set the flag for
	// every Agent installed in the App.
	if _, present := vars["trainingMode"]; present {
		t.Errorf("unset --training-mode must be OMITTED, got %v", vars["trainingMode"])
	}
	var dto struct {
		AgentURN    string  `json:"agentUrn"`
		AppURN      string  `json:"appUrn"`
		PersonaRole *string `json:"personaRole"`
		Status      string  `json:"status"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json: %v (%s)", err, out.String())
	}
	if dto.AgentURN != "hrn:agent:acme.com:iris" || dto.AppURN != "hrn:app:acme.com:eng-team" || dto.Status != "installed" {
		t.Errorf("dto: %s", out.String())
	}
	if dto.PersonaRole == nil || *dto.PersonaRole != "backend-engineer" {
		t.Errorf("a dressed install should carry its persona role: %s", out.String())
	}
}

func TestAppAgentAddSendsTrainingModeOnlyWhenPassed(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"InstallAgentIntoApp": installResp})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "add", "app1", "agt1", "--training-mode", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["InstallAgentIntoApp"], &vars)
	if vars["trainingMode"] != true {
		t.Errorf("explicit --training-mode must reach the wire: %v", vars)
	}
}

// Detaching changes who is on a roster, so it prompts like the group's other
// removals — and refuses non-interactively without --yes, before any write.
func TestAppAgentRemoveRequiresYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UninstallAgentFromApp": `{"data":{"uninstallAgentFromApp":{"agentId":"agt1","appId":"app1"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "remove", "app1", "agt1", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Error("removal without --yes must be refused non-interactively")
	}
	if _, called := captured["UninstallAgentFromApp"]; called {
		t.Error("nothing may be written before confirmation")
	}
}

func TestAppAgentRemove(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UninstallAgentFromApp": `{"data":{"uninstallAgentFromApp":{"agentId":"agt1","appId":"app1"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "remove", "hrn:app:acme.com:eng-team",
		"hrn:agent:acme.com:iris", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UninstallAgentFromApp"], &vars)
	if vars["appRef"] != "hrn:app:acme.com:eng-team" || vars["agentRef"] != "hrn:agent:acme.com:iris" {
		t.Errorf("remove vars: %v", vars)
	}
	// The retention promise is the reason this is reversible — say it.
	if !strings.Contains(out.String(), "memories persist") {
		t.Errorf("output should state the memories survive: %s", out.String())
	}
}

// The --json shape is a stable agent-facing contract, so it needs its own
// assertions — the human path passing says nothing about it (PR #427 review).
func TestAppAgentRemoveJSON(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"UninstallAgentFromApp": `{"data":{"uninstallAgentFromApp":{"agentId":"agt1","appId":"app1"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "remove", "app1", "agt1", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		AppID   string `json:"appId"`
		AgentID string `json:"agentId"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("remove --json: %v (%s)", err, out.String())
	}
	if dto.AppID != "app1" || dto.AgentID != "agt1" || dto.Status != "uninstalled" {
		t.Errorf("all three fields must be carried: %+v (%s)", dto, out.String())
	}
}

// The issue asks for this specifically: a team member who is not an org member
// hits FORBIDDEN, and a bare "Forbidden" does not explain a gate that is
// deliberately stricter than App membership.
func TestAppAgentAddExplainsTheAuthorizationGate(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"InstallAgentIntoApp": `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "add", "app1", "agt1", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "CONTRIBUTOR+") || !strings.Contains(err.Error(), "App membership") {
		t.Errorf("FORBIDDEN should explain the gate, got: %v", err)
	}
}

// A duplicate install is a state conflict, and the server's typed code must
// drive the documented exit code rather than the generic 1.
func TestAppAgentAddDuplicateIsConflict(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"InstallAgentIntoApp": `{"errors":[{"message":"already installed","extensions":{"code":"DUPLICATE_APP_AGENT"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "add", "app1", "agt1", "--server", gql.URL})
	if code := exitCodeFor(root.Execute()); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want Conflict", code)
	}
}
