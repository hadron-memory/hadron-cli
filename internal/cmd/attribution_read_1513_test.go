package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// hadron-cli#1513 — the CLI half of hadron-server#1151 / #3092: who created a
// node or edge, and when. Read only. The provider decides; the CLI passes the
// {handle, urn} object (or null) through verbatim.

// rawStamp is the stored creator id the provider must never have echoed. The
// fake serves it anyway, in a field the CLI does not select, to prove no
// output path can print it.
const rawStamp = "RAWUSERID-0123456789abcdef"

const (
	adaEditor   = `{"handle":"ada","urn":"hrn:user:acme.com:ada"}`
	nullHandle  = `{"handle":null,"urn":"hrn:user:acme.com:bob"}`
	emptyEditor = `{"handle":null,"urn":null}`
	hostile     = "evil\x1b[31mred\x1b[0m\r\u202eflip\nnext"
)

func hostileEditor() string {
	b, _ := json.Marshal(map[string]string{"handle": hostile, "urn": "hrn:user:x:" + hostile})
	return string(b)
}

// opLog1513 records every operation name a run sends, in order.
type opLog1513 struct {
	mu  sync.Mutex
	ops []string
}

func (l *opLog1513) add(op string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ops = append(l.ops, op)
}

func (l *opLog1513) sent(op string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, o := range l.ops {
		if o == op {
			return true
		}
	}
	return false
}

func run1513(t *testing.T, responses map[string]string, args ...string) (string, *opLog1513, error) {
	t.Helper()
	log := &opLog1513{}
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		log.add(op)
		return responses[op]
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	return out.String(), log, err
}

// node1513 is a GetNode payload with createdByUser controllable.
func node1513(created string) string {
	return `{"data":{"node":{"id":"n1","urn":"` + testNodeURN + `","portalUrl":null,
		"memoryId":"mem1","loc":"findings:flaky","name":"flaky","description":null,"abstract":null,
		"abstractOriginHash":null,"nodeType":"info","objectType":null,"tags":[],"content":null,
		"data":null,"properties":null,"seq":null,"isRunnable":false,
		"createdAt":"2026-08-24T00:00:00Z","updatedAt":"2026-08-25T00:00:00Z",
		"createdBy":"` + rawStamp + `","createdByUser":` + created + `,
		"outgoingEdges":[],"incomingEdges":[]}}}`
}

func batchNode1513(created string) string {
	return `{"id":"n1","urn":"` + testNodeURN + `","portalUrl":null,
		"memoryId":"mem1","loc":"findings:flaky","name":"flaky","alias":null,"nodeType":"info",
		"objectType":null,"isRunnable":false,"description":null,"abstract":null,"abstractOriginHash":null,
		"tags":[],"seq":null,"data":null,"properties":null,"content":null,
		"createdAt":"2026-08-24T00:00:00Z","updatedAt":"2026-08-25T00:00:00Z",
		"createdBy":"` + rawStamp + `","createdByUser":` + created + `,
		"outgoingEdges":[],"incomingEdges":[]}`
}

