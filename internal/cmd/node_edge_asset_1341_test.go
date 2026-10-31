package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1341 (cor:api:240:02): node, edge, asset and comment surfaces the
// CLI did not reach. Each test is red on main because the command or flag does
// not exist, or the variable is not sent.

func run1341(t *testing.T, responses map[string]string, args ...string) (map[string]json.RawMessage, string, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	return captured, out.String(), err
}

func vars1341(t *testing.T, captured map[string]json.RawMessage, op string) map[string]any {
	t.Helper()
	raw, ok := captured[op]
	if !ok {
		t.Fatalf("%s was not sent; sent %v", op, keys1340(captured))
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s variables: %v", op, err)
	}
	return v
}

func asJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// setOnly drops the null keys of each edge object: NodeEdgeInput's unset
// fields go out as null (as every existing caller sends them), which the
// server reads as unset on these inputs.
func setOnly(v any) any {
	list, _ := v.([]any)
	out := []any{}
	for _, e := range list {
		m, _ := e.(map[string]any)
		kept := map[string]any{}
		for k, x := range m {
			if x != nil {
				kept[k] = x
			}
		}
		out = append(out, kept)
	}
	return out
}

// refused1341 asserts a usage refusal that sent none of ops.
func refused1341(t *testing.T, captured map[string]json.RawMessage, err error, label string, ops ...string) {
	t.Helper()
	if exitCodeFor(err) != exitcode.Usage {
		t.Errorf("%s: want exit 2, got %v", label, err)
	}
	for _, op := range ops {
		if _, sent := captured[op]; sent {
			t.Errorf("%s: %s must not be sent", label, op)
		}
	}
}

const authoredNode1341 = `{"id":"n9","memoryId":"mem1","loc":"findings:new","name":"New","nodeType":"info","tags":[],"seq":null,"isRunnable":false,"role":null,"updatedAt":"2026-10-31T00:00:00Z"}`

// node add --id and --edge reach createNode's input.id and input.edges.*.
func TestNodeAddIDAndEdges1341(t *testing.T) {
	responses := map[string]string{"CreateNode": `{"data":{"createNode":` + authoredNode1341 + `}}`}
	captured, out, err := run1341(t, responses, "node", "add", "-m", "acme.com::kb", "--loc", "findings:new", "--name", "New",
		"--id", "01a1b2c3d4e5f60718293a4b5c6d7e8f",
		"--edge", "routes-to=start-here",
		"--edge", `{"target":"hrn:node:acme.com:kb:other","name":"cites","loc":"findings:new:cites:other","description":"why","runnable":true}`)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	in, _ := vars1341(t, captured, "CreateNode")["input"].(map[string]any)
	if in["id"] != "01a1b2c3d4e5f60718293a4b5c6d7e8f" {
		t.Errorf("input.id = %v", in["id"])
	}
	want := `[{"name":"routes-to","targetId":"start-here"},{"description":"why","isRunnable":true,"loc":"findings:new:cites:other","name":"cites","targetId":"hrn:node:acme.com:kb:other"}]`
	if got := asJSON(setOnly(in["edges"])); got != want {
		t.Errorf("input.edges =\n %s\nwant\n %s", got, want)
	}

	for _, bad := range [][]string{
		{"--edge", ""},
		{"--edge", "routes-to="},
		{"--edge", `{"name":"x"}`},
		{"--edge", `{"target":"x","bogus":1}`},
		{"--id", ""},
	} {
		captured, _, err := run1341(t, responses, append([]string{"node", "add", "-m", "acme.com::kb", "--loc", "findings:new", "--name", "New"}, bad...)...)
		refused1341(t, captured, err, strings.Join(bad, " "), "CreateNode")
	}
}

