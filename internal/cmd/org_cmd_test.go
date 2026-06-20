package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const orgJSON = `{"id":"org1","urn":"acme.com","name":"Acme","isVisible":true,
	"createdAt":"2026-06-19T00:00:00Z","updatedAt":"2026-06-19T00:00:00Z"}`
const orgUserJSON = `{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice",
	"githubUsername":null,"roles":["CONTRIBUTOR"]}`

func TestOrgCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateOrganization": `{"data":{"createOrganization":` + orgJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "create", "--name", "Acme", "--urn", "acme.com", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateOrganization"], &vars)
	if vars["name"] != "Acme" || vars["urn"] != "acme.com" {
		t.Errorf("create vars: %v", vars)
	}
	var dto map[string]any
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto["id"] != "org1" || dto["urn"] != "acme.com" {
		t.Errorf("dto: %v", dto)
	}
}

func TestOrgGetNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetOrganization": `{"data":{"organization":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "get", "org_x", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found, got %v", err)
	}
}

func TestOrgUpdatePreservesUnset(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateOrganization": `{"data":{"updateOrganization":` + orgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "update", "org1", "--name", "Acme Inc", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateOrganization"], &vars)
	if vars["id"] != "org1" || vars["name"] != "Acme Inc" {
		t.Errorf("update vars: %v", vars)
	}
	for _, k := range []string{"urn", "isVisible"} {
		if _, present := vars[k]; present {
			t.Errorf("unset %q must be omitted, got %v", k, vars[k])
		}
	}
}

func TestOrgUpdateNothingIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "update", "org1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("expected nothing-to-update, got %v", err)
	}
}

func TestOrgRmRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "rm", "org1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestOrgMemberLs(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"OrgMembers": `{"data":{"organization":{"id":"org1","members":[
			{"id":"m1","role":"OWNER","canInvite":true,"user":` + orgUserJSON + `}
		]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "ls", "org1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var members []struct {
		Role string `json:"role"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(out.String()), &members); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(members) != 1 || members[0].Role != "OWNER" || members[0].User.ID != "usr1" {
		t.Errorf("members: %+v", members)
	}
}

func TestOrgMemberAdd(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"AddOrgMember": `{"data":{"addOrgMember":{"id":"m1","role":"CONTRIBUTOR","user":` + orgUserJSON + `}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// lower-case role must be accepted and normalized to the enum.
	root.SetArgs([]string{"org", "member", "add", "org1", "--user", "usr1", "--role", "contributor", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["AddOrgMember"], &vars)
	if vars["orgId"] != "org1" || vars["userId"] != "usr1" || vars["role"] != "CONTRIBUTOR" {
		t.Errorf("add vars: %v", vars)
	}
}

func TestOrgMemberAddRejectsBadRole(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "add", "org1", "--user", "u", "--role", "SUPERADMIN", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --role") {
		t.Fatalf("expected invalid-role error, got %v", err)
	}
}

func TestOrgMemberSetRole(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateOrgMember": `{"data":{"updateOrgMember":{"id":"m1","role":"ADMIN","user":` + orgUserJSON + `}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "set-role", "org1", "--user", "usr1", "--role", "admin", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateOrgMember"], &vars)
	if vars["role"] != "ADMIN" || vars["userId"] != "usr1" {
		t.Errorf("set-role vars: %v", vars)
	}
}

func TestOrgMemberRmWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RemoveOrgMember": `{"data":{"removeOrgMember":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "rm", "org1", "--user", "usr1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RemoveOrgMember"], &vars)
	if vars["orgId"] != "org1" || vars["userId"] != "usr1" {
		t.Errorf("rm vars: %v", vars)
	}
}

func TestOrgMemberRmRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "rm", "org1", "--user", "usr1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}
