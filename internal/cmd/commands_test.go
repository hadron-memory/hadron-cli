package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureGraphQL is like fakeGraphQL but also records request bodies
// per operation so tests can assert on variables.
func captureGraphQL(t *testing.T, responses map[string]string) (*httptest.Server, map[string]json.RawMessage) {
	t.Helper()
	captured := map[string]json.RawMessage{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured[body.OperationName] = body.Variables
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(translateFindNodes(body.OperationName, resp)))
	}))
	t.Cleanup(server.Close)
	return server, captured
}

const nodeJSON = `{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI",
	"nodeType":"finding","tags":["ci"],"updatedAt":"2026-06-11T00:00:00Z"}`

// findNodesVars is the decoded request-variable shape of a FindNodes call — the
// unified field the CLI now sends in place of the old positional `nodes` args.
// The old memory/prefix/nodeType/isRunnable/tags/search knobs now live under
// `filter` (+ `query`/`mode` for a ranked search), so the fake-server variable
// assertions decode into this.
type findNodesVars struct {
	Query  *string `json:"query"`
	Mode   *string `json:"mode"`
	Sort   *string `json:"sort"`
	Limit  *int    `json:"limit"`
	Offset *int    `json:"offset"`
	Filter struct {
		MemoryIds  []string `json:"memoryIds"`
		LocPrefix  string   `json:"locPrefix"`
		NodeType   string   `json:"nodeType"`
		Tags       []string `json:"tags"`
		IsRunnable *bool    `json:"isRunnable"`
	} `json:"filter"`
}

const nodeDetailJSON = `{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI",
	"description":null,"abstract":null,"nodeType":"finding","tags":["ci"],
	"content":"The CI is flaky because...","seq":null,
	"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",
	"outgoingEdges":[{"id":"e1","name":"routes-to","loc":"findings:flaky-ci:routes-to:start-here","isRunnable":false,"priority":0,
		"target":{"id":"n2","loc":"start-here","memoryId":"mem1"}}],
	"incomingEdges":[]}`

const resolveNodeJSON = `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`

// resolveNullJSON is the "no such node" resolveUrn reply. Imports probe
// existence to gate an overwrite (#129); a write test that isn't exercising the
// gate mocks this so the probe classifies the target as new and the write runs.
const resolveNullJSON = `{"data":{"resolveUrn":null}}`

const nodeURN = "acme.com::kb::findings:flaky-ci"

func TestNodeLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + nodeJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls", "--memory", "acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "findings:flaky-ci") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars findNodesVars
	_ = json.Unmarshal(captured["FindNodes"], &vars)
	if len(vars.Filter.MemoryIds) != 1 || vars.Filter.MemoryIds[0] != "acme.com::kb" {
		t.Errorf("--memory should map to filter.memoryIds, got %v", vars.Filter.MemoryIds)
	}
}

func TestNodeGet(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", nodeURN, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "The CI is flaky") {
		t.Errorf("content missing from output: %s", out.String())
	}
	if !strings.Contains(out.String(), "routes-to") {
		t.Errorf("edges missing from output: %s", out.String())
	}
	var vars struct {
		Urn string `json:"urn"`
	}
	_ = json.Unmarshal(captured["ResolveUrn"], &vars)
	if vars.Urn != "hrn:node:"+nodeURN {
		t.Errorf("resolveUrn must receive the hrn:node:-prefixed URN, got %q", vars.Urn)
	}
}

// A node ref that already carries a scheme prefix passes through to
// resolveUrn verbatim — hrn: is canonical, but legacy urn: is accepted
// forever (issue #239). Only a bare ref gets the canonical hrn:node:
// prefix prepended (covered by TestNodeGet above).
func TestNodeGetPrefixPassthrough(t *testing.T) {
	for _, prefixed := range []string{
		"hrn:node:" + nodeURN,
		"urn:node:" + nodeURN,
	} {
		t.Run(prefixed, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"ResolveUrn": resolveNodeJSON,
				"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "get", prefixed, "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var vars struct {
				Urn string `json:"urn"`
			}
			_ = json.Unmarshal(captured["ResolveUrn"], &vars)
			if vars.Urn != prefixed {
				t.Errorf("a scheme-prefixed ref must reach resolveUrn verbatim, got %q want %q", vars.Urn, prefixed)
			}
		})
	}
}

// A single-colon `-m acme.com:kb` must normalize so the composed node URN is
// the valid double-colon `acme.com::kb::<loc>` (not the 3-colon form the strict
// grammar rejects) — #38/#138.
func TestNodeGetSingleColonMemoryNormalizes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "findings:flaky-ci", "-m", "acme.com:kb", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Urn string `json:"urn"`
	}
	_ = json.Unmarshal(captured["ResolveUrn"], &vars)
	if vars.Urn != "hrn:node:acme.com::kb::findings:flaky-ci" {
		t.Errorf("single-colon -m should compose the canonical URN, got %q", vars.Urn)
	}
}

func TestNodeGetRejectsBareLoc(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "findings:flaky-ci", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "fully-qualified") {
		t.Fatalf("expected fully-qualified URN usage error, got %v", err)
	}
}

// A single-colon full ref whose loc itself carries colons has ≥4 colons but zero
// `::`, so it must still be rejected as the ambiguous form — not passed to the
// server's legacy parser (Codex #147 P2).
func TestNodeGetRejectsSingleColonMultiColonLoc(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "acme.com:kb:services:secureid:user-reporting", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "fully-qualified") {
		t.Fatalf("single-colon multi-colon-loc ref must be rejected as ambiguous, got %v", err)
	}
}

func TestNodeGetWrongKindIsUsageError(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"m1","kind":"memory","memoryId":null}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "acme.com::kb::whatever", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not a node") {
		t.Fatalf("expected wrong-kind usage error, got %v", err)
	}
}

func TestNodeGetNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "acme.com::kb::nope", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestNodeAddUsesCreateNode(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "findings:flaky-ci",
		"--name", "Flaky CI", "--content", "body", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &vars)
	if vars.Input["content"] != "body" {
		t.Errorf("content not sent: %v", vars.Input)
	}
	// Create-only is intrinsic to the mutation now — the retired NodeInput
	// createOnly flag must not linger on the wire.
	if _, present := vars.Input["createOnly"]; present {
		t.Errorf("createNode input must not carry createOnly, got %v", vars.Input)
	}
}

