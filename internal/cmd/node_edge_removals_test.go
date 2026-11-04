package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #1160 — `node edge-removals` reads the hard-delete edge audit trail
// (hadron-server#2517, built in #2562; cor:dmo:010:04): one entry per edge a hard delete
// removed from this node. The deleted target's address (targetLoc) and the
// removed edge's loc, which is derived from it, come back null when the caller
// cannot read the memory the target lived in — the nulls are concealment, and
// they must render honestly: JSON null, and an explicit "(withheld)" cell in
// the table, never a blank that reads as missing data.

const (
	edgeRemovalsRef = "hrn:node:acme.com:kb:src"

	edgeRemovalFull = `{"id":"rem1","createdAt":"2026-10-20T12:00:00Z",` +
		`"edgeLoc":"src:depends-on:tgt","edgeName":"depends_on",` +
		`"reason":"TARGET_HARD_DELETED","removedBy":"u1",` +
		`"targetId":"tgt1","targetLoc":"tgt"}`

	// Withheld row: a reader who cannot read the target's memory gets null
	// targetLoc AND null edgeLoc (the edge loc is derived from the target's
	// address); removedBy is independently null when unattributed.
	edgeRemovalWithheld = `{"id":"rem2","createdAt":"2026-10-19T09:30:00Z",` +
		`"edgeLoc":null,"edgeName":"cites",` +
		`"reason":"TARGET_HARD_DELETED","removedBy":null,` +
		`"targetId":"tgt2","targetLoc":null}`

	edgeRemovalsTwoRows = `{"data":{"nodeEdgeRemovals":[` + edgeRemovalFull + `,` + edgeRemovalWithheld + `]}}`
)

func runEdgeRemovals(t *testing.T, url string, extra ...string) (*strings.Builder, *strings.Builder, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := append([]string{"node", "edge-removals", edgeRemovalsRef, "--server", url}, extra...)
	root.SetArgs(args)
	err := root.Execute()
	return out, f.IOStreams.ErrOut.(*strings.Builder), err
}

// The wire: a fully-qualified URN resolves through ResolveUrn to the node's
// PK, and the query carries nodeRef + an EXPLICIT default limit of 20 — the
// server would apply its own default, but the CLI names the page it asked for
// so a full page can hint that more may exist (cli#967).
func TestNodeEdgeRemovalsWireSendsResolvedRefAndExplicitDefaultLimit(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":       `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"NodeEdgeRemovals": edgeRemovalsTwoRows,
	})
	if _, _, err := runEdgeRemovals(t, gql.URL); err != nil {
		t.Fatalf("node edge-removals: %v", err)
	}
	var vars struct {
		NodeRef string `json:"nodeRef"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(captured["NodeEdgeRemovals"], &vars); err != nil {
		t.Fatalf("variables: %v", err)
	}
	if vars.NodeRef != "n1" {
		t.Errorf("nodeRef = %q, want the resolved PK %q", vars.NodeRef, "n1")
	}
	if vars.Limit != 20 {
		t.Errorf("limit = %d with --limit unset, want the explicit default 20", vars.Limit)
	}
}

// --limit is passed through verbatim.
func TestNodeEdgeRemovalsExplicitLimit(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":       `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"NodeEdgeRemovals": edgeRemovalsTwoRows,
	})
	if _, _, err := runEdgeRemovals(t, gql.URL, "--limit", "3"); err != nil {
		t.Fatalf("node edge-removals: %v", err)
	}
	var vars struct {
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(captured["NodeEdgeRemovals"], &vars); err != nil {
		t.Fatalf("variables: %v", err)
	}
	if vars.Limit != 3 {
		t.Errorf("limit = %d, want 3", vars.Limit)
	}
}

// The JSON contract: exact DTO bytes, honest nulls for the withheld locs and
// the unattributed actor. Withheld locs are null, never "".
func TestNodeEdgeRemovalsJSONShape(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":       `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"NodeEdgeRemovals": edgeRemovalsTwoRows,
	})
	out, _, err := runEdgeRemovals(t, gql.URL, "--json")
	if err != nil {
		t.Fatalf("node edge-removals --json: %v", err)
	}
	want := `[
  {
    "id": "rem1",
    "createdAt": "2026-10-20T12:00:00Z",
    "edgeLoc": "src:depends-on:tgt",
    "edgeName": "depends_on",
    "reason": "TARGET_HARD_DELETED",
    "removedBy": "u1",
    "targetId": "tgt1",
    "targetLoc": "tgt"
  },
  {
    "id": "rem2",
    "createdAt": "2026-10-19T09:30:00Z",
    "edgeLoc": null,
    "edgeName": "cites",
    "reason": "TARGET_HARD_DELETED",
    "removedBy": null,
    "targetId": "tgt2",
    "targetLoc": null
  }
]
`
	if got := out.String(); got != want {
		t.Errorf("JSON shape changed:\n got %q\nwant %q", got, want)
	}
}