// node update --edge replaces the outgoing set, so it needs --replace-edges.
func TestNodeUpdateEdgesNeedReplace1341(t *testing.T) {
	responses := map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
		"UpdateNode": `{"data":{"updateNode":` + authoredNode1341 + `}}`,
	}
	captured, out, err := run1341(t, responses, "node", "update", nodeURN, "--edge", `{"target":"start-here","loc":"l1","description":"d","runnable":false}`, "--replace-edges")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	in, _ := vars1341(t, captured, "UpdateNode")["input"].(map[string]any)
	if got := asJSON(setOnly(in["edges"])); got != `[{"description":"d","isRunnable":false,"loc":"l1","targetId":"start-here"}]` {
		t.Errorf("input.edges = %s", got)
	}
	for _, bad := range [][]string{{"--edge", "x=y"}, {"--replace-edges"}} {
		captured, _, err := run1341(t, responses, append([]string{"node", "update", nodeURN}, bad...)...)
		refused1341(t, captured, err, strings.Join(bad, " "), "UpdateNode")
	}
}

// node rm --hard --cascade-cross-memory-edges reaches deleteNode's argument;
// without --hard the server ignores it, so the CLI refuses.
func TestNodeRmCascadeCrossMemoryEdges1341(t *testing.T) {
	responses := map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
		"DeleteNode": `{"data":{"deleteNode":true}}`,
	}
	captured, out, err := run1341(t, responses, "node", "rm", nodeURN, "--hard", "--cascade-cross-memory-edges", "--yes")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if v := vars1341(t, captured, "DeleteNode"); v["cascadeCrossMemoryEdges"] != true || v["hard"] != true {
		t.Errorf("DeleteNode vars = %v", v)
	}
	captured, _, err = run1341(t, responses, "node", "rm", nodeURN, "--hard", "--yes")
	if err != nil {
		t.Fatalf("plain hard rm: %v", err)
	}
	if _, ok := vars1341(t, captured, "DeleteNode")["cascadeCrossMemoryEdges"]; ok {
		t.Errorf("cascadeCrossMemoryEdges must be omitted when not passed")
	}
	captured, _, err = run1341(t, responses, "node", "rm", nodeURN, "--cascade-cross-memory-edges", "--yes")
	refused1341(t, captured, err, "cascade without --hard", "DeleteNode")
}

