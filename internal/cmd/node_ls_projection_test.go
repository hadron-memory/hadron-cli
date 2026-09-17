package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// projNodeJSON is a node whose free-form envelope lives in `data` and whose
// `properties` is null — the shape of every chat message in this platform, and
// the shape the false zero was measured against (#602/#603).
const projNodeJSON = `{"id":"n1","memoryId":"mem1","loc":"chats:team:messages:001-abc-holger",
	"name":"Message from holger","nodeType":"chat-message","tags":[],"seq":1,
	"properties":null,"data":{"authorName":"holger","sessionId":"s1"},
	"updatedAt":"2026-06-11T00:00:00Z"}`

// findNodesProjectionVars decodes the two @include toggles (#602). They are
// `Boolean!`, so both are always on the wire — which is what lets a test assert
// that an unflagged listing explicitly asks for NEITHER column, rather than
// merely not mentioning them.
type findNodesProjectionVars struct {
	WithProperties *bool `json:"withProperties"`
	WithData       *bool `json:"withData"`
}

func decodeProjectionVars(t *testing.T, captured map[string]json.RawMessage) findNodesProjectionVars {
	t.Helper()
	raw, ok := captured["FindNodes"]
	if !ok {
		t.Fatalf("no FindNodes call was captured")
	}
	var v findNodesProjectionVars
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode variables: %v", err)
	}
	if v.WithProperties == nil || v.WithData == nil {
		t.Fatalf("both projection toggles must always be sent (Boolean!), got %s", raw)
	}
	return v
}

// The default listing must be unchanged, on the wire AND in the payload: it
// asks for neither column, and its JSON carries neither key. This is the
// contract half of #602 — the flags are additive, so an agent parsing today's
// `node ls --json` sees nothing new until it opts in.
func TestNodeLsWithoutProjectionOmitsBothColumns(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + projNodeJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls", "--memory", "acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := decodeProjectionVars(t, captured)
	if *vars.WithProperties || *vars.WithData {
		t.Errorf("an unflagged listing must request neither column, got properties=%v data=%v",
			*vars.WithProperties, *vars.WithData)
	}
	// Decoded, not substring-matched: the node's own `data` VALUE contains the
	// word "authorName", and a contains-check for "data" would also hit the
	// GraphQL envelope. Only the row's key set answers the question.
	rows := decodeRows(t, out.String())
	for _, key := range []string{"data", "properties"} {
		if _, present := rows[0][key]; present {
			t.Errorf("unprojected row must not carry %q: %s", key, out.String())
		}
	}
}

// --with-data selects data ALONE. The two toggles are independent so a caller
// after one envelope does not pay for the other column's payload.
func TestNodeLsWithDataSelectsOnlyData(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + projNodeJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls", "--memory", "acme.com::kb", "--with-data", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := decodeProjectionVars(t, captured)
	if !*vars.WithData {
		t.Error("--with-data must request the data column")
	}
	if *vars.WithProperties {
		t.Error("--with-data must NOT drag the properties column along")
	}
	rows := decodeRows(t, out.String())
	if _, present := rows[0]["properties"]; present {
		t.Errorf("properties was not requested and must be absent: %s", out.String())
	}
	got, present := rows[0]["data"]
	if !present {
		t.Fatalf("data was requested and must be present: %s", out.String())
	}
	if c := compact(t, got); c != `{"authorName":"holger","sessionId":"s1"}` {
		t.Errorf("data must arrive verbatim, got %s", c)
	}
}

func TestNodeLsWithPropertiesSelectsOnlyProperties(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + projNodeJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls", "--memory", "acme.com::kb", "--with-properties", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := decodeProjectionVars(t, captured)
	if !*vars.WithProperties || *vars.WithData {
		t.Errorf("--with-properties must request properties alone, got properties=%v data=%v",
			*vars.WithProperties, *vars.WithData)
	}
	if _, present := decodeRows(t, out.String())[0]["data"]; present {
		t.Errorf("data was not requested and must be absent: %s", out.String())
	}
}

// THE three-state case, and the one that corrected this design. A requested
// column that is null on the node must keep its key, carrying the JSON null —
// because an ABSENT key already means "you did not ask for it". Collapsing the
// two is #602's own defect (a zero you cannot interpret), and it is what a
// *json.RawMessage does: encoding/json sets it to nil for a JSON null, which is
// indistinguishable from an unselected field.
func TestNodeLsRequestedNullColumnKeepsItsKey(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + projNodeJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls", "--memory", "acme.com::kb",
		"--with-properties", "--with-data", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	row := decodeRows(t, out.String())[0]
	got, present := row["properties"]
	if !present {
		t.Fatalf("a REQUESTED column must keep its key even when null — absent reads as 'not requested': %s", out.String())
	}
	if string(got) != "null" {
		t.Errorf("properties is null on this node and must render as null, got %s", got)
	}
	// And the sibling column proves the null above is the node's value rather
	// than a projection that silently failed for both.
	if c := compact(t, row["data"]); c != `{"authorName":"holger","sessionId":"s1"}` {
		t.Errorf("data must still carry its value, got %s", c)
	}
}

// Text output must show what was asked for. A table would have to truncate an
// arbitrarily long JSON value, so either flag switches to a per-node block.
func TestNodeLsProjectedTextShowsTheColumns(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + projNodeJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls", "--memory", "acme.com::kb",
		"--with-properties", "--with-data", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"chats:team:messages:001-abc-holger",
		`data: {"authorName":"holger","sessionId":"s1"}`,
		"properties: null",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("projected text output missing %q:\n%s", want, got)
		}
	}
	// The block layout replaces the table, rather than printing both.
	if strings.Contains(got, "LOC") && strings.Contains(got, "RUN") {
		t.Errorf("projection must replace the table header, not print it too:\n%s", got)
	}
}

// Unprojected text output is untouched — still the table.
func TestNodeLsUnprojectedTextKeepsTheTable(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + projNodeJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls", "--memory", "acme.com::kb", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "LOC") || strings.Contains(got, "data:") {
		t.Errorf("unprojected text must stay the table:\n%s", got)
	}
}

// decodeRows parses `node ls --json` into per-row key sets. Decoding rather
// than substring-probing is the point: a row's own JSON VALUES contain the same
// words as its keys, so `strings.Contains(out, "data")` cannot answer whether
// the key is present — the question every projection test here asks.
func decodeRows(t *testing.T, payload string) []map[string]json.RawMessage {
	t.Helper()
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &rows); err != nil {
		t.Fatalf("decode --json payload: %v\n%s", err, payload)
	}
	if len(rows) == 0 {
		t.Fatalf("expected at least one row, got none:\n%s", payload)
	}
	return rows
}

// compact normalizes a projected column for comparison. `output.Write` INDENTS
// the --json payload, so a raw column comes back re-formatted rather than
// byte-identical to what the server sent — comparing the pretty bytes asserts
// the formatter, not the projection.
func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("compact %s: %v", raw, err)
	}
	return buf.String()
}
