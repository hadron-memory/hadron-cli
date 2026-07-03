package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// searchUserJSON builds a SearchUsers user row with the full UserFields shape.
func searchUserJSON(id, name, email, handle string) string {
	return `{"id":"` + id + `","name":"` + name + `","email":"` + email +
		`","handle":"` + handle + `","githubUsername":null,"roles":[]}`
}

func TestAccessCheckJSON(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SearchUsers": `{"data":{"users":{"total":1,"items":[` +
			searchUserJSON("u1", "Alice", "alice@acme.com", "alice") + `]}}}`,
		"EffectiveAccess": `{"data":{"effectiveAccess":{
			"user":{"id":"u1","name":"Alice","email":"alice@acme.com","handle":"alice"},
			"resourceUrn":"hrn:memory:acme.com::kb","resourceKind":"memory",
			"canRead":true,"canWrite":true,"canManage":false,"canDelete":false,
			"role":"writer",
			"grants":[{"source":"MEMORY_SHARE","role":"writer","via":null},
			          {"source":"ORG_ROLE","role":"READER","via":"hrn:org:acme.com"}]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"access", "check", "alice@acme.com", "hrn:memory:acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// The email resolved to the user id before calling effectiveAccess.
	var vars struct {
		User     string `json:"user"`
		Resource string `json:"resource"`
	}
	if err := json.Unmarshal(captured["EffectiveAccess"], &vars); err != nil {
		t.Fatalf("decode vars: %v", err)
	}
	if vars.User != "u1" || vars.Resource != "hrn:memory:acme.com::kb" {
		t.Errorf("unexpected effectiveAccess vars: %+v", vars)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if got["canWrite"] != true || got["role"] != "writer" {
		t.Errorf("unexpected access: %s", out.String())
	}
	grants, _ := got["grants"].([]any)
	if len(grants) != 2 {
		t.Errorf("expected 2 grants, got %s", out.String())
	}
}

func TestAccessCheckNoAccessTable(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"SearchUsers": `{"data":{"users":{"total":1,"items":[` +
			searchUserJSON("u2", "Bob", "bob@acme.com", "bob") + `]}}}`,
		"EffectiveAccess": `{"data":{"effectiveAccess":{
			"user":{"id":"u2","name":"Bob","email":"bob@acme.com","handle":"bob"},
			"resourceUrn":"hrn:memory:acme.com::kb","resourceKind":"memory",
			"canRead":false,"canWrite":false,"canManage":false,"canDelete":false,
			"role":null,"grants":[]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"access", "check", "bob@acme.com", "hrn:memory:acme.com::kb", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "No access") {
		t.Errorf("expected no-access message, got:\n%s", text)
	}
}

// An under-qualified resource ref is rejected locally (exit 2) before any
// GraphQL call — the fake server has no registered operations, so a network
// round-trip would fail the test.
func TestAccessCheckUnderQualifiedResource(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"access", "check", "usr_1", "acme.com::kb", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for under-qualified resource")
	}
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("expected usage exit code, got %d (%v)", code, err)
	}
}

// A leading "@" is a handle sigil: it must be stripped so the exact handle
// match wins over an unrelated fuzzy hit (rather than erroring as ambiguous).
func TestAccessCheckHandleSigil(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SearchUsers": `{"data":{"users":{"total":2,"items":[` +
			searchUserJSON("u1", "Alice", "alice@acme.com", "alice") + `,` +
			searchUserJSON("u2", "Alice Smith", "asmith@acme.com", "alicesmith") + `]}}}`,
		"EffectiveAccess": `{"data":{"effectiveAccess":{
			"user":{"id":"u1","name":"Alice","email":"alice@acme.com","handle":"alice"},
			"resourceUrn":"hrn:memory:acme.com::kb","resourceKind":"memory",
			"canRead":true,"canWrite":false,"canManage":false,"canDelete":false,
			"role":"reader","grants":[{"source":"MEMORY_MEMBER","role":"reader","via":null}]}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"access", "check", "@alice", "hrn:memory:acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		User string `json:"user"`
	}
	if err := json.Unmarshal(captured["EffectiveAccess"], &vars); err != nil {
		t.Fatalf("decode vars: %v", err)
	}
	if vars.User != "u1" {
		t.Errorf("@alice should resolve to the exact-handle match u1, got %q", vars.User)
	}
}

// Multiple non-exact user matches are ambiguous rather than an arbitrary pick.
func TestAccessCheckAmbiguousUser(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"SearchUsers": `{"data":{"users":{"total":2,"items":[` +
			searchUserJSON("u1", "Alice One", "alice1@acme.com", "alice1") + `,` +
			searchUserJSON("u2", "Alice Two", "alice2@acme.com", "alice2") + `]}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"access", "check", "alice", "hrn:memory:acme.com::kb", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("expected usage exit code, got %d (%v)", code, err)
	}
}