// An empty history is [], not null — the slice is initialized before the
// response is read.
func TestNodeEdgeRemovalsEmptyIsAnEmptyArray(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":       `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"NodeEdgeRemovals": `{"data":{"nodeEdgeRemovals":[]}}`,
	})
	out, _, err := runEdgeRemovals(t, gql.URL, "--json")
	if err != nil {
		t.Fatalf("node edge-removals --json: %v", err)
	}
	if got := out.String(); got != "[]\n" {
		t.Errorf("empty history = %q, want %q", got, "[]\n")
	}
}

// The table: withheld locs say so explicitly (never a blank cell), an absent
// actor renders the em-dash every other optional cell uses, and a
// control-carrying server value cannot forge a row (SafeCell).
func TestNodeEdgeRemovalsTableWithholdsAndSanitizes(t *testing.T) {
	attacked := `{"id":"rem3","createdAt":"2026-10-18T00:00:00Z",` +
		`"edgeLoc":null,"edgeName":"a\nb\u001b[31mc",` +
		`"reason":"TARGET_HARD_DELETED","removedBy":null,` +
		`"targetId":"tgt3","targetLoc":null}`
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":       `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"NodeEdgeRemovals": `{"data":{"nodeEdgeRemovals":[` + edgeRemovalFull + `,` + attacked + `]}}`,
	})
	out, _, err := runEdgeRemovals(t, gql.URL)
	if err != nil {
		t.Fatalf("node edge-removals: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"WHEN", "EDGE", "EDGE-NAME", "TARGET-ID", "TARGET-LOC", "REASON", "REMOVED-BY",
		"src:depends-on:tgt", "tgt", "depends_on", "u1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q:\n%s", want, got)
		}
	}
	withheld := strings.Count(got, "(withheld)")
	// Row 1 is fully readable; row 2 has both locs withheld.
	if withheld < 2 {
		t.Errorf("table shows %d \"(withheld)\" cells, want at least 2 (edgeLoc and targetLoc of the unreadable-target row):\n%s", withheld, got)
	}
	if !strings.Contains(got, "—") {
		t.Errorf("table missing the em-dash for the unattributed removedBy:\n%s", got)
	}
	if strings.Contains(got, "a\nb") || strings.Contains(got, "\x1b") {
		t.Errorf("control bytes from the server reached the table verbatim:\n%q", got)
	}
	for _, r := range got {
		if r != '\n' && unicode.IsControl(r) {
			t.Errorf("control rune %q in table output", r)
		}
	}
}

// A full default page is reported as "first 20; more may exist" on stderr —
// the query has no offset or total, so a full page cannot prove the end
// (same discipline as `node revision list`, cli#967).
func TestNodeEdgeRemovalsFullDefaultPageHints(t *testing.T) {
	rows := make([]string, 20)
	for i := range rows {
		rows[i] = `{"id":"rem` + itoa(i) + `","createdAt":"2026-10-01T00:00:00Z",` +
			`"edgeLoc":"e` + itoa(i) + `","edgeName":"n","reason":"TARGET_HARD_DELETED",` +
			`"removedBy":"u1","targetId":"t` + itoa(i) + `","targetLoc":"t"}`
	}
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":       `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"NodeEdgeRemovals": `{"data":{"nodeEdgeRemovals":[` + strings.Join(rows, ",") + `]}}`,
	})
	out, errOut, err := runEdgeRemovals(t, gql.URL)
	if err != nil {
		t.Fatalf("node edge-removals: %v", err)
	}
	// The table carries no entry-ID column (nothing addresses a removal by
	// id); the last row is visible by its edge loc.
	if !strings.Contains(out.String(), "e19") {
		t.Fatalf("expected all 20 rows rendered")
	}
	if !strings.Contains(errOut.String(), "Showing the first 20 edge-removal entries; more may exist") {
		t.Errorf("full default page without a hint on stderr (got %q)", errOut.String())
	}
}

// Usage errors: a negative limit, and a bare namespaced loc without -m (the
// same guidance the other node commands give).
func TestNodeEdgeRemovalsUsageErrors(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":       `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"NodeEdgeRemovals": edgeRemovalsTwoRows,
	})
	if _, _, err := runEdgeRemovals(t, gql.URL, "--limit", "-1"); exitCodeFor(err) != exitcode.Usage {
		t.Errorf("--limit -1: want exit %d (usage), got %d (%v)", exitcode.Usage, exitCodeFor(err), err)
	}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "edge-removals", "findings:flaky-ci", "--server", gql.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Usage {
		t.Errorf("bare loc without -m: want exit %d (usage), got %d (%v)", exitcode.Usage, exitCodeFor(err), err)
	}
}
