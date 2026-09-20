package cmd

import (
	"encoding/json"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #635 — the third leg of the ownedByMe trio cor:api:120:03 names. Agent and
// App each carry ONE owner column (ownerUserId), so unlike memories() there is
// no per-class split; the spec warns explicitly against inferring that one
// column answers it everywhere, so these tests assert the FORWARDING and never
// the predicate's meaning.

const agentsOwnedJSON = `{"data":{"agents":{"total":1,"items":[
	{"id":"a1","urn":"hrn:agent:holger:flow-lab","name":"flow-lab","description":null,
	 "type":"ASSISTANT","visibility":"PERSONAL","organizationId":null,"surfaces":[],
	 "systemMemoryId":null,"systemPrompt":null,"aiProvider":null,"aiModel":null,
	 "hasAiApiKey":false,"personaRole":null,"personaPrompt":null,
	 "createdAt":"2026-09-20T00:00:00Z"}]}}}`

const appsOwnedJSON = `{"data":{"apps":{"total":1,"items":[
	{"id":"ap1","urn":"hrn:app:holger:flow-lab","name":"Flow Lab","appType":"STANDARD",
	 "agentId":"a1","memberCount":1,"createdAt":"2026-09-20T00:00:00Z"}]}}}`

// varsOf decodes a captured request's variables. Returns present=false when the
// operation never ran, so "sent no filter" stays distinguishable from "never
// made the call" — a command that silently stopped querying would otherwise
// satisfy every assertion about what it did not send.
func varsOf(t *testing.T, captured map[string]json.RawMessage, op string) (vars map[string]any, present bool) {
	t.Helper()
	raw, ok := captured[op]
	if !ok {
		return nil, false
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("unmarshal %s variables: %v", op, err)
	}
	return vars, true
}

func TestAgentLsOwnedByMe(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"Agents": agentsOwnedJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "ls", "--owned-by-me", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["PublicAgents"]; ok {
		t.Error("--owned-by-me must use the member-scoped listing, not the marketplace slice")
	}
	vars, present := varsOf(t, captured, "Agents")
	if !present {
		t.Fatal("Agents must be queried")
	}
	filter, _ := vars["filter"].(map[string]any)
	if filter["ownedByMe"] != true {
		t.Errorf("must forward filter.ownedByMe: true, got %v", filter)
	}
	if len(filter) != 1 {
		t.Errorf("ownedByMe must ride alone when no other filter flag is given, got %v", filter)
	}
	// The slice is org-less by definition, so the server never consults orgId.
	// Sending one would be the client narrowing a predicate it does not own.
	if _, sent := vars["orgId"]; sent {
		t.Errorf("orgId must not be sent with --owned-by-me, got %v", vars["orgId"])
	}
	var agents []struct {
		URN string `json:"urn"`
	}
	if err := json.Unmarshal([]byte(out.String()), &agents); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(agents) != 1 || agents[0].URN != "hrn:agent:holger:flow-lab" {
		t.Errorf("the returned row must survive unfiltered, got %+v", agents)
	}
}

// --type and --visibility still narrow the owner slice: the clauses AND. All
// three must ride in ONE filter — dropping any would answer a different
// question while still returning rows.
func TestAgentLsOwnedByMeComposesWithTypeAndVisibility(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"Agents": agentsOwnedJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "ls", "--owned-by-me", "--type", "ASSISTANT",
		"--visibility", "PERSONAL", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars, _ := varsOf(t, captured, "Agents")
	filter, _ := vars["filter"].(map[string]any)
	for k, want := range map[string]any{"ownedByMe": true, "type": "ASSISTANT", "visibility": "PERSONAL"} {
		if filter[k] != want {
			t.Errorf("filter[%q] = %v, want %v (full: %v)", k, filter[k], want, filter)
		}
	}
}

// The unfiltered listing must be unchanged: no filter at all, so ownedByMe
// cannot reach the wire as an explicit null either. Asserted by KEY PRESENCE —
// a typed decode renders null and absent identically.
func TestAgentLsDefaultSendsNoOwnershipFilter(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"Agents": agentsOwnedJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "ls", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars, present := varsOf(t, captured, "Agents")
	if !present {
		t.Fatal("Agents must be queried")
	}
	if _, sent := vars["filter"]; sent {
		t.Errorf("the unfiltered listing must send no filter, got %v", vars["filter"])
	}
}

// --type alone still builds a filter, and ownedByMe must be ABSENT from it
// rather than null. This is the case --owned-by-me's own tests cannot reach:
// the filter object exists on this path, so a missing omitempty would ride
// along inside it.
func TestAgentLsTypeOnlyOmitsOwnedByMe(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"Agents": agentsOwnedJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "ls", "--type", "ASSISTANT", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars, _ := varsOf(t, captured, "Agents")
	filter, _ := vars["filter"].(map[string]any)
	if filter == nil {
		t.Fatal("--type must send a filter")
	}
	if _, sent := filter["ownedByMe"]; sent {
		t.Errorf("ownedByMe must be ABSENT, not null, when not asked for: %v", filter)
	}
}

// --public switches to PublicAgentFilter, which has no ownedByMe FIELD at all —
// the server rejects it at the schema because a PUBLIC agent is never
// user-owned. Refusing client-side names the reason instead of surfacing a
// GraphQL validation error.
func TestAgentLsOwnedByMeRejectsPublic(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "ls", "--owned-by-me", "--public", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil {
		t.Fatal("--owned-by-me with --public should fail")
	}
	if got := renderError(f, err); got != exitcode.Usage {
		t.Fatalf("want exit %d (usage), got %d", exitcode.Usage, got)
	}
}

// --org is refused because the slice is org-less by definition. Keyed on the
// flag being CHANGED, so `--org=` is caught too: asking for nothing is a
// different mistake from not asking, and a value test reads it as the latter.
func TestAgentLsOwnedByMeRejectsOrg(t *testing.T) {
	for _, args := range [][]string{
		{"agent", "ls", "--owned-by-me", "--org", "acme.com"},
		{"agent", "ls", "--owned-by-me", "--org="},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", "http://127.0.0.1:1"))
		err := root.Execute()
		if err == nil {
			t.Fatalf("%v should fail", args)
		}
		if got := renderError(f, err); got != exitcode.Usage {
			t.Errorf("%v: want exit %d (usage), got %d", args, exitcode.Usage, got)
		}
	}
}

func TestAppLsOwnedByMe(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"Apps": appsOwnedJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "ls", "--owned-by-me", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars, present := varsOf(t, captured, "Apps")
	if !present {
		t.Fatal("Apps must be queried")
	}
	filter, _ := vars["filter"].(map[string]any)
	if filter["ownedByMe"] != true {
		t.Errorf("must forward filter.ownedByMe: true, got %v", filter)
	}
	// orgId was ID! before this change; the owner slice is org-less, so it must
	// now be omittable — not sent as "" or null.
	if _, sent := vars["orgId"]; sent {
		t.Errorf("orgId must be omitted entirely with --owned-by-me, got %v", vars["orgId"])
	}
	var apps []struct {
		URN string `json:"urn"`
	}
	if err := json.Unmarshal([]byte(out.String()), &apps); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(apps) != 1 || apps[0].URN != "hrn:app:holger:flow-lab" {
		t.Errorf("the returned row must survive, got %+v", apps)
	}
}

// The --org path is unchanged: orgId still goes out, and no ownership filter
// rides along.
func TestAppLsOrgUnchanged(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"Apps": appsOwnedJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "ls", "--org", "acme.com", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars, _ := varsOf(t, captured, "Apps")
	if vars["orgId"] != "acme.com" {
		t.Errorf("orgId = %v, want acme.com", vars["orgId"])
	}
	if _, sent := vars["filter"]; sent {
		t.Errorf("the --org listing must send no filter, got %v", vars["filter"])
	}
}

// Exactly one of --org / --owned-by-me. The bare command must still refuse —
// dropping MarkFlagRequired("org") to make room for --owned-by-me must not
// quietly turn `app list` into an unscoped listing.
func TestAppLsRequiresExactlyOneSlice(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"neither", []string{"app", "ls"}},
		{"both", []string{"app", "ls", "--owned-by-me", "--org", "acme.com"}},
		{"both, org empty", []string{"app", "ls", "--owned-by-me", "--org="}},
		{"org asked for nothing", []string{"app", "ls", "--org="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"Apps": appsOwnedJSON})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("should fail")
			}
			if got := renderError(f, err); got != exitcode.Usage {
				t.Errorf("want exit %d (usage), got %d", exitcode.Usage, got)
			}
			// A refusal must refuse BEFORE the request — an unscoped Apps query
			// from a platform admin pulls every App on the server.
			if _, ran := captured["Apps"]; ran {
				t.Error("refused invocation must not have queried the server")
			}
		})
	}
}