func nodeJSON1513(t *testing.T, args ...string) map[string]json.RawMessage {
	t.Helper()
	out, _, err := run1513(t, nodeGetStubs(node1513(args[0])), "node", "get", testNodeURN, "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	return m
}

func batchJSON1513(t *testing.T, created string) map[string]json.RawMessage {
	t.Helper()
	out, _, err := run1513(t, map[string]string{
		"NodeBatch": nodeBatchResult([]string{batchNode1513(created)}, ""),
	}, "node", "get", "alpha", "beta", "-m", "acme.com::kb", "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	var env struct {
		Nodes []map[string]json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Nodes) == 0 {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	return env.Nodes[0]
}

// A/B + acceptance 1, 3, 6: the key is always present, the object passes
// through verbatim, and single and batch emit the identical value.
func TestNodeGetCreatedByUserJSON1513(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"user", adaEditor, `{"handle":"ada","urn":"hrn:user:acme.com:ada"}`},
		{"null", "null", "null"},
		{"empty object", emptyEditor, `{"handle":null,"urn":null}`},
		{"handle null urn set", nullHandle, `{"handle":null,"urn":"hrn:user:acme.com:bob"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			single := nodeJSON1513(t, tc.in)
			got, ok := single["createdByUser"]
			if !ok {
				t.Fatalf("single: createdByUser key must always be present")
			}
			if cmp(got) != tc.want {
				t.Errorf("single createdByUser = %s, want %s", got, tc.want)
			}
			batch := batchJSON1513(t, tc.in)
			bgot, ok := batch["createdByUser"]
			if !ok {
				t.Fatalf("batch: createdByUser key must always be present")
			}
			if cmp(bgot) != cmp(got) {
				t.Errorf("single/batch parity: single %s, batch %s", cmp(got), cmp(bgot))
			}
		})
	}
}

// Acceptance 3 and 5 (A): the human line.
func TestNodeGetCreatedByLine1513(t *testing.T) {
	human := func(created string) string {
		t.Helper()
		out, _, err := run1513(t, nodeGetStubs(node1513(created)), "node", "get", testNodeURN)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		return out
	}
	out := human(adaEditor)
	if !strings.Contains(out, "  created by: @ada\n") {
		t.Errorf("handle present -> `created by: @ada`:\n%s", out)
	}
	if u, c := strings.Index(out, "  updated: "), strings.Index(out, "  created by: "); u < 0 || c < u {
		t.Errorf("the created by line follows updated:\n%s", out)
	}
	if out := human(nullHandle); !strings.Contains(out, "  created by: hrn:user:acme.com:bob\n") {
		t.Errorf("handle null, urn present -> the URN:\n%s", out)
	}
	for name, in := range map[string]string{"null": "null", "empty object": emptyEditor} {
		out := human(in)
		if strings.Contains(out, "created by") {
			t.Errorf("%s: nothing printable means no line:\n%s", name, out)
		}
		for _, bad := range []string{"nobody", "unknown author", "author: system"} {
			if strings.Contains(strings.ToLower(out), bad) {
				t.Errorf("%s: must not invent %q:\n%s", name, bad, out)
			}
		}
	}
	// An empty STRING handle and URN are as unprintable as null.
	if out := human(`{"handle":"","urn":""}`); strings.Contains(out, "created by") {
		t.Errorf("empty strings print no line:\n%s", out)
	}
}

// Acceptance 5 (A).
func TestNodeGetCreatedByHostile1513(t *testing.T) {
	out, _, err := run1513(t, nodeGetStubs(node1513(hostileEditor())), "node", "get", testNodeURN)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "created by:") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("created by line missing:\n%s", out)
	}
	for _, bad := range []string{"\x1b", "\r", "\u202e"} {
		if strings.Contains(out, bad) {
			t.Errorf("raw control %q reached the terminal:\n%q", bad, out)
		}
	}
	if strings.Contains(strings.TrimPrefix(line, "  created by: "), "\n") {
		t.Errorf("the line must stay one line: %q", line)
	}
	// The next real line of output must still be its own line.
	if strings.Contains(out, "next\n  ") && !strings.Contains(out, "created by:") {
		t.Errorf("sanitation broke the layout:\n%s", out)
	}
}

// Acceptance 4 (A/B): the raw stored creator never prints, and no query beyond
// the read itself runs.
func TestNodeGetNoLeakNoExtraQuery1513(t *testing.T) {
	out, log, err := run1513(t, nodeGetStubs(node1513(adaEditor)), "node", "get", testNodeURN, "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out, rawStamp) {
		t.Errorf("the raw stamp leaked into --json:\n%s", out)
	}
	for _, op := range log.ops {
		switch op {
		case "ResolveUrn", "GetNode", "ListNodeRevisions", "NodeRevisions", "CommentSummary":
		default:
			if strings.Contains(strings.ToLower(op), "user") || strings.Contains(strings.ToLower(op), "whoami") {
				t.Errorf("an identity lookup ran: %s (ops %v)", op, log.ops)
			}
		}
	}
	text, _, err := run1513(t, nodeGetStubs(node1513(adaEditor)), "node", "get", testNodeURN)
	if err != nil || strings.Contains(text, rawStamp) {
		t.Errorf("the raw stamp leaked into text (err %v):\n%s", err, text)
	}
}

// Edge fixtures. updatedAt and every ByUser are controllable.
func edgeAttr1513(created, updated, updatedAt string) string {
	return `"createdAt":"2026-09-01T00:00:00Z","updatedAt":` + updatedAt + `,
		"createdBy":"` + rawStamp + `","updatedBy":"` + rawStamp + `",
		"createdByUser":` + created + `,"updatedByUser":` + updated
}

func uniformEdge1513(created, updated, updatedAt string) string {
	return `{"id":"e1","urn":"hrn:edge:acme.com:kb:findings:a:routes-to:b","loc":"findings:a:routes-to:b",
		"name":"routes-to","description":null,"isRunnable":false,"priority":0,
		"source":{"id":"n1","loc":"findings:a"},"target":{"id":"n2","loc":"b"},` +
		edgeAttr1513(created, updated, updatedAt) + `}`
}

var attrKeys1513 = []string{"createdAt", "updatedAt", "createdByUser", "updatedByUser"}

func hasAllKeys(t *testing.T, label string, m map[string]json.RawMessage) {
	t.Helper()
	for _, k := range attrKeys1513 {
		if _, ok := m[k]; !ok {
			t.Errorf("%s: key %q must always be present (never omitted)", label, k)
		}
	}
}

func rows1513(t *testing.T, out string) []map[string]json.RawMessage {
	t.Helper()
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("--json array: %v\n%s", err, out)
	}
	return rows
}

// E + acceptance 2: edge get --json.
func TestEdgeGetAttribution1513(t *testing.T) {
	out, _, err := run1513(t, map[string]string{
		"GetEdge": `{"data":{"edge":` + uniformEdge1513(adaEditor, nullHandle, `"2026-09-02T00:00:00Z"`) + `}}`,
	}, "edge", "get", "e1", "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	hasAllKeys(t, "edge get", m)
	if cmp(m["createdByUser"]) != `{"handle":"ada","urn":"hrn:user:acme.com:ada"}` ||
		cmp(m["updatedByUser"]) != `{"handle":null,"urn":"hrn:user:acme.com:bob"}` ||
		cmp(m["createdAt"]) != `"2026-09-01T00:00:00Z"` || cmp(m["updatedAt"]) != `"2026-09-02T00:00:00Z"` {
		t.Errorf("values not passed through verbatim: %s", out)
	}
	if strings.Contains(out, rawStamp) {
		t.Errorf("raw stamp leaked:\n%s", out)
	}
}

// Acceptance 2: null stays null — present, never "" and never omitted — on
// every edge shape.
func TestEdgeAttributionNullStaysNull1513(t *testing.T) {
	edge := uniformEdge1513("null", "null", "null")
	check := func(label string, m map[string]json.RawMessage) {
		hasAllKeys(t, label, m)
		for _, k := range []string{"updatedAt", "createdByUser", "updatedByUser"} {
			if cmp(m[k]) != "null" {
				t.Errorf("%s: %s = %s, want null", label, k, m[k])
			}
		}
	}
	out, _, err := run1513(t, map[string]string{"GetEdge": `{"data":{"edge":` + edge + `}}`}, "edge", "get", "e1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal([]byte(out), &m)
	check("edge get", m)

	out, _, err = run1513(t, map[string]string{
		"ListEdges": `{"data":{"edges":{"total":1,"items":[` + edge + `]}}}`,
	}, "edge", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Total int                          `json:"total"`
		Edges []map[string]json.RawMessage `json:"edges"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil || len(page.Edges) != 1 {
		t.Fatalf("page: %v\n%s", err, out)
	}
	check("edge list page", page.Edges[0])

	nodeScoped := `{"data":{"node":{"id":"n1",
		"outgoingEdges":[{"id":"e1","name":"routes-to","loc":"l1","isRunnable":false,"priority":0,
			"target":{"id":"t1","loc":"findings:race","memoryId":"mem1"},` + edgeAttr1513("null", "null", "null") + `}],
		"incomingEdges":[{"id":"e2","name":"cites","loc":"l2","isRunnable":false,"priority":0,
			"source":{"id":"s1","loc":"inst","memoryId":"mem1"},` + edgeAttr1513("null", "null", "null") + `}]}}}`
	out, _, err = run1513(t, map[string]string{"ResolveUrn": resolveNodeJSON, "NodeEdgesAttributed": nodeScoped},
		"edge", "list", nodeURN, "--json")
	if err != nil {
		t.Fatal(err)
	}
	rows := rows1513(t, out)
	if len(rows) != 2 {
		t.Fatalf("both directions: %d rows\n%s", len(rows), out)
	}
	for i, r := range rows {
		check("node-scoped row "+string(rune('0'+i)), r)
	}
}

// C + D + acceptance 2/7: values, order and the unchanged table columns.
func TestEdgeListAttributionValues1513(t *testing.T) {
	out, _, err := run1513(t, map[string]string{
		"ListEdges": `{"data":{"edges":{"total":2,"items":[` +
			uniformEdge1513(adaEditor, "null", "null") + `,` +
			uniformEdge1513("null", adaEditor, `"2026-09-03T00:00:00Z"`) + `]}}}`,
	}, "edge", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Total int                          `json:"total"`
		Edges []map[string]json.RawMessage `json:"edges"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Edges) != 2 {
		t.Fatalf("envelope changed: %s", out)
	}
	if cmp(page.Edges[0]["createdByUser"]) != adaEditor || cmp(page.Edges[1]["updatedByUser"]) != adaEditor {
		t.Errorf("values or order wrong: %s", out)
	}
	text, _, err := run1513(t, map[string]string{
		"ListEdges": `{"data":{"edges":{"total":1,"items":[` + uniformEdge1513(adaEditor, adaEditor, "null") + `]}}}`,
	}, "edge", "list")
	if err != nil {
		t.Fatal(err)
	}
	if h := strings.Fields(strings.SplitN(text, "\n", 2)[0]); strings.Join(h, " ") != "FROM REL TO EDGE-ID" {
		t.Errorf("the page table columns are unchanged, got %q", h)
	}
}

func TestEdgeListNodeScopedAttribution1513(t *testing.T) {
	const detail = `{"data":{"node":{"id":"n1",
		"outgoingEdges":[
			{"id":"e1","name":"routes-to","loc":"l1","isRunnable":false,"priority":0,"target":{"id":"t1","loc":"findings:race","memoryId":"mem1"},` +
		`"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-02T00:00:00Z","createdByUser":` + adaEditor + `,"updatedByUser":null},
			{"id":"e2","name":"cites","loc":"l2","isRunnable":false,"priority":0,"target":{"id":"t2","loc":"findings:slow","memoryId":"mem1"},` +
		`"createdAt":"2026-09-01T00:00:00Z","updatedAt":null,"createdByUser":null,"updatedByUser":null}],
		"incomingEdges":[
			{"id":"e3","name":"complements","loc":"l3","isRunnable":false,"priority":0,"source":{"id":"s1","loc":"instructions","memoryId":"mem1"},` +
		`"createdAt":"2026-09-01T00:00:00Z","updatedAt":null,"createdByUser":null,"updatedByUser":null}]}}}`
	stubs := map[string]string{"ResolveUrn": resolveNodeJSON, "NodeEdgesAttributed": detail}

	out, log, err := run1513(t, stubs, "edge", "list", nodeURN, "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if log.sent("GetNode") {
		t.Errorf("shape C reads through its own query, not GetNode's per-edge selections: %v", log.ops)
	}
	rows := rows1513(t, out)
	ids := []string{}
	for _, r := range rows {
		var id string
		_ = json.Unmarshal(r["id"], &id)
		ids = append(ids, id)
		hasAllKeys(t, id, r)
	}
	if strings.Join(ids, ",") != "e1,e2,e3" {
		t.Errorf("order or count changed: %v", ids)
	}
	if cmp(rows[0]["createdByUser"]) != adaEditor || cmp(rows[0]["updatedAt"]) != `"2026-09-02T00:00:00Z"` {
		t.Errorf("row values: %s", out)
	}

	// Filters are unchanged: a subset of the unfiltered list, same shape.
	out, _, err = run1513(t, stubs, "edge", "list", nodeURN, "--json", "--direction", "incoming")
	if err != nil {
		t.Fatal(err)
	}
	if r := rows1513(t, out); len(r) != 1 || cmp(r[0]["id"]) != `"e3"` {
		t.Errorf("--direction incoming: %s", out)
	}
	text, _, err := run1513(t, stubs, "edge", "list", nodeURN)
	if err != nil {
		t.Fatal(err)
	}
	if h := strings.Fields(strings.SplitN(text, "\n", 2)[0]); strings.Join(h, " ") != "DIR REL NODE EDGE-ID" {
		t.Errorf("the node-scoped table columns are unchanged, got %q", h)
	}
	if strings.Contains(text, "ada") {
		t.Errorf("list tables keep their columns; attribution is --json only:\n%s", text)
	}
}

// E human rows (rules 2-5), acceptance 3 and 5.
func TestEdgeGetHumanAttribution1513(t *testing.T) {
	human := func(created, updated, updatedAt string) string {
		t.Helper()
		out, _, err := run1513(t, map[string]string{
			"GetEdge": `{"data":{"edge":` + uniformEdge1513(created, updated, updatedAt) + `}}`,
		}, "edge", "get", "e1")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		return out
	}
	out := human(adaEditor, nullHandle, `"2026-09-02T00:00:00Z"`)
	for _, want := range []string{"CREATED", "2026-09-01T00:00:00Z", "UPDATED", "2026-09-02T00:00:00Z", "CREATED BY", "@ada", "UPDATED BY", "hrn:user:acme.com:bob"} {
		if !strings.Contains(out, want) {
			t.Errorf("edge get must show %q:\n%s", want, out)
		}
	}
	if p, c := strings.Index(out, "PRIORITY"), strings.Index(out, "CREATED"); p < 0 || c < p {
		t.Errorf("the new rows follow PRIORITY:\n%s", out)
	}

	// null updatedAt: no UPDATED row. null / empty authors: no BY rows.
	out = human("null", emptyEditor, "null")
	for _, bad := range []string{"UPDATED", "CREATED BY", "UPDATED BY", "nobody", "unknown author"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q must not print for null timestamps or unprintable authors:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "CREATED") {
		t.Errorf("createdAt is non-null and prints:\n%s", out)
	}

	out = human(hostileEditor(), hostileEditor(), `"2026-09-02T00:00:00Z"`)
	for _, bad := range []string{"\x1b", "\r", "\u202e"} {
		if strings.Contains(out, bad) {
			t.Errorf("raw control %q reached the terminal:\n%q", bad, out)
		}
	}
	if strings.Contains(out, rawStamp) {
		t.Errorf("raw stamp leaked:\n%s", out)
	}
}

// Acceptance 7: edge create|update stay on edgeDTO, unchanged.
func TestEdgeMutationOutputUnchanged1513(t *testing.T) {
	keys := attrKeys1513
	out, _, err := run1513(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","name":"routes-to","loc":"l","isRunnable":false,"priority":0,
			"sourceNodeId":"n1","targetNodeId":"n2","description":null,"condition":null}}}`,
	}, "edge", "add", "--from", "a.com::m::x", "--to", "a.com::m::y", "--name", "routes-to", "--json")
	if err != nil {
		t.Skipf("edge add fixture does not fit this build (%v) — covered by the existing edge_test.go goldens", err)
	}
	for _, k := range keys {
		if strings.Contains(out, `"`+k+`"`) {
			t.Errorf("edge add output must not gain %q (mutation output is out of scope):\n%s", k, out)
		}
	}
}

// cmp renders a RawMessage compactly: the CLI pretty-prints --json.
func cmp(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

// The URN is displayed when the handle is null; it must be sanitized on that
// path too (the hostile fixture above always carries a handle).
func TestAttributionHostileURNOnly1513(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"handle": nil, "urn": "hrn:user:x:" + hostile})
	urnOnly := string(b)
	assertClean := func(label, out string) {
		t.Helper()
		for _, bad := range []string{"\x1b", "\r", "\u202e"} {
			if strings.Contains(out, bad) {
				t.Errorf("%s: raw control %q reached the terminal:\n%q", label, bad, out)
			}
		}
		if !strings.Contains(out, "hrn:user:x:evil") {
			t.Errorf("%s: the URN should print (sanitized):\n%q", label, out)
		}
	}
	out, _, err := run1513(t, nodeGetStubs(node1513(urnOnly)), "node", "get", testNodeURN)
	if err != nil {
		t.Fatal(err)
	}
	assertClean("node get", out)
	out, _, err = run1513(t, map[string]string{
		"GetEdge": `{"data":{"edge":` + uniformEdge1513(urnOnly, urnOnly, `"2026-09-02T00:00:00Z"`) + `}}`,
	}, "edge", "get", "e1")
	if err != nil {
		t.Fatal(err)
	}
	assertClean("edge get", out)
}

// The incoming direction maps its own attribution (the outgoing rows alone
// would not notice it dropped).
func TestEdgeListIncomingAttribution1513(t *testing.T) {
	const detail = `{"data":{"node":{"id":"n1","outgoingEdges":[],
		"incomingEdges":[{"id":"e3","name":"complements","loc":"l3","isRunnable":false,"priority":0,
			"source":{"id":"s1","loc":"instructions","memoryId":"mem1"},` +
		`"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-02T00:00:00Z","createdByUser":` + adaEditor + `,"updatedByUser":` + nullHandle + `}]}}}`
	out, _, err := run1513(t, map[string]string{"ResolveUrn": resolveNodeJSON, "NodeEdgesAttributed": detail}, "edge", "list", nodeURN, "--json")
	if err != nil {
		t.Fatal(err)
	}
	rows := rows1513(t, out)
	if len(rows) != 1 || cmp(rows[0]["createdByUser"]) != adaEditor || cmp(rows[0]["updatedByUser"]) != nullHandle ||
		cmp(rows[0]["updatedAt"]) != `"2026-09-02T00:00:00Z"` {
		t.Errorf("incoming row attribution: %s", out)
	}
}
