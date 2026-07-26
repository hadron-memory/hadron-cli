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
		// A raw id resolves via the user(ref:) fast path before the share write.
		"GetUser":           `{"data":{"user":{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice","githubUsername":null,"roles":[]}}}`,
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

// #280: --grantee accepts a user ref (here a @handle), resolved to the PK the
// createMemoryShare(granteeId: ID!) mutation requires.
func TestMemoryShareCreateResolvesGranteeRef(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetUser":           `{"data":{"user":{"id":"usr_jane","name":"Jane","email":"jane@acme.com","handle":"jane","githubUsername":null,"roles":[]}}}`,
		"CreateMemoryShare": `{"data":{"createMemoryShare":{"memoryShare":{"role":"writer","grantee":` + memUserJSON + `}}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "share", "create", "mem1", "--grantee", "@jane", "--role", "writer", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The @handle sigil is stripped and resolved via user(ref: hrn:user:jane).
	var getUserVars map[string]any
	_ = json.Unmarshal(captured["GetUser"], &getUserVars)
	if getUserVars["ref"] != "hrn:user:jane" {
		t.Errorf("a @handle must resolve via hrn:user:<handle>, got %v", getUserVars["ref"])
	}
	// The mutation receives the resolved PK, not the raw ref.
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateMemoryShare"], &vars)
	if vars["granteeId"] != "usr_jane" {
		t.Errorf("createMemoryShare must receive the resolved user id, got %v", vars["granteeId"])
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

func TestMemoryShareRmWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetUser":           `{"data":{"user":{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice","githubUsername":null,"roles":[]}}}`,
		"DeleteMemoryShare": `{"data":{"deleteMemoryShare":{"memoryId":"mem1","granteeId":"usr1"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "share", "rm", "mem1", "--grantee", "usr1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteMemoryShare"], &vars)
	if vars["memoryRef"] != "mem1" || vars["granteeId"] != "usr1" {
		t.Errorf("rm vars: %v", vars)
	}
}

// `revoke` stays as an alias so existing scripts keep working after the
// hadron-server#785 rename — including the JSON status they branch on.
func TestMemoryShareRevokeAlias(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetUser":           `{"data":{"user":{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice","githubUsername":null,"roles":[]}}}`,
		"DeleteMemoryShare": `{"data":{"deleteMemoryShare":{"memoryId":"mem1","granteeId":"usr1"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "share", "revoke", "mem1", "--grantee", "usr1", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["DeleteMemoryShare"]; !ok {
		t.Error("the revoke alias must call deleteMemoryShare")
	}
	var dto struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	// The published status for `revoke` predates the rename; scripts branch on it.
	if dto.Status != "revoked" {
		t.Errorf("the revoke alias must keep status=revoked, got %q", dto.Status)
	}
}

// The canonical verb reports the value its sibling rm commands use.
func TestMemoryShareRmStatusRemoved(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetUser":           `{"data":{"user":{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice","githubUsername":null,"roles":[]}}}`,
		"DeleteMemoryShare": `{"data":{"deleteMemoryShare":{"memoryId":"mem1","granteeId":"usr1"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "share", "rm", "mem1", "--grantee", "usr1", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), `"status": "removed"`) {
		t.Errorf("share rm should report status=removed, got %s", out.String())
	}
}

// hadron-server#785: no --grantee means "my own share" — the grantee variable
// must be OMITTED, not sent as null (the server keys the delete on the caller
// only when the key is absent). No GetUser round-trip either: nothing to resolve.
func TestMemoryShareRmSelfOmitsGrantee(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteMemoryShare": `{"data":{"deleteMemoryShare":{"memoryId":"mem1","granteeId":"usr_me"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "share", "rm", "mem1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteMemoryShare"], &vars)
	if vars["memoryRef"] != "mem1" {
		t.Errorf("rm vars: %v", vars)
	}
	// Key ABSENT, not present-and-null: a null would be a different request
	// than the one the server's self-removal contract describes.
	if _, present := vars["granteeId"]; present {
		t.Errorf("self-removal must omit granteeId entirely, got %v", vars["granteeId"])
	}
	if _, ok := captured["GetUser"]; ok {
		t.Error("self-removal must not resolve a user ref")
	}
	if !strings.Contains(out.String(), "your share") {
		t.Errorf("output should name the self path, got %q", out.String())
	}
}

// The memory is passed through as a REF (canonicalized, no memory(ref:) lookup):
// that lookup can't see a soft-deleted memory, and leaving one is supported.
func TestMemoryShareRmPassesMemoryRefWithoutLookup(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteMemoryShare": `{"data":{"deleteMemoryShare":{"memoryId":"mem1","granteeId":"usr_me"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "share", "rm", "acme.com::kb", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["GetMemory"]; ok {
		t.Error("share rm must not pre-resolve the memory — a soft-deleted one wouldn't resolve")
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteMemoryShare"], &vars)
	if vars["memoryRef"] != "hrn:mem:acme.com:kb" {
		t.Errorf("the URN should reach the server canonicalized, got %v", vars["memoryRef"])
	}
}

const memOrgJSON = `{"id":"org1","name":"PartnerCo","urn":"partnerco.com"}`

func TestMemorySubscriptionCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemorySubscription": `{"data":{"createMemorySubscription":{"role":"READER","activated":true,"organization":` + memOrgJSON + `}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// Mixed-case role must normalize to the UPPER-case general Role enum.
	root.SetArgs([]string{"memory", "subscription", "create", "mem1", "--org", "org1", "--role", "Reader", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateMemorySubscription"], &vars)
	if vars["memoryId"] != "mem1" || vars["orgId"] != "org1" || vars["role"] != "READER" {
		t.Errorf("subscription vars: %v", vars)
	}
}

func TestMemorySubscriptionRejectsBadRole(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "subscription", "create", "mem1", "--org", "org1", "--role", "manager", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --role") {
		t.Fatalf("expected invalid-role error, got %v", err)
	}
}

func TestMemorySubscriptionLs(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MemorySubscriptions": `{"data":{"memory":{"id":"mem1","subscriptions":[
			{"role":"CONTRIBUTOR","activated":true,"organization":` + memOrgJSON + `}
		]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "subscription", "ls", "mem1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var subs []struct {
		Role         string `json:"role"`
		Activated    bool   `json:"activated"`
		Organization struct {
			ID  string `json:"id"`
			URN string `json:"urn"`
		} `json:"organization"`
	}
	if err := json.Unmarshal([]byte(out.String()), &subs); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(subs) != 1 || subs[0].Role != "CONTRIBUTOR" || !subs[0].Activated || subs[0].Organization.URN != "partnerco.com" {
		t.Errorf("subscriptions: %+v", subs)
	}
}

func TestMemorySubscriptionSetRole(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMemorySubscription": `{"data":{"updateMemorySubscription":{"role":"ADMIN","activated":true,"organization":` + memOrgJSON + `}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "subscription", "set-role", "mem1", "--org", "org1", "--role", "admin", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateMemorySubscription"], &vars)
	if vars["role"] != "ADMIN" || vars["orgId"] != "org1" {
		t.Errorf("set-role vars: %v", vars)
	}
}

func TestMemorySubscriptionRmRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "subscription", "rm", "mem1", "--org", "org1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestMemorySubscriptionRmWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteMemorySubscription": `{"data":{"deleteMemorySubscription":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "subscription", "rm", "mem1", "--org", "org1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteMemorySubscription"], &vars)
	if vars["memoryId"] != "mem1" || vars["orgId"] != "org1" {
		t.Errorf("rm vars: %v", vars)
	}
}