// An invalid --loc (space) is rejected client-side, before any network call —
// CreateNode must never be reached (issue #189).
func TestNodeAddRejectsInvalidLoc(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "bad loc", "--name", "X", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error for a loc with a space")
	}
	if _, ok := captured["CreateNode"]; ok {
		t.Error("CreateNode should not be called when --loc is invalid")
	}
}

func TestNodeUpdatePreservesUnsetFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN,
		"--name", "Flaky CI (resolved)", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpdateNode"], &vars)
	if vars.Input["name"] != "Flaky CI (resolved)" {
		t.Errorf("name not updated: %v", vars.Input)
	}
	if vars.Input["id"] != "n1" {
		t.Errorf("update must target the resolved node id: %v", vars.Input)
	}
	// Unset optional fields must be OMITTED (server: omitted = preserve,
	// null/[] clears). `tags` is in this list deliberately: a
	// defaulted-empty --tag serialized as tags:[] clears the node's tags
	// server-side (#37). memoryId/loc are the alternate selector — XOR
	// with id — so they must stay off the wire too.
	for _, key := range []string{"content", "description", "abstract", "data", "nodeType", "tags", "memoryId", "loc"} {
		if _, present := vars.Input[key]; present {
			t.Errorf("unset field %q must be omitted from update input, got %v", key, vars.Input[key])
		}
	}
}

// hadron-cli#37 regression: a content-only update must NOT send `tags` on
// the wire. The server reads an omitted `tags` as "preserve" but an
// explicit `tags: []` as "clear" — so a defaulted-empty --tag would
// silently strip a node's tags, knocking spec nodes out of the corpus.
// The changed("tag") gate in `node update` keeps the field off the wire;
// this locks that gate (the server-side preserve contract now lives in
// hadron-server's updateNode resolver — omitted tags are preserved, #235).
func TestNodeUpdateContentOnlyOmitsTags(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN,
		"--content", "revised body", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(captured["UpdateNode"], &vars); err != nil {
		t.Fatalf("unmarshal UpdateNode variables: %v", err)
	}
	if vars.Input["content"] != "revised body" {
		t.Errorf("content not sent: %v", vars.Input)
	}
	if v, present := vars.Input["tags"]; present {
		t.Errorf("unset --tag must be omitted from update input, got tags=%v", v)
	}
}

func TestNodeUpdateClearsFieldWithEmptyString(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN,
		"--description", "", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpdateNode"], &vars)
	// An explicitly-passed empty string must be SENT (the server
	// normalizes it to null and clears the field), not omitted.
	if v, present := vars.Input["description"]; !present || v != "" {
		t.Errorf("explicit --description \"\" must send an empty string, got %v (present=%v)", v, present)
	}
}

// #38/#41: a paragraph abstract (backticks, newlines) is hostile to inline
// shell quoting, so --abstract-file reads it from a file.
func TestNodeUpdateAbstractFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abstract.md")
	const abstract = "A new abstract mentioning `info` nodes.\n"
	if err := os.WriteFile(path, []byte(abstract), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN, "--abstract-file", path, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpdateNode"], &vars)
	if vars.Input["abstract"] != abstract {
		t.Errorf("abstract should come from --abstract-file, got %v", vars.Input["abstract"])
	}
}

// Content and abstract can't both read stdin — the guard fires before any
// network round-trip.
func TestNodeUpdateRejectsDualStdin(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN, "--content", "-", "--abstract", "-", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("expected a dual-stdin usage error, got %v", err)
	}
}

// #45 review: an explicit --abstract "" (clear) together with --abstract-file
// must error, not silently let the file win. The guard is on Changed(), so it
// catches the empty-value case the resolver's value check would miss — and
// fires before any network round-trip.
func TestNodeUpdateRejectsAbstractAndAbstractFile(t *testing.T) {
	for _, abstract := range []string{"", "inline text"} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "update", nodeURN, "--abstract", abstract, "--abstract-file", "/tmp/x.md", "--server", "http://127.0.0.1:1"})
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("--abstract %q + --abstract-file should be a mutual-exclusion error, got %v", abstract, err)
		}
	}
}

// #69 item 4: --data forwards a parsed JSON object on the create input.
func TestNodeAddSendsData(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "palette:brand",
		"--name", "Brand palette", "--data", `{"primary":"#0a0"}`, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &vars)
	data, ok := vars.Input["data"].(map[string]any)
	if !ok || data["primary"] != "#0a0" {
		t.Errorf("--data must forward a parsed JSON object, got %v", vars.Input["data"])
	}
}

// --data-file reads the JSON from a file (structured data dodges shell quoting).
func TestNodeUpdateDataFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := os.WriteFile(path, []byte("{\n  \"swatches\": 3\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN, "--data-file", path, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpdateNode"], &vars)
	data, ok := vars.Input["data"].(map[string]any)
	if !ok || data["swatches"] != float64(3) {
		t.Errorf("--data-file must forward parsed JSON, got %v", vars.Input["data"])
	}
}

// Invalid JSON is rejected before any mutation (exit 2). `node add` reaches
// resolveData without a network round-trip, so no server is needed.
func TestNodeAddRejectsInvalidData(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "x", "--name", "X",
		"--data", "{not json}", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("expected invalid-JSON usage error, got %v", err)
	}
}

