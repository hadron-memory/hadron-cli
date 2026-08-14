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

// #423: the command printed a resource URN it then refused as input — bare for
// a memory/agent/app, prefixed for a node, within one command's own output.
// Every kind must come out canonical v2, in --json and in the human line.
func TestAccessCheckEmitsCanonicalResourceURN(t *testing.T) {
	cases := []struct {
		name, kind, serverUrn, want string
	}{
		{"memory", "memory", "hadronmemory.com:dev", "hrn:mem:hadronmemory.com:dev"},
		{"agent", "agent", "hadronmemory.com:ada", "hrn:agent:hadronmemory.com:ada"},
		// The node branch already emitted prefixed — it must not be touched.
		{"node", "node", "hrn:node:hadronmemory.com:dev:preflight", "hrn:node:hadronmemory.com:dev:preflight"},
		// An AiServiceConfig has no URN: the field carries its id, verbatim.
		{"config", "aiServiceConfig", "cfg_123", "cfg_123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{
				"SearchUsers": `{"data":{"users":{"total":1,"items":[` +
					searchUserJSON("u1", "Alice", "alice@acme.com", "alice") + `]}}}`,
				"EffectiveAccess": `{"data":{"effectiveAccess":{
					"user":{"id":"u1","name":"Alice","email":"alice@acme.com","handle":"alice"},
					"resourceUrn":"` + tc.serverUrn + `","resourceKind":"` + tc.kind + `",
					"canRead":true,"canWrite":false,"canManage":false,"canDelete":false,
					"role":"reader","grants":[]}}}`,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"access", "check", "alice@acme.com", "hrn:mem:acme.com:kb",
				"--json", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var dto struct {
				Resource struct {
					URN  string `json:"urn"`
					Kind string `json:"kind"`
				} `json:"resource"`
			}
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatalf("--json: %v (%s)", err, out.String())
			}
			if dto.Resource.URN != tc.want {
				t.Errorf("resource.urn = %q, want %q", dto.Resource.URN, tc.want)
			}

			// The human line carries the same value — the issue reports both.
			f2, out2 := testFactory(t)
			root2 := NewRootCmd(f2)
			root2.SetArgs([]string{"access", "check", "alice@acme.com", "hrn:mem:acme.com:kb", "--server", gql.URL})
			if err := root2.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !strings.Contains(out2.String(), "Resource: "+tc.want+" ("+tc.kind+")") {
				t.Errorf("human output should carry the canonical URN: %s", out2.String())
			}
		})
	}
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
		"GetUser": `{"data":{"user":null}}`, // find-one misses; the paged search must still resolve
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
		"GetUser": `{"data":{"user":null}}`, // neither a PK nor an exact handle
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
		case "GetUser":
			// The find-one fast path misses — the exact match is only
			// reachable through the paged search.
			_, _ = w.Write([]byte(`{"data":{"user":null}}`))
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

// An @handle ref resolves through the user(ref:) find-one fast path — one
// round trip, no paged search at all (SearchUsers is deliberately not
// registered: calling it would fail the test).
func TestAccessCheckFastPathHandle(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetUser": `{"data":{"user":` +
			searchUserJSON("u9", "Alice", "alice@acme.com", "alice") + `}}`,
		"EffectiveAccess": `{"data":{"effectiveAccess":{
			"user":{"id":"u9","name":"Alice","email":"alice@acme.com","handle":"alice"},
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
	// A sigiled ref is handle-shaped: the find-one must get the URN form.
	var getUserVars struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(captured["GetUser"], &getUserVars); err != nil {
		t.Fatalf("decode GetUser vars: %v", err)
	}
	if getUserVars.Ref != "hrn:user:alice" {
		t.Errorf("expected user(ref: hrn:user:alice), got %q", getUserVars.Ref)
	}
	var vars struct {
		User string `json:"user"`
	}
	if err := json.Unmarshal(captured["EffectiveAccess"], &vars); err != nil {
		t.Fatalf("decode vars: %v", err)
	}
	if vars.User != "u9" {
		t.Errorf("@alice should fast-path to u9, got %q", vars.User)
	}
}

// A bare id-shaped ref fast-paths as a PK — the first user(ref:) attempt is
// the token itself, no URN wrapping and no search.
func TestAccessCheckFastPathId(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetUser": `{"data":{"user":` +
			searchUserJSON("usr_42", "Bob", "bob@acme.com", "bob") + `}}`,
		"EffectiveAccess": `{"data":{"effectiveAccess":{
			"user":{"id":"usr_42","name":"Bob","email":"bob@acme.com","handle":"bob"},
			"resourceUrn":"hrn:memory:acme.com::kb","resourceKind":"memory",
			"canRead":true,"canWrite":true,"canManage":false,"canDelete":false,
			"role":"writer","grants":[{"source":"MEMORY_SHARE","role":"writer","via":null}]}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"access", "check", "usr_42", "hrn:memory:acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var getUserVars struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(captured["GetUser"], &getUserVars); err != nil {
		t.Fatalf("decode GetUser vars: %v", err)
	}
	if getUserVars.Ref != "usr_42" {
		t.Errorf("expected user(ref: usr_42) first, got %q", getUserVars.Ref)
	}
	var vars struct {
		User string `json:"user"`
	}
	if err := json.Unmarshal(captured["EffectiveAccess"], &vars); err != nil {
		t.Fatalf("decode vars: %v", err)
	}
	if vars.User != "usr_42" {
		t.Errorf("usr_42 should fast-path to itself, got %q", vars.User)
	}
}