// node set-properties reaches updateNodeProperties.
func TestNodeSetProperties1341(t *testing.T) {
	responses := map[string]string{
		"ResolveUrn":           resolveNodeJSON,
		"UpdateNodeProperties": `{"data":{"updateNodeProperties":{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI","properties":{"a":1,"b":2},"updatedAt":"2026-10-31T00:00:00Z"}}}`,
	}
	captured, out, err := run1341(t, responses, "node", "set-properties", nodeURN, "--patch", `{"b":2}`, "--reason", "fix", "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	v := vars1341(t, captured, "UpdateNodeProperties")
	if v["nodeRef"] != "n1" || asJSON(v["properties"]) != `{"b":2}` || v["reason"] != "fix" {
		t.Errorf("vars = %v", v)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil || asJSON(dto["properties"]) != `{"a":1,"b":2}` {
		t.Errorf("--json = %s (%v)", out, err)
	}
	for _, bad := range [][]string{{"--patch", "[1]"}, {"--patch", "{bad"}, {}, {"--patch", "{}", "--patch-file", "x"}} {
		captured, _, err := run1341(t, responses, append([]string{"node", "set-properties", nodeURN}, bad...)...)
		refused1341(t, captured, err, strings.Join(bad, " "), "UpdateNodeProperties")
	}
}

const edgeJSON1341 = `{"id":"e1","urn":"hrn:edge:acme.com:kb:findings:a:routes-to:b","loc":"findings:a:routes-to:b","name":"routes-to","description":null,"isRunnable":false,"priority":0,
	"source":{"id":"n1","loc":"findings:a"},"target":{"id":"n2","loc":"b"}}`

// edge get reaches the uniform single-edge read.
func TestEdgeGet1341(t *testing.T) {
	responses := map[string]string{"GetEdge": `{"data":{"edge":` + edgeJSON1341 + `}}`}
	captured, out, err := run1341(t, responses, "edge", "get", "hrn:edge:acme.com:kb:findings:a:routes-to:b", "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if v := vars1341(t, captured, "GetEdge"); v["ref"] != "hrn:edge:acme.com:kb:findings:a:routes-to:b" {
		t.Errorf("vars = %v", v)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil || dto["urn"] != "hrn:edge:acme.com:kb:findings:a:routes-to:b" || dto["sourceLoc"] != "findings:a" {
		t.Errorf("--json = %s (%v)", out, err)
	}
	_, _, err = run1341(t, map[string]string{"GetEdge": `{"data":{"edge":null}}`}, "edge", "get", "e404")
	if exitCodeFor(err) != exitcode.NotFound {
		t.Errorf("a null edge must exit %d, got %v", exitcode.NotFound, err)
	}
}

// edge list with no node reaches the uniform edges list and every filter.
func TestEdgeListUniform1341(t *testing.T) {
	responses := map[string]string{"ListEdges": `{"data":{"edges":{"total":1,"items":[` + edgeJSON1341 + `]}}}`}
	captured, out, err := run1341(t, responses, "edge", "list", "-m", "hrn:mem:acme.com:kb", "--source", "n1", "--target", "n2",
		"--runnable=false", "--include-deleted", "--org", "acme.com", "--limit", "5", "--offset", "10", "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	v := vars1341(t, captured, "ListEdges")
	if asJSON(v["filter"]) != `{"includeDeleted":true,"isRunnable":false,"memoryId":"hrn:mem:acme.com:kb","sourceNodeId":"n1","targetNodeId":"n2"}` ||
		v["orgId"] != "acme.com" || v["limit"] != float64(5) || v["offset"] != float64(10) {
		t.Errorf("vars = %v", v)
	}
	var dto struct {
		Total int              `json:"total"`
		Edges []map[string]any `json:"edges"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil || dto.Total != 1 || len(dto.Edges) != 1 || dto.Edges[0]["targetLoc"] != "b" {
		t.Errorf("--json = %s (%v)", out, err)
	}
	// Node-only filters need a node; list-only flags need none.
	captured, _, err = run1341(t, responses, "edge", "list", "--direction", "incoming")
	refused1341(t, captured, err, "--direction without a node", "ListEdges")
	captured, _, err = run1341(t, responses, "edge", "list", nodeURN, "--source", "n1")
	refused1341(t, captured, err, "--source with a node", "ListEdges", "GetNode")
}

// asset list --all reaches the cross-memory asset list and every filter.
func TestAssetListAll1341(t *testing.T) {
	responses := map[string]string{"ListAssets": `{"data":{"assets":{"total":1,"items":[{"id":"a1","urn":"hrn:asset:acme.com:kb:assets:a1","filename":"f.png","mimeType":"image/png","sizeBytes":3,
		"description":null,"scanStatus":"CLEAN","scanSignature":null,"uploadedAt":"2026-10-31T00:00:00Z","uploadedBy":null,"deletedAt":null,"memoryId":"mem1",
		"publicUrl":null,"publicLinkEnabled":false,"canManagePublicLink":false,"publicLinkAvailable":false}]}}}`}
	captured, out, err := run1341(t, responses, "asset", "list", "--all", "-m", "hrn:mem:acme.com:kb", "-m", "hrn:mem:acme.com:other",
		"--org", "acme.com", "--mine", "--mime", "image/png", "--scan-status", "CLEAN", "--include-deleted", "--limit", "5", "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	v := vars1341(t, captured, "ListAssets")
	if asJSON(v["filter"]) != `{"includeDeleted":true,"memoryRefs":["hrn:mem:acme.com:kb","hrn:mem:acme.com:other"],"mimeType":"image/png","mine":true,"scanStatus":"CLEAN"}` ||
		v["orgId"] != "acme.com" || v["limit"] != float64(5) {
		t.Errorf("vars = %v", v)
	}
	var dto struct {
		Total  int              `json:"total"`
		Assets []map[string]any `json:"assets"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil || dto.Total != 1 || len(dto.Assets) != 1 || dto.Assets[0]["memoryId"] != "mem1" {
		t.Errorf("--json = %s (%v)", out, err)
	}
	captured, _, err = run1341(t, responses, "asset", "list", "--org", "acme.com", "-m", "hrn:mem:acme.com:kb")
	refused1341(t, captured, err, "--org without --all", "ListAssets", "MemoryAssets")
	captured, _, err = run1341(t, responses, "asset", "list", "--all", "--scan-status", "MAYBE")
	refused1341(t, captured, err, "bad --scan-status", "ListAssets")
}

// comment delete --thread reaches deleteCommentThread, with its required reason.
func TestCommentDeleteThread1341(t *testing.T) {
	responses := map[string]string{"DeleteCommentThread": `{"data":{"deleteCommentThread":true}}`}
	captured, out, err := run1341(t, responses, "comment", "delete", "hrn:node:acme.com:kb:comments:c1", "--thread", "--reason", "spam", "--yes", "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if v := vars1341(t, captured, "DeleteCommentThread"); v["ref"] != "hrn:node:acme.com:kb:comments:c1" || v["reason"] != "spam" {
		t.Errorf("vars = %v", v)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil || dto["deleted"] != true || dto["thread"] != "hrn:node:acme.com:kb:comments:c1" {
		t.Errorf("--json = %s (%v)", out, err)
	}
	for _, bad := range [][]string{
		{"--thread", "--yes"},
		{"--thread", "--reason", " ", "--yes"},
		{"--thread", "--reason", "spam"},
		{"--reason", "spam", "--expected-revision", "3", "--yes"},
	} {
		captured, _, err := run1341(t, responses, append([]string{"comment", "delete", "hrn:node:acme.com:kb:comments:c1"}, bad...)...)
		refused1341(t, captured, err, strings.Join(bad, " "), "DeleteCommentThread", "DeleteComment")
	}
}

// search reaches findNodes' expand, fields, granularity and minScore, and
// prints the passages a chunk search returns.
func TestSearchRankingArguments1341(t *testing.T) {
	env := `{"data":{"findNodes":{"total":0,"degraded":null,"reason":null,"hits":[],
		"passages":[{"parentNodeId":"n1","parentNodeUrn":"hrn:node:acme.com:kb:a","chunkIndex":2,"charStart":10,"charEnd":40}]}}}`
	captured, out, err := run1341(t, map[string]string{"SearchNodes": env}, "search", "flaky", "--mode", "vector", "--expand", "2",
		"--field", "name", "--field", "content", "--granularity", "chunk", "--min-score", "0.4", "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	v := vars1341(t, captured, "SearchNodes")
	if v["expand"] != float64(2) || asJSON(v["fields"]) != `["name","content"]` || v["granularity"] != "chunk" || v["minScore"] != 0.4 {
		t.Errorf("vars = %v", v)
	}
	var dto struct {
		Passages []map[string]any `json:"passages"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil || len(dto.Passages) != 1 || dto.Passages[0]["parentNodeUrn"] != "hrn:node:acme.com:kb:a" {
		t.Errorf("--json = %s (%v)", out, err)
	}
	captured, _, _ = run1341(t, map[string]string{"SearchNodes": searchEnvelope}, "search", "flaky")
	for _, k := range []string{"expand", "fields", "granularity", "minScore"} {
		if _, ok := vars1341(t, captured, "SearchNodes")[k]; ok {
			t.Errorf("%s must be omitted when not passed", k)
		}
	}
	for _, bad := range [][]string{{"--granularity", "page"}, {"--field", "body"}, {"--expand", "-1"}} {
		captured, _, err := run1341(t, map[string]string{"SearchNodes": env}, append([]string{"search", "flaky"}, bad...)...)
		refused1341(t, captured, err, strings.Join(bad, " "), "SearchNodes")
	}
}

// node list reaches the findNodes filter fields it used to drop.
func TestNodeListFilters1341(t *testing.T) {
	responses := map[string]string{"FindNodes": `{"data":{"nodes":[]}}`}
	captured, out, err := run1341(t, responses, "node", "list", "--created-by", "u1", "--include-deleted", "--memory-class", "system",
		"--memory-class", "knowledge", "--role", "spec", "--updated-after", "2026-10-01T00:00:00Z", "--updated-before", "2026-10-31T00:00:00Z", "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	f, _ := vars1341(t, captured, "FindNodes")["filter"].(map[string]any)
	for k, w := range map[string]string{"createdBy": `"u1"`, "includeDeleted": `true`, "memoryClasses": `["system","knowledge"]`, "role": `"spec"`,
		"updatedAfter": `"2026-10-01T00:00:00Z"`, "updatedBefore": `"2026-10-31T00:00:00Z"`} {
		if got := asJSON(f[k]); got != w {
			t.Errorf("filter.%s = %s, want %s", k, got, w)
		}
	}
	for _, bad := range [][]string{{"--memory-class", "secret"}, {"--role", ""}, {"--created-by", ""}, {"--updated-after", "yesterday"}} {
		captured, _, err := run1341(t, responses, append([]string{"node", "list"}, bad...)...)
		refused1341(t, captured, err, strings.Join(bad, " "), "FindNodes")
	}
}

// node export --section is the presentation render (full: false).
func TestNodeExportSections1341(t *testing.T) {
	responses := map[string]string{
		"ResolveUrn":         resolveNodeJSON,
		"NodeExportSections": nodeExportResp("MD", "text/markdown", "flaky-ci.md", "# Flaky CI\n"),
		"NodeExportMeta":     exportMetaJSON,
	}
	captured, out, err := run1341(t, responses, "node", "export", nodeURN, "--section", "metadata", "--section", "content")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if v := vars1341(t, captured, "NodeExportSections"); asJSON(v["sections"]) != `["METADATA","CONTENT"]` || v["format"] != "MD" {
		t.Errorf("vars = %v", v)
	}
	if _, sent := captured["NodeExport"]; sent {
		t.Errorf("a sectioned export must not use the canonical render")
	}
	if !strings.Contains(out, "# Flaky CI") {
		t.Errorf("output = %q", out)
	}
	for _, bad := range [][]string{{"--section", "bogus"}, {"--section", "content", "--format", "pdf", "-o", "x.pdf"}} {
		captured, _, err := run1341(t, responses, append([]string{"node", "export", nodeURN}, bad...)...)
		refused1341(t, captured, err, strings.Join(bad, " "), "NodeExportSections", "NodeExport")
	}
}

// asset upload --agent-request sends intent AGENT_REQUEST with the request id.
func TestAssetUploadAgentRequest1341(t *testing.T) {
	fx := newUploadFixture(t, 200)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetMemory":           assetMemoryResp,
		"BeginAssetUpload":    `{"data":{"beginAssetUploadV2":{"uploadId":"up1","putUrl":"` + fx.storeURL + `","putHeaders":[],"storageKey":"k","maxSizeBytes":10485760,"allowedMimeType":"image/png","expiresAt":"2026-08-06T01:00:00Z"}}}`,
		"CompleteAssetUpload": `{"data":{"completeAssetUpload":{"id":"a1","urn":"hrn:asset:acme.com:kb:assets:a1","filename":"logo.png","mimeType":"image/png","sizeBytes":7,"scanStatus":"PENDING","uploadedAt":"2026-08-06T00:00:00Z","memoryId":"mem1","publicUrl":null}}}`,
	})
	p := writeTempFile(t, "logo.png", "PNGDATA")
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "upload", p, "-m", "hrn:mem:acme.com:kb", "--agent-request", "req-7", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if v := vars1341(t, captured, "BeginAssetUpload"); v["intent"] != "AGENT_REQUEST" || v["uploadRequestId"] != "req-7" {
		t.Errorf("vars = %v", v)
	}
	gql2, captured2 := captureGraphQL(t, map[string]string{"GetMemory": assetMemoryResp})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"asset", "upload", p, "-m", "hrn:mem:acme.com:kb", "--agent-request", "", "--server", gql2.URL})
	refused1341(t, captured2, root2.Execute(), "empty --agent-request", "BeginAssetUpload")
}
