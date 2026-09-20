package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ownedByMeJSON is a Memories page as the server answers `ownedByMe: true`.
// Both rows are org-less and the caller's, and they are deliberately of
// DIFFERENT classes: `personal` (owned via the strict userId column) and
// `knowledge` (owned via the spec-047 ownerUserId tenant column). The second
// is the population a class-based approximation drops — hadron-server#1176,
// and the two rows portal#866 recovered.
const ownedByMeJSON = `{"data":{"memories":{"total":2,"items":[
	{"id":"m1","urn":"hrn:mem:holger:jens","name":"Jens","shortDescription":null,"class":"personal",
	 "visibility":null,"organizationId":null,"isEncrypted":false,"maxRevCount":10,
	 "updatedAt":"2026-09-20T00:00:00Z"},
	{"id":"m2","urn":"hrn:mem:holger:holgers-gear","name":"Holgers Gear","shortDescription":null,
	 "class":"knowledge","visibility":null,"organizationId":null,"isEncrypted":false,
	 "maxRevCount":10,"updatedAt":"2026-09-20T00:00:00Z"}]}}}`

// memoriesFilter pulls the `filter` variable out of the captured Memories
// request. present reports whether the operation ran at all, so a test can
// tell "sent no filter" from "never made the call" — otherwise a command that
// silently stopped querying would pass every assertion about what it did not
// send.
//
// The filter comes back as a map rather than a typed struct on purpose: these
// tests assert which keys reached the wire, and a decode into a struct renders
// an absent key and an explicit null identically.
func memoriesFilter(t *testing.T, captured map[string]json.RawMessage) (filter map[string]any, present bool) {
	t.Helper()
	raw, ok := captured["Memories"]
	if !ok {
		return nil, false
	}
	var vars map[string]any
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("unmarshal Memories variables: %v", err)
	}
	f, _ := vars["filter"].(map[string]any)
	return f, true
}

// #617: the predicate is FORWARDED, never reproduced. The request must carry
// filter.ownedByMe, and every row the server returns must survive — including
// the knowledge-class one, which is exactly what a client-side repair on class
// or organizationId would drop (the repair portal#866 deleted).
func TestMemoryLsOwnedByMe(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"Memories": ownedByMeJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--owned-by-me", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	filter, present := memoriesFilter(t, captured)
	if !present {
		t.Fatal("--owned-by-me must still query the own-union listing")
	}
	if filter["ownedByMe"] != true {
		t.Errorf("--owned-by-me must forward filter.ownedByMe: true, got %v", filter)
	}
	// The slice is org-less BY THE SERVER'S definition. A client that also sent
	// a class or visibility clause would be narrowing a predicate it does not
	// own, and the two would drift apart the moment the server's did.
	if len(filter) != 1 {
		t.Errorf("--owned-by-me must send ownedByMe alone, got %v", filter)
	}

	var memories []struct {
		URN   string `json:"urn"`
		Class string `json:"class"`
	}
	if err := json.Unmarshal([]byte(out.String()), &memories); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	// BOTH rows named, in order — not a count, and not one row spot-checked.
	// A length of 2 passes if the command kept one row twice, and naming only
	// the knowledge row passes if it dropped the personal one and duplicated
	// the survivor. The pair is the assertion: the personal row proves nothing
	// was lost, the knowledge row proves nothing was filtered by class.
	want := []struct{ urn, class string }{
		{"hrn:mem:holger:jens", "personal"},
		{"hrn:mem:holger:holgers-gear", "knowledge"},
	}
	if len(memories) != len(want) {
		t.Fatalf("every row the server returned must survive, got %d: %s", len(memories), out.String())
	}
	for i, w := range want {
		if memories[i].URN != w.urn || memories[i].Class != w.class {
			t.Errorf("row %d: got %+v, want {%s %s}", i, memories[i], w.urn, w.class)
		}
	}
}

// The default listing is unchanged: no filter at all, so `ownedByMe` cannot
// reach the wire as an explicit null either (the omitempty the for-directive
// binds). A caller who did not ask must send byte-identical requests to the
// ones sent before this flag existed.
func TestMemoryLsDefaultSendsNoOwnershipFilter(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"Memories": ownedByMeJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	filter, present := memoriesFilter(t, captured)
	if !present {
		t.Fatal("the default listing must query Memories")
	}
	if filter != nil {
		t.Errorf("the default listing must send no filter, got %v", filter)
	}
}

// The other filter-bearing flag must not drag ownedByMe onto the wire as an
// explicit null. This is the case --owned-by-me's own tests cannot reach: with
// --include-agent-system the filter object EXISTS, so a missing omitempty
// would ride along inside it, and the server reads null as a value rather than
// as silence.
//
// Asserted by KEY PRESENCE, deliberately. A typed decode — or a `== nil` test
// on the value — renders "ownedByMe": null and an absent key identically, so
// the assertion would pass with the omitempty directive dropped and the wire
// contract broken.
func TestMemoryLsIncludeAgentSystemOmitsOwnedByMe(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"Memories": ownedByMeJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--include-agent-system", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	filter, present := memoriesFilter(t, captured)
	if !present || filter == nil {
		t.Fatal("--include-agent-system must send a filter")
	}
	if _, sent := filter["ownedByMe"]; sent {
		t.Errorf("ownedByMe must be ABSENT, not null, when it was not asked for: %v", filter)
	}
}

// --include-agent-system composes: the clauses AND, so asking for your own
// memories WITH the system class is a meaningful narrowing, not a conflict.
// Both clauses must ride in one filter — dropping either would silently answer
// a different question.
func TestMemoryLsOwnedByMeComposesWithAgentSystem(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"Memories": ownedByMeJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--owned-by-me", "--include-agent-system", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	filter, present := memoriesFilter(t, captured)
	if !present {
		t.Fatal("Memories must be queried")
	}
	if filter["ownedByMe"] != true {
		t.Errorf("ownedByMe must survive --include-agent-system, got %v", filter)
	}
	classes, _ := filter["memoryClasses"].([]any)
	if len(classes) == 0 {
		t.Errorf("--include-agent-system must still send memoryClasses, got %v", filter)
	}
}

// Combining the two slices is empty BY CONSTRUCTION — a grantee is never their
// own grantor, so the shared slice excludes owned memories. An empty page is
// the one answer a caller cannot distinguish from a real result, so this is a
// usage refusal (exit 2) rather than a listing that happens to return nothing.
func TestMemoryLsOwnedByMeRejectsSharedWithMe(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--owned-by-me", "--shared-with-me", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil {
		t.Fatal("combining --owned-by-me with --shared-with-me should fail")
	}
	if got := renderError(f, err); got != exitcode.Usage {
		t.Fatalf("mutually exclusive flags should exit %d (usage), got %d", exitcode.Usage, got)
	}
}

// The human table is the same three columns as the default listing: this flag
// narrows the set, it does not change what a memory is. A row must not lose
// its class on the way through.
func TestMemoryLsOwnedByMeTable(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"Memories": ownedByMeJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--owned-by-me", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	text := out.String()
	for _, want := range []string{"CLASS", "hrn:mem:holger:holgers-gear", "knowledge"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in output:\n%s", want, text)
		}
	}
	// Ownership is not a share: the grantor columns belong to the other slice.
	if strings.Contains(text, "SHARED BY") {
		t.Errorf("--owned-by-me is not the shared slice:\n%s", text)
	}
}
