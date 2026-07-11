package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const grantJSON = `{"id":"pg1","principalType":"USER","principalId":"u2","principalHandle":"jane",
	"organizationId":"org1","organizationUrn":"acme.com","actions":["memory.clone"],
	"expiresAt":null,"createdAt":"2026-07-10T00:00:00Z"}`

func TestGrantCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreatePrincipalGrant": `{"data":{"createPrincipalGrant":` + grantJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"grant", "create", "--org", "acme.com", "--user", "jane",
		"--action", "memory.clone, memory.create", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreatePrincipalGrant"], &vars)
	if vars["orgRef"] != "acme.com" || vars["userRef"] != "jane" {
		t.Errorf("core fields wrong: %v", vars)
	}
	actions, _ := vars["actions"].([]any)
	if len(actions) != 2 || actions[0] != "memory.clone" || actions[1] != "memory.create" {
		t.Errorf("comma-split + trim should yield two actions, got %v", vars["actions"])
	}
	// Unset --expires must be omitted (omitempty), not sent as null.
	if v, present := vars["expiresAt"]; present {
		t.Errorf("unset --expires must be omitted, got %v", v)
	}
	var dto struct {
		ID      string   `json:"id"`
		Actions []string `json:"actions"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json not valid: %v\n%s", err, out.String())
	}
	if dto.ID != "pg1" || len(dto.Actions) != 1 {
		t.Errorf("dto should carry the server's grant, got %+v", dto)
	}
}

func TestGrantCreateRequiresAction(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// --action given but empty after normalization → usage error, no request.
	root.SetArgs([]string{"grant", "create", "--org", "acme.com", "--user", "jane",
		"--action", " , ", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "at least one --action") {
		t.Fatalf("expected at-least-one-action usage error, got %v", err)
	}
}

func TestGrantLsDefaultsToOwnGrants(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"PrincipalGrants": `{"data":{"principalGrants":{"total":1,"items":[` + grantJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"grant", "ls", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["PrincipalGrants"], &vars)
	// No --org/--user: both must be omitted so the server applies its
	// self-audit default scope.
	if v, present := vars["orgRef"]; present {
		t.Errorf("unset --org must be omitted, got %v", v)
	}
	if v, present := vars["userRef"]; present {
		t.Errorf("unset --user must be omitted, got %v", v)
	}
	var dtos []struct {
		ID              string  `json:"id"`
		PrincipalHandle *string `json:"principalHandle"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dtos); err != nil {
		t.Fatalf("--json not valid: %v\n%s", err, out.String())
	}
	if len(dtos) != 1 || dtos[0].ID != "pg1" || dtos[0].PrincipalHandle == nil {
		t.Errorf("unexpected dtos: %+v", dtos)
	}
}

func TestGrantLsOrgScoped(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"PrincipalGrants": `{"data":{"principalGrants":{"total":1,"items":[` + grantJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"grant", "ls", "--org", "acme.com", "--user", "jane", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["PrincipalGrants"], &vars)
	if vars["orgRef"] != "acme.com" || vars["userRef"] != "jane" {
		t.Errorf("org/user refs should map, got %v", vars)
	}
	// Human table renders the handle and the action list.
	if !strings.Contains(out.String(), "jane") || !strings.Contains(out.String(), "memory.clone") {
		t.Errorf("table should show grantee + actions:\n%s", out.String())
	}
}

func TestGrantRevoke(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RevokePrincipalGrant": `{"data":{"revokePrincipalGrant":true}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"grant", "revoke", "pg1", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RevokePrincipalGrant"], &vars)
	if vars["ref"] != "pg1" {
		t.Errorf("ref should map, got %v", vars)
	}
	var dto struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json not valid: %v\n%s", err, out.String())
	}
	if dto.Status != "revoked" {
		t.Errorf("expected revoked status, got %+v", dto)
	}
}

func TestGrantRevokeRequiresConfirmation(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// Non-interactive without --yes must refuse before any request.
	root.SetArgs([]string{"grant", "revoke", "pg1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected confirmation-required error")
	}
}
