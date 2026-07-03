package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

// users() is name-ascending and served in 200-cap pages, so with >200
// substring matches the EXACT handle match can sit on a later page. The
// resolver must keep paging (short-circuiting once the exact match is in
// hand) instead of judging page one alone ambiguous.
func TestAccessCheckExactMatchBeyondFirstPage(t *testing.T) {
	// Page 1: 200 fuzzy "alice…" matches, none exact. Page 2: the exact
	// handle match, sorted last by name.
	fuzzy := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		fuzzy = append(fuzzy, searchUserJSON(
			fmt.Sprintf("u%03d", i),
			fmt.Sprintf("Alice %03d", i),
			fmt.Sprintf("alice%03d@acme.com", i),
			fmt.Sprintf("alice-%03d", i)))
	}
	page1 := `{"data":{"users":{"total":201,"items":[` + strings.Join(fuzzy, ",") + `]}}}`
	page2 := `{"data":{"users":{"total":201,"items":[` +
		searchUserJSON("u-exact", "Zz Alice", "alice@acme.com", "alice") + `]}}}`

	var searchCalls int
	var effectiveVars json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Offset *int `json:"offset"`
			} `json:"variables"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "SearchUsers":
			searchCalls++
			if body.Variables.Offset != nil && *body.Variables.Offset >= 200 {
				_, _ = w.Write([]byte(page2))
				return
			}
			_, _ = w.Write([]byte(page1))
		case "EffectiveAccess":
			var envelope struct {
				Variables json.RawMessage `json:"variables"`
			}
			_ = json.Unmarshal(raw, &envelope)
			effectiveVars = envelope.Variables
			_, _ = w.Write([]byte(`{"data":{"effectiveAccess":{
				"user":{"id":"u-exact","name":"Zz Alice","email":"alice@acme.com","handle":"alice"},
				"resourceUrn":"hrn:memory:acme.com::kb","resourceKind":"memory",
				"canRead":true,"canWrite":false,"canManage":false,"canDelete":false,
				"role":"reader","grants":[{"source":"MEMORY_MEMBER","role":"reader","via":null}]}}}`))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
		}
	}))
	t.Cleanup(server.Close)

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"access", "check", "@alice", "hrn:memory:acme.com::kb", "--json", "--server", server.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("expected the page-2 exact handle match to resolve, got: %v", err)
	}
	if searchCalls != 2 {
		t.Errorf("expected 2 SearchUsers pages, got %d", searchCalls)
	}
	var vars struct {
		User string `json:"user"`
	}
	if err := json.Unmarshal(effectiveVars, &vars); err != nil {
		t.Fatalf("decode effectiveAccess vars: %v", err)
	}
	if vars.User != "u-exact" {
		t.Errorf("@alice should resolve to the exact-handle match u-exact, got %q", vars.User)
	}
}
