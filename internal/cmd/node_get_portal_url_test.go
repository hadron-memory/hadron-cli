package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// #515 / cor:api:230:01 — `node get` prints the fully-qualified URN and the
// SERVER-BUILT link that opens it, and composes neither.
//
// The gap this closes: every role-agent briefing says "never hand-build a
// portal link — copy the URL line a node read prints". The MCP node read prints
// one; the CLI's never did, so on this surface the instruction was
// unsatisfiable and the only remaining option was the composition the rule
// forbids.

// nodeGetJSON is a GetNode response with portalUrl controllable. Passing ""
// yields JSON null — the absent case, and the one at risk of never being
// exercised, since a fixture with a link makes every assertion about absence
// vacuous. The urn is fixed: it is non-nullable server-side, so there is no
// absent case to stage for it.
func nodeGetJSON(portalURL string) string {
	q := func(s string) string {
		if s == "" {
			return "null"
		}
		return `"` + s + `"`
	}
	return `{"data":{"node":{"id":"n1","urn":"` + testNodeURN + `","portalUrl":` + q(portalURL) + `,
		"memoryId":"mem1","loc":"findings:flaky","name":"flaky","description":null,"abstract":null,
		"abstractOriginHash":null,"nodeType":"info","objectType":null,"tags":[],"content":null,
		"data":null,"properties":null,"seq":null,"isRunnable":false,
		"createdAt":"2026-08-24T00:00:00Z","updatedAt":"2026-08-24T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}}}`
}

const (
	testNodeURN = "hrn:node:acme.com:kb:findings:flaky"
	testNodeURL = "https://hadronmemory.com/app/u/hrn:node:acme.com:kb:findings:flaky"
)

// nodeGetStubs pairs the node payload with the ResolveUrn hop a URN-shaped ref
// takes before the read.
func nodeGetStubs(node string) map[string]string {
	return map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"GetNode":    node,
	}
}

func TestNodeGetPrintsURNAndPortalURL(t *testing.T) {
	gql, _ := captureGraphQL(t, nodeGetStubs(nodeGetJSON(testNodeURL)))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "urn: "+testNodeURN) {
		t.Errorf("the fully-qualified urn must print:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "URL: "+testNodeURL) {
		t.Errorf("the server's URL line must print:\n%s", out.String())
	}
	// ADJACENT, and the URN first: cor:api:230:01 wants the identifier and the
	// link it opens to read as one answer, so a reader copying "the URL line"
	// need not work out which identifier it belongs to.
	urnAt := strings.Index(out.String(), "urn: ")
	urlAt := strings.Index(out.String(), "URL: ")
	if urnAt < 0 || urlAt < urnAt {
		t.Errorf("the URL must follow the urn it opens:\n%s", out.String())
	}
}

// The absent case, which is the entire client obligation in the rule: render
// when present, render NOTHING when absent, and never fall back to composing
// one — a link to a guessed origin fails silently for whoever clicks it, which
// is worse than the caller seeing none.
func TestNodeGetOmitsAnAbsentPortalURL(t *testing.T) {
	gql, _ := captureGraphQL(t, nodeGetStubs(nodeGetJSON("")))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out.String(), "URL:") {
		t.Errorf("an absent link must render nothing at all:\n%s", out.String())
	}
	if strings.Contains(out.String(), "http") {
		t.Errorf("the CLI must not compose a link when the server sent none:\n%s", out.String())
	}
	// The IDENTIFIER SURVIVES the link's absence. They are independent answers,
	// and losing the link must not cost the caller the reference.
	if !strings.Contains(out.String(), "urn: "+testNodeURN) {
		t.Errorf("the urn must still print when the link is absent:\n%s", out.String())
	}
}

// --json carries both, and omits rather than nulls an absent one: these fields
// are omitempty because only the surfaces that select them populate them, so a
// null would assert "this server emits no link" on a surface that never asked.
func TestNodeGetJSONCarriesURNAndPortalURL(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		gql, _ := captureGraphQL(t, nodeGetStubs(nodeGetJSON(testNodeURL)))
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "get", testNodeURN, "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
			t.Fatalf("unmarshal: %v\n%s", err, out.String())
		}
		if dto["urn"] != testNodeURN {
			t.Errorf("urn = %v", dto["urn"])
		}
		if dto["portalUrl"] != testNodeURL {
			t.Errorf("portalUrl = %v", dto["portalUrl"])
		}
	})

	t.Run("absent is omitted, not null", func(t *testing.T) {
		gql, _ := captureGraphQL(t, nodeGetStubs(nodeGetJSON("")))
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "get", testNodeURN, "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, present := dto["portalUrl"]; present {
			t.Errorf("an absent link must leave the key out, got %v", dto["portalUrl"])
		}
		if dto["urn"] != testNodeURN {
			t.Errorf("the urn must survive: %v", dto["urn"])
		}
	})
}

// A WIRE assertion against the generated operation, never captureGraphQL —
// which records only request VARIABLES, so a selection assertion there can
// never fail (review:assert-the-query-not-the-capture).
func TestGetNodeSelectsURNAndPortalURL(t *testing.T) {
	for _, field := range []string{"urn", "portalUrl"} {
		var found bool
		for _, line := range strings.Split(gen.GetNode_Operation, "\n") {
			// Field-exact: a substring match would be satisfied by a longer
			// sibling sharing the prefix.
			if strings.TrimSpace(line) == field {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("GetNode must select %q on the wire", field)
		}
	}
}