func TestNodeAddRejectsDataAndDataFile(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "x", "--name", "X",
		"--data", "{}", "--data-file", "/tmp/x.json", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

// #92: --data-merge calls the updateNodeData mutation (a shallow merge),
// forwarding the resolved node id and the JSON patch — NOT an upsert.
func TestNodeUpdateDataMerge(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":     resolveNodeJSON,
		"UpdateNodeData": `{"data":{"updateNodeData":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN,
		"--data-merge", `{"status":"closed"}`, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, updated := captured["UpdateNode"]; updated {
		t.Errorf("a merge-only update must not call UpdateNode")
	}
	// A merge-only update needs just the id, so it resolves the ref without
	// the extra GetNodeById round-trip (captureGraphQL flags unexpected ops).
	if _, fetched := captured["GetNode"]; fetched {
		t.Errorf("a merge-only update must not call GetNodeById")
	}
	var vars struct {
		NodeRef string         `json:"nodeRef"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(captured["UpdateNodeData"], &vars); err != nil {
		t.Fatalf("unmarshal UpdateNodeData variables: %v", err)
	}
	if vars.NodeRef != "n1" {
		t.Errorf("nodeRef must be the resolved id, got %q", vars.NodeRef)
	}
	if vars.Data["status"] != "closed" {
		t.Errorf("--data-merge must forward the parsed patch, got %v", vars.Data)
	}
}

// --data-merge-file reads the JSON patch from a file ("-" reads stdin).
func TestNodeUpdateDataMergeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patch.json")
	if err := os.WriteFile(path, []byte("{\n  \"swatches\": 3\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":     resolveNodeJSON,
		"GetNode":        `{"data":{"node":` + nodeDetailJSON + `}}`,
		"UpdateNodeData": `{"data":{"updateNodeData":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN, "--data-merge-file", path, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(captured["UpdateNodeData"], &vars)
	if vars.Data["swatches"] != float64(3) {
		t.Errorf("--data-merge-file must forward parsed JSON, got %v", vars.Data)
	}
}

// #89: node get surfaces isRunnable in the text view and --json.
func TestNodeGetSurfacesRunnable(t *testing.T) {
	const detailRunnable = `{"id":"n1","memoryId":"mem1","loc":"tasks:brief","name":"Brief",
		"description":null,"abstract":null,"nodeType":"task","tags":[],
		"content":null,"data":null,"seq":null,"isRunnable":true,
		"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}`
	for _, jsonMode := range []bool{false, true} {
		gql, _ := captureGraphQL(t, map[string]string{
			"ResolveUrn": resolveNodeJSON,
			"GetNode":    `{"data":{"node":` + detailRunnable + `}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"node", "get", nodeURN, "--server", gql.URL}
		if jsonMode {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("execute (json=%v): %v", jsonMode, err)
		}
		if jsonMode {
			var got struct {
				IsRunnable bool `json:"isRunnable"`
			}
			if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
				t.Fatalf("unmarshal node get --json: %v", err)
			}
			if !got.IsRunnable {
				t.Errorf("--json must surface isRunnable:true, got %s", out.String())
			}
		} else if !strings.Contains(out.String(), "runnable: true") {
			t.Errorf("text view must surface runnable, got %s", out.String())
		}
	}
}

// #89: node list --json surfaces isRunnable per node.
func TestNodeLsSurfacesRunnable(t *testing.T) {
	const runnableJSON = `{"id":"n2","memoryId":"mem1","loc":"tasks:brief","name":"Brief",
		"nodeType":"task","tags":[],"seq":null,"isRunnable":true,"updatedAt":"2026-06-11T00:00:00Z"}`
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + runnableJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls", "--memory", "acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got []struct {
		IsRunnable bool `json:"isRunnable"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("unmarshal node ls --json: %v", err)
	}
	if len(got) != 1 || !got[0].IsRunnable {
		t.Errorf("node ls --json must surface isRunnable, got %s", out.String())
	}
}

// #89 follow-up: `node ls --runnable[=false]` filters server-side via
// findNodes filter.isRunnable. Omitting the flag must NOT reach the wire
// (server reads absent as "any runnable status"), so a default-false bool
// doesn't silently hide most nodes.
func TestNodeLsRunnableFilter(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want any // nil = must be omitted from the wire
	}{
		{"omitted", nil, nil},
		{"true", []string{"--runnable"}, true},
		{"explicit-true", []string{"--runnable=true"}, true},
		{"false", []string{"--runnable=false"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"FindNodes": `{"data":{"nodes":[` + nodeJSON + `]}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"node", "ls", "-m", "acme.com:kb", "--server", gql.URL}, tc.args...))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var vars struct {
				Filter map[string]any `json:"filter"`
			}
			_ = json.Unmarshal(captured["FindNodes"], &vars)
			got, present := vars.Filter["isRunnable"]
			if tc.want == nil {
				if present {
					t.Errorf("omitted --runnable must not send filter.isRunnable, got %v", got)
				}
				return
			}
			if !present || got != tc.want {
				t.Errorf("filter.isRunnable = %v (present=%v), want %v", got, present, tc.want)
			}
		})
	}
}

// #89: --runnable is a tri-state — set true, set false, or (omitted) preserve.
// When omitted, isRunnable must NOT reach the wire (server reads absent as
// "preserve"); when passed, the chosen value is sent.
func TestNodeUpdateRunnable(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want any // nil = field must be absent
	}{
		{"omitted preserves", []string{"--name", "X"}, nil},
		{"set true", []string{"--runnable"}, true},
		{"set false", []string{"--runnable=false"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"ResolveUrn": resolveNodeJSON,
				"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"node", "update", nodeURN, "--server", gql.URL}, tc.args...))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var vars struct {
				Input map[string]any `json:"input"`
			}
			_ = json.Unmarshal(captured["UpdateNode"], &vars)
			got, present := vars.Input["isRunnable"]
			if tc.want == nil {
				if present {
					t.Errorf("omitted --runnable must not send isRunnable, got %v", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("isRunnable = %v (present=%v), want %v", got, present, tc.want)
			}
		})
	}
}

// #89: node create --runnable forwards isRunnable on the create input.
func TestNodeAddRunnable(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "tasks:brief",
		"--name", "Brief", "--type", "task", "--runnable", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &vars)
	if vars.Input["isRunnable"] != true {
		t.Errorf("--runnable must forward isRunnable:true, got %v", vars.Input["isRunnable"])
	}
}

// #88: --reason rides on the update and is recorded in version history.
func TestNodeUpdateForwardsReason(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN, "--type", "task",
		"--reason", "restore task type", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpdateNode"], &vars)
	if vars.Input["reason"] != "restore task type" {
		t.Errorf("--reason must forward to update input.reason, got %v", vars.Input["reason"])
	}
}

// #88: --reason also rides on the data-merge mutation (updateNodeData).
func TestNodeUpdateDataMergeForwardsReason(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":     resolveNodeJSON,
		"UpdateNodeData": `{"data":{"updateNodeData":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN,
		"--data-merge", `{"a":1}`, "--reason", "tweak", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(captured["UpdateNodeData"], &vars)
	if vars.Reason != "tweak" {
		t.Errorf("--reason must forward to updateNodeData reason, got %q", vars.Reason)
	}
}

// #88: an omitted — or blank/whitespace-only — --reason must not reach the
// wire, else it would override the server's caller-identity editedBy fallback
// with an empty rationale.
func TestNodeUpdateOmitsReasonWhenUnset(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"omitted", []string{"node", "update", nodeURN, "--type", "task"}},
		{"empty", []string{"node", "update", nodeURN, "--type", "task", "--reason", ""}},
		{"whitespace", []string{"node", "update", nodeURN, "--type", "task", "--reason", "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"ResolveUrn": resolveNodeJSON,
				"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var vars struct {
				Input map[string]any `json:"input"`
			}
			_ = json.Unmarshal(captured["UpdateNode"], &vars)
			if v, present := vars.Input["reason"]; present {
				t.Errorf("a blank --reason must be left off the wire, got %v", v)
			}
		})
	}
}

// Replace and merge are different mutations; passing both is a usage error
// that fires before any network round-trip.
func TestNodeUpdateRejectsDataAndDataMerge(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN,
		"--data", "{}", "--data-merge", "{}", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("--data + --data-merge should be a mutual-exclusion error, got %v", err)
	}
}

// --data-merge and --data-merge-file are mutually exclusive. The guard is on
// Changed(), so an explicit --data-merge "" (which the value check would miss)
// still errors instead of letting the file silently win — and fires before any
// network round-trip.
func TestNodeUpdateRejectsDataMergeAndDataMergeFile(t *testing.T) {
	for _, patch := range []string{"", `{"a":1}`} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "update", nodeURN,
			"--data-merge", patch, "--data-merge-file", "/tmp/x.json", "--server", "http://127.0.0.1:1"})
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("--data-merge %q + --data-merge-file should be a mutual-exclusion error, got %v", patch, err)
		}
	}
}

// An invalid --data-merge patch is rejected before the mutation. resolveMergeData
// runs after the node fetch, so the resolve/fetch responses must be stubbed.
func TestNodeUpdateRejectsInvalidDataMerge(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN, "--data-merge", "{not json}", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("expected invalid-JSON usage error, got %v", err)
	}
}

