package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#1217: the "more may exist" hint fires only for a FULL page that the CLI
// sized itself (no --limit). Each negative case below kills one clause of
// `!explicitLimit && len(dtos) >= limit`.

const edgeRemovalsHint1217 = "more may exist"

func edgeRemovalsServer1217(t *testing.T) (string, map[string]json.RawMessage) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":       `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"NodeEdgeRemovals": edgeRemovalsTwoRows,
	})
	return gql.URL, captured
}

// A short default page (2 < 20) is the whole history: no hint.
func TestNodeEdgeRemovalsShortDefaultPageNoHint(t *testing.T) {
	url, _ := edgeRemovalsServer1217(t)
	out, errOut, err := runEdgeRemovals(t, url)
	if err != nil {
		t.Fatalf("node edge-removals: %v", err)
	}
	if !strings.Contains(out.String(), "src:depends-on:tgt") {
		t.Fatalf("rows not rendered:\n%s", out.String())
	}
	if strings.Contains(errOut.String(), edgeRemovalsHint1217) {
		t.Errorf("a short default page must not hint (got %q)", errOut.String())
	}
}

// An explicit --limit names the page the caller asked for, so even a full
// page (2 of --limit 2) prints no hint.
func TestNodeEdgeRemovalsExplicitFullPageNoHint(t *testing.T) {
	url, _ := edgeRemovalsServer1217(t)
	out, errOut, err := runEdgeRemovals(t, url, "--limit", "2")
	if err != nil {
		t.Fatalf("node edge-removals: %v", err)
	}
	if !strings.Contains(out.String(), "src:depends-on:tgt") {
		t.Fatalf("rows not rendered:\n%s", out.String())
	}
	if strings.Contains(errOut.String(), edgeRemovalsHint1217) {
		t.Errorf("an explicit --limit with a full page must not hint (got %q)", errOut.String())
	}
}

// --limit 0 is sent as 0 (the server answers an empty page); the CLI neither
// refuses it nor caps it (no server upper bound to mirror). Behaviour control.
func TestNodeEdgeRemovalsLimitZeroSentVerbatim(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":       `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"NodeEdgeRemovals": `{"data":{"nodeEdgeRemovals":[]}}`,
	})
	if _, _, err := runEdgeRemovals(t, gql.URL, "--limit", "0"); err != nil {
		t.Fatalf("node edge-removals --limit 0: %v", err)
	}
	var vars struct {
		Limit *int `json:"limit"`
	}
	if err := json.Unmarshal(captured["NodeEdgeRemovals"], &vars); err != nil {
		t.Fatal(err)
	}
	if vars.Limit == nil || *vars.Limit != 0 {
		t.Errorf("--limit 0 must be sent as limit: 0, got %v", vars.Limit)
	}
}

// The help says what --limit 0 does: the server returns an empty page.
func TestNodeEdgeRemovalsHelpStatesLimitZero(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "edge-removals", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--limit 0 returns an empty page") {
		t.Errorf("help must state that --limit 0 returns an empty page:\n%s", out.String())
	}
}
