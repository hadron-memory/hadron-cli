package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const memUserJSON = `{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice"}`

func TestMemoryMemberAdd(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"AddMemoryMember": `{"data":{"addMemoryMember":{"memoryMember":{"role":"writer","user":` + memUserJSON + `}}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// mixed-case role must normalize to the lower-case enum.
	root.SetArgs([]string{"memory", "member", "add", "mem1", "--user", "usr1", "--role", "Writer", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["AddMemoryMember"], &vars)
	if vars["memoryId"] != "mem1" || vars["userId"] != "usr1" || vars["role"] != "writer" {
		t.Errorf("add vars: %v", vars)
	}
}

func TestMemoryMemberAddRejectsBadRole(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "member", "add", "mem1", "--user", "u", "--role", "manager", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --role") {
		t.Fatalf("expected invalid-role error, got %v", err)
	}
}

func TestMemoryMemberLs(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MemoryMembers": `{"data":{"memory":{"id":"mem1","members":[
			{"role":"owner","createdAt":"2026-06-19T00:00:00Z","user":` + memUserJSON + `}
		]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "member", "ls", "mem1", "--json", "--server", gql.URL})
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
	if len(members) != 1 || members[0].Role != "owner" || members[0].User.ID != "usr1" {
		t.Errorf("members: %+v", members)
	}
}

func TestMemoryMemberSetRole(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMemoryMemberRole": `{"data":{"updateMemoryMemberRole":{"memoryMember":{"role":"reader","user":` + memUserJSON + `}}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "member", "set-role", "mem1", "--user", "usr1", "--role", "reader", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateMemoryMemberRole"], &vars)
	if vars["role"] != "reader" || vars["userId"] != "usr1" {
		t.Errorf("set-role vars: %v", vars)
	}
}

func TestMemoryMemberRmRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "member", "rm", "mem1", "--user", "usr1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestMemoryMemberRmWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RemoveMemoryMember": `{"data":{"removeMemoryMember":{"memoryId":"mem1","userId":"usr1"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "member", "rm", "mem1", "--user", "usr1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RemoveMemoryMember"], &vars)
	if vars["memoryId"] != "mem1" || vars["userId"] != "usr1" {
		t.Errorf("rm vars: %v", vars)
	}
}

func TestMemoryShareCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemoryShare": `{"data":{"createMemoryShare":{"memoryShare":{"role":"reader","grantee":` + memUserJSON + `}}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "share", "create", "mem1", "--grantee", "usr1", "--role", "reader", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateMemoryShare"], &vars)
	if vars["memoryId"] != "mem1" || vars["granteeId"] != "usr1" || vars["role"] != "reader" {
		t.Errorf("share vars: %v", vars)
	}
}

// owner is a valid member role but NOT a share role — share must reject it.
func TestMemoryShareRejectsOwnerRole(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "share", "create", "mem1", "--grantee", "u", "--role", "owner", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --role") {
		t.Fatalf("expected invalid-role error (owner isn't a share role), got %v", err)
	}
}

func TestMemoryShareLs(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MemoryShares": `{"data":{"memory":{"id":"mem1","shares":[
			{"role":"reader","createdAt":"2026-06-19T00:00:00Z","grantee":` + memUserJSON + `}
		]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "share", "ls", "mem1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var shares []struct {
		Role    string `json:"role"`
		Grantee struct {
			ID string `json:"id"`
		} `json:"grantee"`
	}
	if err := json.Unmarshal([]byte(out.String()), &shares); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(shares) != 1 || shares[0].Role != "reader" || shares[0].Grantee.ID != "usr1" {
		t.Errorf("shares: %+v", shares)
	}
}

func TestMemoryShareRevokeWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RevokeMemoryShare": `{"data":{"revokeMemoryShare":{"memoryId":"mem1","granteeId":"usr1"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "share", "revoke", "mem1", "--grantee", "usr1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RevokeMemoryShare"], &vars)
	if vars["memoryId"] != "mem1" || vars["granteeId"] != "usr1" {
		t.Errorf("revoke vars: %v", vars)
	}
}
