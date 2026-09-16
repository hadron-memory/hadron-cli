package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// scopeJSON is one scope as the server projects ScopeFields. hiddenMemoryCount
// is deliberately non-zero in most fixtures: a scope whose listed memories are
// all readable is the case that hides the disclosure bug.
const scopeJSON = `{
	"id":"0123456789abcdef0123456789abcdef","name":"research","description":"papers",
	"ownerType":"ORGANIZATION","ownerId":"org1","ownerUrn":"hrn:org:acme.com",
	"memoryCount":3,"hiddenMemoryCount":1,
	"createdAt":"2026-09-16T00:00:00Z","updatedAt":null,
	"memories":[
		{"position":0,"memory":{"id":"m1","urn":"hrn:mem:acme.com:papers","name":"Papers","shortDescription":null}},
		{"position":1,"memory":{"id":"m2","urn":"hrn:mem:acme.com:notes","name":"Notes","shortDescription":null}}
	]}`

// TestScopeCreateSendsMemoryOrderToTheWire pins the ORDER of --memory.
//
// A scope's memory list is ordered and the order decides which memory wins for
// an address, so reordering it silently changes behaviour. No output-level
// assertion can see this — the server echoes back whatever it stored — so the
// only place to measure it is the request.
func TestScopeCreateSendsMemoryOrderToTheWire(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateScope": `{"data":{"createScope":` + scopeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"scope", "create", "research",
		"--owner-org", "acme.com",
		"-m", "hrn:mem:acme.com:papers",
		"-m", "hrn:mem:acme.com:notes",
		"-m", "hrn:mem:acme.com:archive",
		"--json", "--server", gql.URL,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input struct {
			Name            string   `json:"name"`
			MemoryRefs      []string `json:"memoryRefs"`
			OrganizationRef *string  `json:"organizationRef"`
			AppRef          *string  `json:"appRef"`
			AgentRef        *string  `json:"agentRef"`
		} `json:"input"`
	}
	if err := json.Unmarshal(captured["CreateScope"], &vars); err != nil {
		t.Fatalf("decode vars: %v", err)
	}
	want := []string{"hrn:mem:acme.com:papers", "hrn:mem:acme.com:notes", "hrn:mem:acme.com:archive"}
	if len(vars.Input.MemoryRefs) != len(want) {
		t.Fatalf("memoryRefs = %v, want %v", vars.Input.MemoryRefs, want)
	}
	for i := range want {
		if vars.Input.MemoryRefs[i] != want[i] {
			t.Errorf("memoryRefs[%d] = %q, want %q (order is load-bearing)", i, vars.Input.MemoryRefs[i], want[i])
		}
	}
	if vars.Input.OrganizationRef == nil || *vars.Input.OrganizationRef != "acme.com" {
		t.Errorf("organizationRef = %v, want acme.com", vars.Input.OrganizationRef)
	}
	// The two owners NOT chosen must be absent, not null: the server's rule is
	// "exactly one owner", and an explicit null is a value being offered.
	if vars.Input.AppRef != nil || vars.Input.AgentRef != nil {
		t.Errorf("unchosen owners must be omitted, got appRef=%v agentRef=%v", vars.Input.AppRef, vars.Input.AgentRef)
	}
}

// TestScopeCreateRefusesTwoOwners drives the REAL command, not the helper.
//
// Calling exactlyOneOwner directly would pass with the wiring deleted; the
// point of the check is that the command refuses before issuing a mutation.
func TestScopeCreateRefusesTwoOwners(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"scope", "create", "research",
		"--owner-org", "acme.com", "--owner-app", "hrn:app:acme.com:dev",
		"-m", "hrn:mem:acme.com:papers",
		"--server", gql.URL,
	})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a refusal when two owners are passed")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("error should name the arity rule, got: %v", err)
	}
	if _, ok := captured["CreateScope"]; ok {
		t.Error("CreateScope must not be called when the owner flags are ambiguous")
	}
}

