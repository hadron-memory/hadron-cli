package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1226 (R211, cor:dmo:050:04): `agent create|update --member-roles`, the
// Agent's roles on `agent get`, and `app member roles` reading
// App.availableMemberRoles. The guards: an omitted flag sends NO memberRoles
// key (the server reads omitted as "default"/"preserve"), an empty value is a
// local usage error that sends nothing, and role validity stays the server's.

const agentWithRolesJSON = `{"id":"agt1","urn":"acme.com::support-bot","name":"Support Bot","description":null,
	"type":"CHATBOT","visibility":"ORGANIZATION","organizationId":"acme.com","surfaces":[],
	"systemMemoryId":null,"aiProvider":null,"aiModel":null,"hasAiApiKey":false,
	"usesTasks":true,"createdAt":"2026-06-19T00:00:00Z",
	"installationPolicy":{"memberRoles":["owner","cook"]}}`

func TestAgentCreateMemberRolesSendsExactList(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentWithRolesJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot",
		"--member-roles", "owner,cook", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAgent"], &vars)
	got, _ := json.Marshal(vars["memberRoles"])
	if string(got) != `["owner","cook"]` {
		t.Errorf("memberRoles on the wire = %s, want [\"owner\",\"cook\"]", got)
	}
}

func TestAgentCreateWithoutMemberRolesSendsNoKey(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentWithRolesJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAgent"], &vars)
	if _, present := vars["memberRoles"]; present {
		t.Errorf("omitted --member-roles must send no memberRoles key, got %v", vars["memberRoles"])
	}
}

func TestAgentUpdateMemberRolesSendsExactList(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAgent": `{"data":{"updateAgent":` + agentWithRolesJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1", "--member-roles", "owner,cook", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a lone --member-roles is a change, not 'nothing to update': %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateAgent"], &vars)
	got, _ := json.Marshal(vars["memberRoles"])
	if string(got) != `["owner","cook"]` {
		t.Errorf("memberRoles on the wire = %s", got)
	}
}

func TestAgentUpdateWithoutMemberRolesSendsNoKey(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAgent": `{"data":{"updateAgent":` + agentWithRolesJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1", "--name", "Bot v2", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateAgent"], &vars)
	if _, present := vars["memberRoles"]; present {
		t.Errorf("omitted --member-roles must be preserved (no key), got %v", vars["memberRoles"])
	}
}

func TestAgentMemberRolesEmptyIsUsageErrorAndSendsNothing(t *testing.T) {
	for _, args := range [][]string{
		{"agent", "create", "--org", "acme.com", "--name", "Support Bot", "--member-roles", ""},
		{"agent", "create", "--org", "acme.com", "--name", "Support Bot", "--member-roles", " "},
		{"agent", "update", "agt1", "--member-roles", ""},
		{"agent", "update", "agt1", "--name", "x", "--member-roles", ""},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(args, "--server", gql.URL))
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want a usage error, got %v", err)
			}
			if len(captured) != 0 {
				t.Errorf("an empty --member-roles must send nothing, sent %v", captured)
			}
		})
	}
}

func TestAgentMemberRolesServerRefusalIsVerbatim(t *testing.T) {
	const msg = "member role \"Cook!\" is malformed: use lowercase letters, digits and hyphens"
	gql, _ := captureGraphQL(t, map[string]string{
		"UpdateAgent": memberGQLError(msg, "BAD_USER_INPUT"),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1", "--member-roles", "Cook!", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), msg) {
		t.Fatalf("server BAD_USER_INPUT must surface unchanged, got %v", err)
	}
	if exitCodeFor(err) != exitcode.Usage {
		t.Errorf("exit code = %d, want usage", exitCodeFor(err))
	}
}

func TestAgentGetShowsMemberRoles(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetAgent": `{"data":{"agent":` + agentWithRolesJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "get", "agt1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("json: %v", err)
	}
	got, _ := json.Marshal(dto["memberRoles"])
	if string(got) != `["owner","cook"]` {
		t.Errorf("--json memberRoles = %s", got)
	}

	gql2, _ := captureGraphQL(t, map[string]string{
		"GetAgent": `{"data":{"agent":` + agentWithRolesJSON + `}}`,
	})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"agent", "get", "agt1", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out2.String(), "  member roles: owner, cook\n") {
		t.Errorf("text output missing the member roles line:\n%s", out2.String())
	}
}

func TestAgentGetMemberRolesAbsentIsEmptyArray(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetAgent": `{"data":{"agent":` + agentJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "get", "agt1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), `"memberRoles": []`) && !strings.Contains(out.String(), `"memberRoles":[]`) {
		t.Errorf("memberRoles must render as [], not null/absent:\n%s", out.String())
	}
}

const appRolesResp = `{"data":{"app":{"id":"capp100000000000000000000","urn":"hrn:app:acme.com:eng-team",
	"availableMemberRoles":["owner","member","cook"]}}}`

func TestAppMemberRolesTextAndJSON(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"AppMemberRoles": appRolesResp})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "member", "roles", "--app", memberApp, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out.String() != "owner\nmember\ncook\n" {
		t.Errorf("text = %q, want one role per line in server order", out.String())
	}

	gql2, _ := captureGraphQL(t, map[string]string{"AppMemberRoles": appRolesResp})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"app", "member", "roles", "--app", memberApp, "--json", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var roles []string
	if err := json.Unmarshal([]byte(out2.String()), &roles); err != nil || strings.Join(roles, ",") != "owner,member,cook" {
		t.Errorf("--json must be a bare array in server order, got %q (%v)", out2.String(), err)
	}
}

func TestAppMemberRolesEmptyList(t *testing.T) {
	const empty = `{"data":{"app":{"id":"capp100000000000000000000","urn":"hrn:app:acme.com:eng-team","availableMemberRoles":[]}}}`
	gql, _ := captureGraphQL(t, map[string]string{"AppMemberRoles": empty})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "member", "roles", "--app", memberApp, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("empty list must be [], got %q", out.String())
	}

	gql2, _ := captureGraphQL(t, map[string]string{"AppMemberRoles": empty})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"app", "member", "roles", "--app", memberApp, "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.TrimSpace(out2.String()) == "" {
		t.Errorf("an empty text list should say so rather than print nothing")
	}
}

func TestAppMemberRolesAppNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"AppMemberRoles": `{"data":{"app":null}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "member", "roles", "--app", memberApp, "--server", gql.URL})
	if err := root.Execute(); exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("want not-found, got %v", err)
	}
}