// #69 item 5: node get surfaces `data` in both the text view and --json.
func TestNodeGetSurfacesData(t *testing.T) {
	const detailWithData = `{"id":"n1","memoryId":"mem1","loc":"palette:brand","name":"Brand",
		"description":null,"abstract":null,"nodeType":"data","tags":[],
		"content":null,"data":{"primary":"#0a0"},"seq":null,
		"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}`
	for _, jsonMode := range []bool{false, true} {
		gql, _ := captureGraphQL(t, map[string]string{
			"ResolveUrn": resolveNodeJSON,
			"GetNode":    `{"data":{"node":` + detailWithData + `}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"node", "get", "acme.com::kb::palette:brand", "--server", gql.URL}
		if jsonMode {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("execute (json=%v): %v", jsonMode, err)
		}
		if !strings.Contains(out.String(), "#0a0") {
			t.Errorf("data not surfaced (json=%v): %s", jsonMode, out.String())
		}
		if !jsonMode && !strings.Contains(out.String(), "data:") {
			t.Errorf("text view missing the data line: %s", out.String())
		}
	}
}

func TestNodeRmRequiresYesNonInteractive(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "rm", nodeURN, "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestNodeRmWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
		"DeleteNode": `{"data":{"deleteNode":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "rm", nodeURN, "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteNode"], &vars)
	// #542: deleteNode takes a single nodeRef — the resolved node's PK.
	if vars["nodeRef"] != "n1" {
		t.Errorf("delete arg must be the fetched node's id: %+v", vars)
	}
	// A default (soft) delete must not send hard at all — an explicit hard:null
	// or hard:false would be wrong wire shape for "soft".
	if v, present := vars["hard"]; present {
		t.Errorf("soft delete must omit hard, got %v", v)
	}
}

func TestNodeRmHard(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
		"DeleteNode": `{"data":{"deleteNode":true}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "rm", nodeURN, "--hard", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteNode"], &vars)
	if vars["hard"] != true {
		t.Errorf("--hard must send hard:true, got %v", vars["hard"])
	}
	// The --json status distinguishes a hard delete from a soft one.
	if !strings.Contains(out.String(), "hard-deleted") {
		t.Errorf("--json status should be hard-deleted, got %s", out.String())
	}
}

// Default (render) mode: resolve the ref to a nodeRef, omit appRef, and print
// the compiled prompt the server returns.
func TestTaskRunRendersPrompt(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"RunTask":    `{"data":{"runTask":"compiled prompt"}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"task", "run", nodeURN, "--arg", "chat_slug=demo", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RunTask"], &vars)
	if vars["nodeRef"] != "n1" {
		t.Errorf("URN must resolve to a node ref passed as nodeRef, got %v", vars["nodeRef"])
	}
	if v, present := vars["appRef"]; present {
		t.Errorf("render mode must omit appRef, got %v", v)
	}
	if a, _ := vars["args"].(map[string]any); a["chat_slug"] != "demo" {
		t.Errorf("--arg should build the args object, got %v", vars["args"])
	}
	if !strings.Contains(out.String(), "compiled prompt") {
		t.Errorf("rendered prompt should be printed, got %s", out.String())
	}
}

// --app switches to execute mode: send appRef and treat the returned string as
// a run id (printed, and surfaced as runId/mode in --json).
func TestTaskRunExecuteMode(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"RunTask":    `{"data":{"runTask":"run_abc"}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"task", "run", nodeURN, "--app", "acme.com:ops", "--as-self", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RunTask"], &vars)
	if vars["appRef"] != "acme.com:ops" {
		t.Errorf("--app should map to appRef, got %v", vars["appRef"])
	}
	if vars["runAsSelf"] != true {
		t.Errorf("--as-self should send runAsSelf:true, got %v", vars["runAsSelf"])
	}
	var dto struct {
		Mode  string `json:"mode"`
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json not valid: %v\n%s", err, out.String())
	}
	if dto.Mode != "execute" || dto.RunID != "run_abc" {
		t.Errorf("execute mode should report runId, got %+v", dto)
	}
}

// --as-self is meaningless without --app and must be rejected, not silently
// dropped.
func TestTaskRunAsSelfRequiresApp(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"task", "run", nodeURN, "--as-self", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--as-self requires --app") {
		t.Fatalf("expected --as-self/--app usage error, got %v", err)
	}
}

func TestEdgeAddOmitsUnsetOptionals(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"CreateEdge": `{"data":{"createEdge":` + edgeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "add", "--from", nodeURN, "--to", nodeURN,
		"--name", "routes-to", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateEdge"], &vars)
	if vars["name"] != "routes-to" {
		t.Errorf("name not sent: %v", vars)
	}
	// Unset optionals must be OMITTED, not sent as explicit nulls — the
	// server rejects priority: null (hadron-server#263).
	for _, key := range []string{"priority", "condition", "data"} {
		if v, present := vars[key]; present {
			t.Errorf("unset %q must be omitted from createEdge variables, got %v", key, v)
		}
	}
}

// #69 item 3: `node get <loc> -m <memory>` resolves the same URN the
// fully-qualified form does (cf. TestNodeGet).
func TestNodeGetMemoryFlag(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "findings:flaky-ci", "-m", "acme.com::kb", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Urn string `json:"urn"`
	}
	_ = json.Unmarshal(captured["ResolveUrn"], &vars)
	if vars.Urn != "hrn:node:acme.com::kb::findings:flaky-ci" {
		t.Errorf("-m <memory> + bare loc should resolve the full URN, got %q", vars.Urn)
	}
}

// A loc can itself contain colons; with -m the whole positional is the bare
// loc and the memory is prepended verbatim. (A heuristic that skipped the join
// for refs with ">=2 colons" would misparse this as a full URN and drop -m.)
func TestNodeGetMemoryFlagMultiColonLoc(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "cor:acl:010:01", "-m", "hadronmemory.com::specs", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Urn string `json:"urn"`
	}
	_ = json.Unmarshal(captured["ResolveUrn"], &vars)
	if vars.Urn != "hrn:node:hadronmemory.com::specs::cor:acl:010:01" {
		t.Errorf("a multi-colon loc with -m must join verbatim, got %q", vars.Urn)
	}
}

// edge add -m resolves both endpoints as bare locs in that one memory.
func TestEdgeAddMemoryFlag(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"CreateEdge": `{"data":{"createEdge":` + edgeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "add", "-m", "acme.com::kb",
		"--from", "findings:flaky-ci", "--to", "start-here", "--name", "routes-to", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The capture keeps the last ResolveUrn — the --to endpoint, joined onto -m.
	var vars struct {
		Urn string `json:"urn"`
	}
	_ = json.Unmarshal(captured["ResolveUrn"], &vars)
	if vars.Urn != "hrn:node:acme.com::kb::start-here" {
		t.Errorf("edge add -m should resolve --to as a bare loc in the memory, got %q", vars.Urn)
	}
}

func TestEdgeUpdateLabelOnlyPreservesCondition(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateEdge": `{"data":{"updateEdge":` + edgeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "update", "e1", "--name", "complements", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateEdge"], &vars)
	if vars["edgeId"] != "e1" || vars["name"] != "complements" {
		t.Errorf("unexpected update vars: %v", vars)
	}
	// An explicit null clears condition/data server-side, so a
	// label-only update must omit them entirely.
	for _, key := range []string{"priority", "condition", "data"} {
		if v, present := vars[key]; present {
			t.Errorf("unset %q must be omitted from updateEdge variables, got %v", key, v)
		}
	}
}

func TestEdgeUpdateExplicitNullClearsCondition(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateEdge": `{"data":{"updateEdge":` + edgeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "update", "e1", "--condition", "null", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateEdge"], &vars)
	// --condition null is the explicit clear: it must be SENT as a
	// JSON null, not dropped by omitempty.
	if v, present := vars["condition"]; !present || v != nil {
		t.Errorf("explicit --condition null must send null, got %v (present=%v)", v, present)
	}
}

const memoryJSON = `{"id":"m1","urn":"acme.com::kb","name":"KB","shortDescription":null,
	"class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1",
	"isEncrypted":false,"maxRevCount":10,"updatedAt":"2026-06-11T00:00:00Z"}`

func TestMemorySetCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemory": `{"data":{"createMemory":` + memoryJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// No --class: the server assigns the default, which the create output must
	// surface — the actual #108 friction scenario.
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "KB", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateMemory"], &vars)
	if vars["orgId"] != "acme.com" || vars["name"] != "KB" {
		t.Errorf("unexpected create vars: %v", vars)
	}
	// The create output surfaces the server-assigned class + visibility (#108).
	if s := out.String(); !strings.Contains(s, "class: knowledge") || !strings.Contains(s, "visibility: ORGANIZATION") {
		t.Errorf("create output must echo effective class/visibility, got:\n%s", s)
	}
}

func TestMemorySetCreateInApp(t *testing.T) {
	appMemoryJSON := `{"id":"m2","urn":"acme.com::coach:agent:app-mem:runbook","name":"Runbook","shortDescription":"Shared ops",
		"class":"app","visibility":null,"organizationId":"o1",
		"isEncrypted":false,"maxRevCount":25,"updatedAt":"2026-07-14T00:00:00Z"}`
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemoryInApp": `{"data":{"createMemoryInApp":` + appMemoryJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--app", "acme.com::coach", "--agent", "acme.com::agent",
		"--class", "app", "--name", "Runbook", "--short", "Shared ops", "--tag", "ops",
		"--max-rev-count", "25", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var vars map[string]any
	_ = json.Unmarshal(captured["CreateMemoryInApp"], &vars)
	for key, want := range map[string]any{
		"appRef": "acme.com::coach", "agentRef": "acme.com::agent",
		"memoryClass": "app", "name": "Runbook", "shortDescription": "Shared ops",
		"maxRevCount": float64(25),
	} {
		if vars[key] != want {
			t.Errorf("%s = %v, want %v (all vars: %v)", key, vars[key], want, vars)
		}
	}
	if _, present := vars["orgId"]; present {
		t.Errorf("App-scoped create must not send orgId: %v", vars)
	}
	if _, present := vars["visibility"]; present {
		t.Errorf("App-scoped create must not send visibility: %v", vars)
	}
	if _, present := vars["description"]; present {
		t.Errorf("unset optional description must be omitted, not sent as null: %v", vars)
	}
	tags, _ := vars["tags"].([]any)
	if len(tags) != 1 || tags[0] != "ops" {
		t.Errorf("unexpected tags: %v", vars["tags"])
	}

	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, out.String())
	}
	if dto["urn"] != "acme.com::coach:agent:app-mem:runbook" || dto["class"] != "app" {
		t.Errorf("unexpected stable memory DTO: %v", dto)
	}
}

func TestMemorySetCreateInAppValidatesFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"paired refs", []string{"--app", "app1", "--class", "app", "--name", "Runbook"}, "--app and --agent"},
		{"class required", []string{"--app", "app1", "--agent", "agent1", "--name", "Runbook"}, "requires --class"},
		{"supported class", []string{"--app", "app1", "--agent", "agent1", "--class", "knowledge", "--name", "KB"}, "requires --class app, personal, or private"},
		{"org rejected", []string{"--app", "app1", "--agent", "agent1", "--class", "app", "--name", "Runbook", "--org", "acme.com"}, "--org cannot be used"},
		{"visibility rejected", []string{"--app", "app1", "--agent", "agent1", "--class", "app", "--name", "Runbook", "--visibility", "ORGANIZATION"}, "--visibility cannot be used"},
		{"slug rejected", []string{"--app", "app1", "--agent", "agent1", "--class", "app", "--name", "Runbook", "--slug", "runbook"}, "--slug cannot be used"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := append([]string{"memory", "set"}, tt.args...)
			args = append(args, "--server", "http://127.0.0.1:1")
			root.SetArgs(args)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestMemorySetCreateRejectsAppAndSystemClassesWithoutApp(t *testing.T) {
	for _, class := range []string{"app", "system"} {
		t.Run(class, func(t *testing.T) {
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "KB", "--class", class, "--server", "http://127.0.0.1:1"})
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), "free-standing creation does not support --class "+class) {
				t.Fatalf("expected free-standing %s-class usage error, got %v", class, err)
			}
		})
	}
}

func TestMemoryAttach(t *testing.T) {
	attachedJSON := `{"id":"m3","urn":"acme.com::my-notes","name":"My notes","shortDescription":null,
		"class":"personal","visibility":null,"organizationId":"o1",
		"isEncrypted":false,"maxRevCount":10,"updatedAt":"2026-07-14T00:00:00Z"}`
	gql, captured := captureGraphQL(t, map[string]string{
		"AttachMemoryToApp": `{"data":{"attachMemoryToApp":` + attachedJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "attach", "acme.com::my-notes", "--app", "app1", "--agent", "agent1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var vars map[string]any
	_ = json.Unmarshal(captured["AttachMemoryToApp"], &vars)
	if vars["memoryRef"] != "hrn:memory:acme.com::my-notes" || vars["appRef"] != "app1" || vars["agentRef"] != "agent1" {
		t.Errorf("unexpected attach refs: %v", vars)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, out.String())
	}
	if dto["urn"] != "acme.com::my-notes" || dto["class"] != "personal" {
		t.Errorf("unexpected stable memory DTO: %v", dto)
	}
}

func TestMemoryAttachRequiresAppAndAgent(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "attach", "m1", "--app", "app1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "requires --app and --agent") {
		t.Fatalf("expected required-ref usage error, got %v", err)
	}
}

// A short memory ref (single- or double-colon org:slug) must normalize to the
// canonical hrn:memory URN the server's memory(ref:) accepts, instead of failing
// as "not found" (#108).
func TestMemoryGetShortFormNormalizes(t *testing.T) {
	for _, short := range []string{"acme.com:kb", "acme.com::kb"} {
		gql, captured := captureGraphQL(t, map[string]string{
			"GetMemory": `{"data":{"memory":` + memoryJSON + `}}`,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"memory", "get", short, "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute %q: %v", short, err)
		}
		var vars struct {
			Ref string `json:"ref"`
		}
		_ = json.Unmarshal(captured["GetMemory"], &vars)
		if vars.Ref != "hrn:memory:acme.com::kb" {
			t.Errorf("memory get %q should send canonical ref, got %q", short, vars.Ref)
		}
	}
}

func TestMemorySetUpdate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetMemory":    `{"data":{"memory":` + memoryJSON + `}}`,
		"UpdateMemory": `{"data":{"updateMemory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "acme.com::kb", "--short", "Project knowledge", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateMemory"], &vars)
	// The URN is resolved to the PK via memory(ref:) before updateMemory.
	if vars["id"] != "m1" || vars["shortDescription"] != "Project knowledge" {
		t.Errorf("unexpected update vars: %v", vars)
	}
	// Unset optionals must be OMITTED, not sent as explicit nulls —
	// the server treats omitted as "preserve".
	for _, key := range []string{"name", "description", "tags", "visibility", "urn", "maxRevCount"} {
		if v, present := vars[key]; present {
			t.Errorf("unset %q must be omitted from updateMemory variables, got %v", key, v)
		}
	}
}

// --max-rev-count threads maxRevCount into create and update, and rejects < 1
// client-side (mirrors the server guard) before any network call.
func TestMemorySetMaxRevCount(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemory": `{"data":{"createMemory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "KB", "--max-rev-count", "25", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("create execute: %v", err)
	}
	var cvars map[string]any
	_ = json.Unmarshal(captured["CreateMemory"], &cvars)
	if cvars["maxRevCount"] != float64(25) {
		t.Errorf("create should send maxRevCount=25, got %v", cvars["maxRevCount"])
	}

	gql2, captured2 := captureGraphQL(t, map[string]string{
		"GetMemory":    `{"data":{"memory":` + memoryJSON + `}}`,
		"UpdateMemory": `{"data":{"updateMemory":` + memoryJSON + `}}`,
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"memory", "set", "acme.com::kb", "--max-rev-count", "5", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("update execute: %v", err)
	}
	var uvars map[string]any
	_ = json.Unmarshal(captured2["UpdateMemory"], &uvars)
	if uvars["maxRevCount"] != float64(5) {
		t.Errorf("update should send maxRevCount=5, got %v", uvars["maxRevCount"])
	}
}

func TestMemorySetRejectsMaxRevCountBelowOne(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemory": `{"data":{"createMemory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "KB", "--max-rev-count", "0", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a usage error for --max-rev-count 0")
	}
	if _, called := captured["CreateMemory"]; called {
		t.Error("an invalid --max-rev-count must not reach the server")
	}
}

// `memory get` displays maxRevCount.
func TestMemoryGetShowsMaxRevCount(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetMemory": `{"data":{"memory":` + memoryJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "get", "acme.com::kb", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "max revisions: 10") {
		t.Errorf("get output should show max revisions: %s", out.String())
	}
}

func TestMemorySetUpdateSendsTags(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetMemory":    `{"data":{"memory":` + memoryJSON + `}}`,
		"UpdateMemory": `{"data":{"updateMemory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "acme.com::kb", "--tag", "go", "--tag", "cli", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Tags []string `json:"tags"`
	}
	_ = json.Unmarshal(captured["UpdateMemory"], &vars)
	if len(vars.Tags) != 2 || vars.Tags[0] != "go" || vars.Tags[1] != "cli" {
		t.Errorf("tags not sent: %s", captured["UpdateMemory"])
	}
}

// --slug on update renames the memory: the bare slug is sent as updateMemory's
// urn argument (#108).
func TestMemorySetUpdateSlug(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetMemory":    `{"data":{"memory":` + memoryJSON + `}}`,
		"UpdateMemory": `{"data":{"updateMemory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "acme.com::kb", "--slug", "project-kb", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateMemory"], &vars)
	if vars["id"] != "m1" || vars["urn"] != "project-kb" {
		t.Errorf("--slug should send the bare slug as updateMemory urn, got %v", vars)
	}
}

// --slug on create: createMemory derives the slug from --name, so the CLI
// follows up with a rename to the requested slug and echoes the renamed URN
// (#108 — name and slug differing, expressible in one command).
func TestMemorySetCreateWithSlug(t *testing.T) {
	renamed := `{"id":"m1","urn":"acme.com::hadrontool-pdf","name":"Hadron PDF Tool","shortDescription":null,
		"class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1",
		"isEncrypted":false,"updatedAt":"2026-06-11T00:00:00Z"}`
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemory": `{"data":{"createMemory":` + memoryJSON + `}}`,
		"UpdateMemory": `{"data":{"updateMemory":` + renamed + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "Hadron PDF Tool", "--slug", "hadrontool-pdf", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var uvars map[string]any
	_ = json.Unmarshal(captured["UpdateMemory"], &uvars)
	if uvars["id"] != "m1" || uvars["urn"] != "hadrontool-pdf" {
		t.Errorf("create --slug should rename via updateMemory urn, got %v", uvars)
	}
	if s := out.String(); !strings.Contains(s, "acme.com::hadrontool-pdf") {
		t.Errorf("output should show the renamed URN, got:\n%s", s)
	}
}

// When --slug already matches the name-derived slug, no rename call is made —
// create stays a single round-trip.
func TestMemorySetCreateSlugMatchesSkipsRename(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemory": `{"data":{"createMemory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// memoryJSON's slug is already "kb"; an unregistered UpdateMemory call would
	// error, so a wrongly-fired rename fails the test.
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "KB", "--slug", "kb", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, called := captured["UpdateMemory"]; called {
		t.Errorf("no rename should fire when --slug matches the derived slug")
	}
}

// A malformed --slug is a usage error caught client-side, before any call.
func TestMemorySetRejectsBadSlug(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "KB", "--slug", "Bad Slug", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "is not valid") {
		t.Fatalf("expected slug validation error, got %v", err)
	}
}

// An explicit empty --slug is rejected (not silently treated as "no slug"), so
// the caller fails fast instead of creating a memory under the derived slug.
func TestMemorySetRejectsEmptySlug(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "KB", "--slug", "", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "is not valid") {
		t.Fatalf("expected empty-slug rejection, got %v", err)
	}
}

func TestMemorySetCreateRequiresOrgAndName(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--name", "KB", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--org") {
		t.Fatalf("expected org/name usage error, got %v", err)
	}
}

func TestMemoryRm(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteMemory": `{"data":{"deleteMemory":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "rm", "acme.com:scratch", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteMemory"], &vars)
	if vars["id"] != "acme.com:scratch" {
		t.Errorf("unexpected delete vars: %v", vars)
	}
}

func TestMemoryClone(t *testing.T) {
	cloneJSON := `{"id":"m2","urn":"acme.com:kb-fork","name":"kb-fork","shortDescription":null,
		"class":"knowledge","visibility":"ORGANIZATION","organizationId":"org1",
		"isEncrypted":false,"updatedAt":"2026-06-12T00:00:00Z"}`
	gql, captured := captureGraphQL(t, map[string]string{
		"CloneMemory": `{"data":{"cloneMemory":` + cloneJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "clone", "acme.com::kb", "--target-urn", "acme.com::kb-fork", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CloneMemory"], &vars)
	if vars["ref"] != "acme.com::kb" || vars["targetUrn"] != "acme.com::kb-fork" {
		t.Errorf("unexpected clone vars: %v", vars)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto["urn"] != "acme.com:kb-fork" {
		t.Errorf("unexpected output dto: %v", dto)
	}
}

func TestMemoryCloneRequiresTargetURN(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "clone", "acme.com::kb"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error when --target-urn is missing")
	}
}

func TestMemoryCloneRejectsRelativeTargetURN(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// A bare slug (no "::") is caught client-side before any network call.
	root.SetArgs([]string{"memory", "clone", "acme.com::kb", "--target-urn", "just-a-slug"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "fully-qualified") {
		t.Fatalf("expected a fully-qualified URN error, got %v", err)
	}
}

func TestMemoryExtract(t *testing.T) {
	extractJSON := `{"id":"m3","urn":"acme.com:auth-kb","name":"auth-kb","shortDescription":null,
		"class":"knowledge","visibility":"ORGANIZATION","organizationId":"org1",
		"isEncrypted":false,"updatedAt":"2026-07-12T00:00:00Z"}`
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":                resolveNodeJSON,
		"ExtractParentNodeToMemory": `{"data":{"extractParentNodeToMemory":` + extractJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "extract", nodeURN, "acme.com::auth-kb", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["ExtractParentNodeToMemory"], &vars)
	// The parent ref is resolved to a node id before the mutation.
	if vars["parentRef"] != "n1" {
		t.Errorf("parentRef = %v, want n1", vars["parentRef"])
	}
	if vars["targetUrn"] != "acme.com::auth-kb" {
		t.Errorf("targetUrn = %v", vars["targetUrn"])
	}
	// move is omitted (not sent as null/false) so the server keeps its default.
	if _, ok := vars["move"]; ok {
		t.Errorf("move should be omitted when the flag is unset, got %v", vars["move"])
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto["urn"] != "acme.com:auth-kb" {
		t.Errorf("unexpected output dto: %v", dto)
	}
}

func TestMemoryExtractMoveWithMemoryFlag(t *testing.T) {
	extractJSON := `{"id":"m3","urn":"acme.com:auth-kb","name":"auth-kb","shortDescription":null,
		"class":"knowledge","visibility":"ORGANIZATION","organizationId":"org1",
		"isEncrypted":false,"updatedAt":"2026-07-12T00:00:00Z"}`
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":                resolveNodeJSON,
		"ExtractParentNodeToMemory": `{"data":{"extractParentNodeToMemory":` + extractJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// A bare loc resolves through -m; --move sends move=true.
	root.SetArgs([]string{"memory", "extract", "-m", "acme.com::kb", "findings:auth", "acme.com::auth-kb", "--move", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var resolveVars map[string]any
	_ = json.Unmarshal(captured["ResolveUrn"], &resolveVars)
	if resolveVars["urn"] != "hrn:node:acme.com::kb::findings:auth" {
		t.Errorf("ResolveUrn urn = %v", resolveVars["urn"])
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["ExtractParentNodeToMemory"], &vars)
	if vars["move"] != true {
		t.Errorf("move = %v, want true", vars["move"])
	}
	if got := out.String(); !strings.Contains(got, "moved") {
		t.Errorf("human output should note the move, got:\n%s", got)
	}
}

func TestMemoryExtractAcceptsRawNodeID(t *testing.T) {
	extractJSON := `{"id":"m3","urn":"acme.com:auth-kb","name":"auth-kb","shortDescription":null,
		"class":"knowledge","visibility":"ORGANIZATION","organizationId":"org1",
		"isEncrypted":false,"updatedAt":"2026-07-12T00:00:00Z"}`
	// No ResolveUrn mock: a colon-free raw node id is passed straight to the
	// mutation as a PK (the server's parentRef accepts it), not resolved.
	gql, captured := captureGraphQL(t, map[string]string{
		"ExtractParentNodeToMemory": `{"data":{"extractParentNodeToMemory":` + extractJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "extract", "n1", "acme.com::auth-kb", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["ExtractParentNodeToMemory"], &vars)
	if vars["parentRef"] != "n1" {
		t.Errorf("parentRef = %v, want the raw id n1 passed through", vars["parentRef"])
	}
}

// A colon-carrying bare token is a namespaced loc, not a PK — rejected client-side
// with the node-ref guidance rather than sent as a bogus id.
func TestMemoryExtractRejectsBareLocWithoutMemory(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "extract", "findings:auth", "acme.com::auth-kb"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "fully-qualified node URN") {
		t.Fatalf("expected a fully-qualified-URN usage error, got %v", err)
	}
}

func TestMemoryExtractRejectsRelativeTargetURN(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// A bare slug (no "::") is caught client-side before any network call.
	root.SetArgs([]string{"memory", "extract", nodeURN, "just-a-slug"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "fully-qualified") {
		t.Fatalf("expected a fully-qualified URN error, got %v", err)
	}
}

const appJSON = `{"id":"app1","urn":"urn:agent:acme.com::bot::acme.com:helper","name":"Bot",
	"appType":"CHATBOT","agentId":"agent1","memberCount":2,"createdAt":"2026-06-11T00:00:00Z"}`

func TestAppLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Apps": `{"data":{"apps":{"total":1,"items":[` + appJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "ls", "--org", "acme.com", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "CHATBOT") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["Apps"], &vars)
	if vars["orgId"] != "acme.com" {
		t.Errorf("unexpected vars: %v", vars)
	}
}

func TestAppInstall(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateApp": `{"data":{"createApp":` + appJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "install", "--org", "acme.com", "--agent", "agent1",
		"--name", "Bot", "--type", "CHATBOT", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateApp"], &vars)
	if vars["orgId"] != "acme.com" || vars["agentId"] != "agent1" || vars["appType"] != "CHATBOT" {
		t.Errorf("unexpected vars: %v", vars)
	}
	// Unset optionals must be OMITTED, not sent as explicit nulls.
	for _, key := range []string{"urn", "description"} {
		if v, present := vars[key]; present {
			t.Errorf("unset %q must be omitted from createApp variables, got %v", key, v)
		}
	}
}

func TestAppUninstall(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteApp": `{"data":{"deleteApp":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "uninstall", "app1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteApp"], &vars)
	if vars["id"] != "app1" {
		t.Errorf("unexpected vars: %v", vars)
	}
}

// aiConfigsJSON is a masked two-config picker result: one App-owned config
// with a key preview, one Org-owned config without a key.
const aiConfigsJSON = `{"data":{"resolveAiServiceConfigs":[
	{"id":"cfg1","name":"default","ownerType":"APP","ownerId":"app1",
	 "provider":"anthropic","model":"claude-opus-4-8","hasApiKey":true,
	 "apiKeyPreview":"…7f3a","params":{"maxTokens":4096},"enabled":true,
	 "createdAt":"2026-06-11T00:00:00Z","updatedAt":null},
	{"id":"cfg2","name":"fast","ownerType":"ORGANIZATION","ownerId":"org1",
	 "provider":"openai","model":"gpt-4o-mini","hasApiKey":false,
	 "apiKeyPreview":null,"params":null,"enabled":true,
	 "createdAt":"2026-06-11T00:00:00Z","updatedAt":null}
]}}`

func TestAiConfigLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveAiServiceConfigs": aiConfigsJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "ls", "--app", "acme.com:juno-app",
		"--agent", "acme.com:juno", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	// Table carries name, owner (ownerType), provider, model, and the key preview.
	for _, want := range []string{"default", "APP", "anthropic", "claude-opus-4-8", "7f3a",
		"fast", "ORGANIZATION", "openai", "—"} {
		if !strings.Contains(got, want) {
			t.Errorf("table output missing %q:\n%s", want, got)
		}
	}

	// --app and --agent map to the appRef/agentRef variables (ID or URN, verbatim).
	var vars map[string]any
	_ = json.Unmarshal(captured["ResolveAiServiceConfigs"], &vars)
	if vars["appRef"] != "acme.com:juno-app" || vars["agentRef"] != "acme.com:juno" {
		t.Errorf("unexpected vars: %v", vars)
	}
}

func TestAiConfigLsJSONOmitsUnsetAgent(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveAiServiceConfigs": aiConfigsJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// No --agent: it must be omitted from the variables, not sent as null.
	root.SetArgs([]string{"ai-config", "ls", "--app", "acme.com:juno-app", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	// Never any key material beyond the preview — no raw apiKey field.
	if strings.Contains(got, `"apiKey":`) {
		t.Errorf("--json output leaked a raw apiKey field:\n%s", got)
	}
	// Stable masked DTO shape.
	var dtos []map[string]any
	if err := json.Unmarshal([]byte(got), &dtos); err != nil {
		t.Fatalf("--json is not a valid array: %v\n%s", err, got)
	}
	if len(dtos) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(dtos))
	}
	for _, key := range []string{"hasApiKey", "apiKeyPreview", "ownerType", "ownerId", "params"} {
		if _, ok := dtos[0][key]; !ok {
			t.Errorf("masked DTO missing %q field: %v", key, dtos[0])
		}
	}

	var vars map[string]any
	_ = json.Unmarshal(captured["ResolveAiServiceConfigs"], &vars)
	if vars["appRef"] != "acme.com:juno-app" {
		t.Errorf("appRef should map from --app, got %v", vars["appRef"])
	}
	if v, present := vars["agentRef"]; present {
		t.Errorf("unset --agent must be omitted from variables, got %v", v)
	}
}