// TestScopeCreateRefusesNoMemories — a scope with no memories is not a lens.
func TestScopeCreateRefusesNoMemories(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"scope", "create", "research", "--owner-org", "acme.com", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a refusal when no --memory is passed")
	}
	if _, ok := captured["CreateScope"]; ok {
		t.Error("CreateScope must not be called without memories")
	}
}

// TestScopeGetResolvesNameThroughTheServer pins that a bare NAME is resolved by
// ScopeExplain and that the RESOLVED ID is what reaches GetScope.
//
// A client-side name match would produce identical output, so this is asserted
// on the wire: the presence of the ScopeExplain call is the behaviour under
// test, not a detail of it.
func TestScopeGetResolvesNameThroughTheServer(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ScopeExplain": `{"data":{"scopeExplain":{"resolvedVia":"ORGANIZATION","droppedCount":0,
			"scope":` + scopeJSON + `,"memories":[],"winner":null,"shadowed":[]}}}`,
		"GetScope": `{"data":{"scope":` + scopeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"scope", "get", "research", "--app", "hrn:app:acme.com:dev", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["ScopeExplain"]; !ok {
		t.Fatal("a bare name must be resolved server-side via ScopeExplain, not matched locally")
	}
	var explainVars map[string]any
	_ = json.Unmarshal(captured["ScopeExplain"], &explainVars)
	if explainVars["name"] != "research" {
		t.Errorf("ScopeExplain should resolve the NAME, got vars %v", explainVars)
	}
	var getVars map[string]any
	_ = json.Unmarshal(captured["GetScope"], &getVars)
	if getVars["ref"] != "0123456789abcdef0123456789abcdef" {
		t.Errorf("GetScope should receive the RESOLVED id, got %v", getVars["ref"])
	}
}

// TestScopeGetByIDSkipsResolution — an id is unambiguous, so it must not cost a
// resolution round trip.
func TestScopeGetByIDSkipsResolution(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetScope": `{"data":{"scope":` + scopeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"scope", "get", "0123456789abcdef0123456789abcdef", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["ScopeExplain"]; ok {
		t.Error("a bare id needs no ScopeExplain round trip")
	}
}

// TestScopeGetSurfacesHiddenMemories is the visibility-gap guard.
//
// The scope lists 3 memories and projects 2; the third is unreadable. Rendering
// the two without saying so understates the scope, which is the failure mode
// CLAUDE.md names. Asserted in BOTH branches because they are separate code
// paths and a fix to one does not reach the other.
func TestScopeGetSurfacesHiddenMemories(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"GetScope": `{"data":{"scope":` + scopeJSON + `}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"scope", "get", "0123456789abcdef0123456789abcdef", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var dto struct {
			MemoryCount       int `json:"memoryCount"`
			HiddenMemoryCount int `json:"hiddenMemoryCount"`
			Memories          []struct {
				Position int `json:"position"`
			} `json:"memories"`
		}
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
			t.Fatalf("decode: %v — %s", err, out.String())
		}
		if dto.HiddenMemoryCount != 1 {
			t.Errorf("hiddenMemoryCount = %d, want 1", dto.HiddenMemoryCount)
		}
		if dto.MemoryCount != 3 || len(dto.Memories) != 2 {
			t.Errorf("memoryCount=%d with %d listed: the pair is what discloses the gap", dto.MemoryCount, len(dto.Memories))
		}
	})

	t.Run("human", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"GetScope": `{"data":{"scope":` + scopeJSON + `}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"scope", "get", "0123456789abcdef0123456789abcdef", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(out.String(), "not readable by you") {
			t.Errorf("the human branch must disclose the hidden memory, got:\n%s", out.String())
		}
	})
}

// TestScopeUpdateOmitsUnsetFields is the omitempty guard.
//
// The server reads an OMITTED field as preserve and an explicit null as clear,
// so an unset flag that serializes to null silently wipes a value. A decode
// cannot tell an absent key from a null one — `jq .field` and encoding/json
// both read both as null — so this asserts on the RAW request body.
func TestScopeUpdateOmitsUnsetFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateScope": `{"data":{"updateScope":` + scopeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"scope", "update", "0123456789abcdef0123456789abcdef",
		"--description", "papers we actually cite",
		"--json", "--server", gql.URL,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	raw := string(captured["UpdateScope"])
	if !strings.Contains(raw, `"description"`) {
		t.Errorf("the field that WAS passed must be present: %s", raw)
	}
	// Prove the keys are ABSENT rather than null — the distinction the wire
	// semantics turn on.
	for _, key := range []string{`"name"`, `"memoryRefs"`} {
		if strings.Contains(raw, key) {
			t.Errorf("unset %s must be OMITTED, not sent (it would clear the field): %s", key, raw)
		}
	}
}

// TestScopeUpdateRefusesEmptyMemoryList — --memory REPLACES the list, so an
// empty one would silently empty the scope.
func TestScopeUpdateRefusesNothingToDo(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"scope", "update", "0123456789abcdef0123456789abcdef", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a refusal when no field flag is passed")
	}
	if _, ok := captured["UpdateScope"]; ok {
		t.Error("UpdateScope must not be called with an empty input")
	}
}

// TestScopeExplainReportsShadowedAndDropped — the two disclosures are the
// reason `explain` exists, so both are pinned.
func TestScopeExplainReportsShadowedAndDropped(t *testing.T) {
	shadow := strings.Replace(scopeJSON, `"id":"0123456789abcdef0123456789abcdef"`, `"id":"ffffffffffffffffffffffffffffffff"`, 1)
	gql, _ := captureGraphQL(t, map[string]string{
		"ScopeExplain": `{"data":{"scopeExplain":{"resolvedVia":"APP","droppedCount":2,
			"scope":` + scopeJSON + `,
			"memories":[{"id":"m1","urn":"hrn:mem:acme.com:papers","name":"Papers"}],
			"winner":{"id":"m1","urn":"hrn:mem:acme.com:papers","name":"Papers"},
			"shadowed":[` + shadow + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"scope", "explain", "research", "--app", "hrn:app:acme.com:dev", "--loc", "findings:x", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		ResolvedVia  string `json:"resolvedVia"`
		DroppedCount int    `json:"droppedCount"`
		Winner       *struct {
			URN string `json:"urn"`
		} `json:"winner"`
		Shadowed []struct {
			ID string `json:"id"`
		} `json:"shadowed"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("decode: %v — %s", err, out.String())
	}
	if dto.ResolvedVia != "APP" {
		t.Errorf("resolvedVia = %q, want APP — the server reports which rung it used", dto.ResolvedVia)
	}
	if dto.DroppedCount != 2 {
		t.Errorf("droppedCount = %d, want 2", dto.DroppedCount)
	}
	if dto.Winner == nil || dto.Winner.URN != "hrn:mem:acme.com:papers" {
		t.Errorf("winner = %v, want the memory that wins for --loc", dto.Winner)
	}
	if len(dto.Shadowed) != 1 {
		t.Fatalf("shadowed = %v, want the one lower-precedence scope", dto.Shadowed)
	}
}

// TestScopeGetUnreadableSaysOnlyWhatIsKnown.
//
// scope(ref:) returns null for a scope that does not exist AND one the caller
// may not read, identically and on purpose (no existence disclosure). The
// message must not claim the scope does not exist.
func TestScopeGetUnreadableSaysOnlyWhatIsKnown(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetScope": `{"data":{"scope":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"scope", "get", "0123456789abcdef0123456789abcdef", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for an unreadable scope")
	}
	if !strings.Contains(err.Error(), "readable") {
		t.Errorf("message should assert only unreadability, got: %v", err)
	}
	if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") {
		t.Errorf("message must not claim non-existence the server declined to disclose, got: %v", err)
	}
}

// TestScopeListDisclosesHiddenAcrossScopes — the aggregate disclosure, without
// which MEMORIES in the listing silently disagrees with `scope get`.
func TestScopeListDisclosesHiddenAcrossScopes(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"Scopes": `{"data":{"scopes":{"total":1,"items":[` + scopeJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"scope", "list", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "not readable by you") {
		t.Errorf("the listing must disclose hidden memories, got:\n%s", out.String())
	}
}

// TestScopeNameWithoutAppContextNamesAFlag.
//
// The server DOES refuse a bare name with no App context — but in its own
// words: "pass appRef", a GraphQL field with no flag behind it, leaving the
// reader nothing to type. Found by running the command rather than by reading
// it. Both name-taking paths are covered: `get` pre-resolves, `explain` passes
// the name straight through, so a guard in one does not reach the other.
func TestScopeNameWithoutAppContextNamesAFlag(t *testing.T) {
	for _, verb := range []string{"get", "explain"} {
		t.Run(verb, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"scope", verb, "research", "--server", gql.URL})
			err := root.Execute()
			if err == nil {
				t.Fatal("expected a refusal for a bare name with no App context")
			}
			if !strings.Contains(err.Error(), "--app") {
				t.Errorf("the message must name a flag the reader can type, got: %v", err)
			}
			if strings.Contains(err.Error(), "appRef") {
				t.Errorf("the message must not name the GraphQL field, got: %v", err)
			}
			if _, ok := captured["ScopeExplain"]; ok {
				t.Error("the refusal should precede the round trip")
			}
		})
	}
}

// TestScopeByNameReachesAnIDShapedName — @codex on #594.
//
// Scope names allow [a-z0-9_-], so a 32-character all-hex NAME is legal AND
// id-shaped. Shape still decides by default (it costs no round trip, and the
// collision needs a pathological name), but --by-name must make such a scope
// reachable rather than sending the name as a primary key.
//
// This is where scopes differ from nodes: IsNodeID is safe for locs because a
// loc cannot be 32 hex characters, and that reasoning does not carry over.
func TestScopeByNameReachesAnIDShapedName(t *testing.T) {
	const hexName = "deadbeefdeadbeefdeadbeefdeadbeef"

	t.Run("without --by-name it is treated as an id", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{
			"GetScope": `{"data":{"scope":` + scopeJSON + `}}`,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"scope", "get", hexName, "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if _, ok := captured["ScopeExplain"]; ok {
			t.Error("shape decides by default — an id-shaped argument must not cost a resolution")
		}
	})

	t.Run("with --by-name it is resolved as a name", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{
			"ScopeExplain": `{"data":{"scopeExplain":{"resolvedVia":"ORGANIZATION","droppedCount":0,
				"scope":` + scopeJSON + `,"memories":[],"winner":null,"shadowed":[]}}}`,
			"GetScope": `{"data":{"scope":` + scopeJSON + `}}`,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"scope", "get", hexName, "--by-name", "--app", "hrn:app:acme.com:dev", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		raw, ok := captured["ScopeExplain"]
		if !ok {
			t.Fatal("--by-name must force name resolution, or an id-shaped name is unreachable")
		}
		var vars map[string]any
		_ = json.Unmarshal(raw, &vars)
		if vars["name"] != hexName {
			t.Errorf("ScopeExplain should carry the NAME, got %v", vars)
		}
	})
}

// TestScopeIDOperationsIgnoreABrokenActiveApp — @codex on #594.
//
// An id needs no App context, so an unrelated ambient setting must not be able
// to break an otherwise unambiguous command. A hand-edited config whose App ref
// no longer parses makes f.App() a usage error; resolving it eagerly turned
// every id-based verb into a failure. Driven through the real command with a
// poisoned config, since a direct call to the resolver would not exercise the
// ordering under test.
func TestScopeIDOperationsIgnoreABrokenActiveApp(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetScope": `{"data":{"scope":` + scopeJSON + `}}`,
	})
	f, _ := testFactory(t)
	cfg, err := f.Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if err := cfg.Set("app", "not a valid app ref"); err != nil {
		t.Fatalf("poison config: %v", err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"scope", "get", "0123456789abcdef0123456789abcdef", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an id-based read must not consult the active App: %v", err)
	}
}
