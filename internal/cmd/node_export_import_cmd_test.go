package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const myMemoriesJSON = `{"data":{"myMemories":[{"id":"mem1","urn":"acme.com:kb","name":"KB",
	"shortDescription":null,"class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1",
	"isEncrypted":false,"updatedAt":"2026-06-11T00:00:00Z"}]}}`

// nodeExportResp builds a NodeExport GraphQL response wrapping data, the way the
// server's single-node renderer returns it (#106).
func nodeExportResp(format, mime, fname, data string) string {
	d, _ := json.Marshal(data)
	return fmt.Sprintf(`{"data":{"nodeExport":{"format":%q,"mimeType":%q,"filename":%q,"data":%s,"bytes":%d}}}`,
		format, mime, fname, d, len(data))
}

// A minimal node read for the file-write summary (loc/name/memory) — the
// render itself returns no identifying metadata.
const exportMetaJSON = `{"data":{"nodeById":{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI",
	"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"task","tags":[],
	"content":"x","data":null,"seq":null,"createdAt":"2026-06-11T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",
	"outgoingEdges":[],"incomingEdges":[]}}}`

// node export routes through the SERVER renderer (#106): the CLI writes exactly
// the bytes nodeExport returns, so its output is identical to the portal and
// every other API client.
func TestNodeExportToFile(t *testing.T) {
	const exportedMD = "---\nname: Flaky CI\nloc: findings:flaky-ci\nmemory: acme.com:kb\ntype: task\n---\n\nThe body.\n"
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":  resolveNodeJSON,
		"NodeExport":  nodeExportResp("MD", "text/markdown", "flaky-ci.md", exportedMD),
		"GetNodeById": exportMetaJSON,
		"MyMemories":  myMemoriesJSON,
	})
	f, out := testFactory(t)
	file := filepath.Join(t.TempDir(), "flaky.md")
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "-o", file, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var summary struct {
		Node    string `json:"node"`
		Loc     string `json:"loc"`
		Memory  string `json:"memory"`
		OutFile string `json:"outFile"`
		Format  string `json:"format"`
		Bytes   int    `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(out.String()), &summary); err != nil {
		t.Fatalf("summary not JSON: %v\n%s", err, out.String())
	}
	if summary.Memory != "acme.com:kb" || summary.Loc != "findings:flaky-ci" || summary.Format != "md" || summary.Bytes != len(exportedMD) {
		t.Errorf("summary = %+v", summary)
	}
	// The file is byte-for-byte what the server returned.
	if md := mustRead(t, file); md != exportedMD {
		t.Errorf("file must equal the server render verbatim:\ngot:  %q\nwant: %q", md, exportedMD)
	}
	// The MD format is the one requested of the server.
	var vars struct {
		Format string `json:"format"`
	}
	_ = json.Unmarshal(captured["NodeExport"], &vars)
	if vars.Format != "MD" {
		t.Errorf("server asked for format %q, want MD", vars.Format)
	}
}

// Exporting to stdout streams the server-rendered document itself — never the
// summary wrapper, even with --json, so a piped md/json stream isn't corrupted.
// The stdout path makes no extra metadata reads.
func TestNodeExportStdoutIsRawDocument(t *testing.T) {
	const md = "---\nname: Flaky CI\nloc: findings:flaky-ci\n---\n\nThe body.\n"
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"NodeExport": nodeExportResp("MD", "text/markdown", "flaky-ci.md", md),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out.String(); got != md {
		t.Errorf("stdout must be the raw server-rendered document verbatim, got:\n%q", got)
	}
	if strings.Contains(out.String(), `"outFile"`) {
		t.Errorf("stdout must not be a summary wrapper:\n%s", out.String())
	}
}

func TestNodeExportJSONFormat(t *testing.T) {
	const body = `{"loc":"findings:flaky-ci","memory":"acme.com:kb","type":"task"}`
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"NodeExport": nodeExportResp("JSON", "application/json", "flaky-ci.json", body),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "--format", "json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out.String() != body {
		t.Errorf("stdout must be the server JSON verbatim:\n%s", out.String())
	}
	var vars struct {
		Format string `json:"format"`
	}
	_ = json.Unmarshal(captured["NodeExport"], &vars)
	if vars.Format != "JSON" {
		t.Errorf("--format json must ask the server for JSON, got %q", vars.Format)
	}
}

// A server that can't render the node (not found / no access) surfaces the
// error rather than writing a silent empty file.
func TestNodeExportServerErrorPropagates(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"NodeExport": `{"errors":[{"message":"node not found","extensions":{"code":"NOT_FOUND"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error when the server render fails")
	}
}

// Against a server too old to have the nodeExport field, the schema-validation
// error becomes a clear "upgrade the server" message, not a raw GraphQL dump.
func TestNodeExportOldServerUnknownField(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"NodeExport": `{"errors":[{"message":"Cannot query field \"nodeExport\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("expected a clear old-server error, got %v", err)
	}
}

// A self-describing standalone markdown file, as `node export` writes it.
const importMd = `---
name: Flaky CI
id: n1
loc: findings:flaky-ci
memory: acme.com:kb
type: task
alias: flaky
description: One liner
abstract: A summary.
tags:
  - ci
seq: 3
data:
  k: v
properties:
  p: q
nodes:
  - id: n2
    loc: start
    rel: routes-to
    priority: 10
---

The body.
`

func TestNodeImportCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":null}}`, // absent → created
		"UpsertNode": `{"data":{"upsertNode":` + nodeJSON + `}}`,
	})
	f, out := testFactory(t)
	file := filepath.Join(t.TempDir(), "flaky.md")
	if err := os.WriteFile(file, []byte(importMd), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var summary struct {
		Action       string           `json:"action"`
		NodeID       string           `json:"nodeId"`
		Memory       string           `json:"memory"`
		Loc          string           `json:"loc"`
		EdgesWired   int              `json:"edgesWired"`
		UnwiredEdges []map[string]any `json:"unwiredEdges"`
	}
	if err := json.Unmarshal([]byte(out.String()), &summary); err != nil {
		t.Fatalf("summary not JSON: %v\n%s", err, out.String())
	}
	if summary.Action != "created" || summary.NodeID != "n1" {
		t.Errorf("summary = %+v, want created n1", summary)
	}
	if summary.UnwiredEdges == nil {
		t.Error("unwiredEdges must be [] (stable shape), not null")
	}
	if summary.EdgesWired != 0 {
		t.Errorf("edges must not be wired without --with-edges, got %d", summary.EdgesWired)
	}

	// The upsert carries the full body, including the richer fields node
	// add/update never populated (alias, data, properties, seq).
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(captured["UpsertNode"], &vars); err != nil {
		t.Fatalf("UpsertNode vars: %v", err)
	}
	in := vars.Input
	checks := map[string]any{
		"memoryId": "acme.com:kb", "loc": "findings:flaky-ci", "name": "Flaky CI",
		"content": "The body.", "nodeType": "task", "alias": "flaky",
		"description": "One liner", "abstract": "A summary.", "seq": float64(3),
	}
	for k, want := range checks {
		if in[k] != want {
			t.Errorf("input[%q] = %v, want %v", k, in[k], want)
		}
	}
	if data, _ := in["data"].(map[string]any); data["k"] != "v" {
		t.Errorf("input.data not mapped: %v", in["data"])
	}
	if props, _ := in["properties"].(map[string]any); props["p"] != "q" {
		t.Errorf("input.properties not mapped: %v", in["properties"])
	}
	if tags, _ := in["tags"].([]any); len(tags) != 1 || tags[0] != "ci" {
		t.Errorf("input.tags not mapped: %v", in["tags"])
	}
	// createOnly must be omitted on a plain import (upsert semantics give
	// create-or-update for free).
	if _, present := in["createOnly"]; present {
		t.Errorf("createOnly must be omitted without --create-only, got %v", in["createOnly"])
	}
	// The recompute-only hashes must never be sent.
	for _, k := range []string{"contentHash", "abstractOriginHash", "id"} {
		if _, present := in[k]; present {
			t.Errorf("%q must not be sent in the upsert input", k)
		}
	}
}

func TestNodeImportUpdate(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON, // present → updated
		"UpsertNode": `{"data":{"upsertNode":` + nodeJSON + `}}`,
	})
	f, out := testFactory(t)
	file := filepath.Join(t.TempDir(), "flaky.md")
	if err := os.WriteFile(file, []byte(importMd), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var summary struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal([]byte(out.String()), &summary)
	if summary.Action != "updated" {
		t.Errorf("action = %q, want updated", summary.Action)
	}
}

func TestNodeImportTargetPrecedenceAndCreateOnly(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":null}}`,
		"UpsertNode": `{"data":{"upsertNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	file := filepath.Join(t.TempDir(), "flaky.md")
	if err := os.WriteFile(file, []byte(importMd), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "-m", "acme.com:other", "--loc", "moved:here", "--create-only", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpsertNode"], &vars)
	if vars.Input["memoryId"] != "acme.com:other" || vars.Input["loc"] != "moved:here" {
		t.Errorf("flags must override frontmatter target: %v", vars.Input)
	}
	if vars.Input["createOnly"] != true {
		t.Errorf("--create-only must set createOnly, got %v", vars.Input["createOnly"])
	}
}

func TestNodeImportMissingTargetIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	file := filepath.Join(t.TempDir(), "bare.md")
	if err := os.WriteFile(file, []byte("---\nname: X\n---\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "no target memory") {
		t.Fatalf("expected a no-target usage error, got %v", err)
	}
}

func TestNodeImportStdin(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":null}}`,
		"UpsertNode": `{"data":{"upsertNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader(importMd)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpsertNode"], &vars)
	if vars.Input["name"] != "Flaky CI" {
		t.Errorf("stdin import did not parse the piped document: %v", vars.Input)
	}
}

func TestNodeImportEmptyInputIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("   \n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "empty input") {
		t.Fatalf("expected an empty-input usage error, got %v", err)
	}
}

func TestNodeImportDryRunMutatesNothing(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":null}}`,
	})
	f, out := testFactory(t)
	file := filepath.Join(t.TempDir(), "flaky.md")
	if err := os.WriteFile(file, []byte(importMd), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if captured["UpsertNode"] != nil {
		t.Error("--dry-run must not call UpsertNode")
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Errorf("dry-run output should say so:\n%s", out.String())
	}
}

const importEdgesMd = `---
name: Flaky CI
id: n1
loc: findings:flaky-ci
memory: acme.com:kb
type: task
nodes:
  - id: ne1
    loc: start
    rel: existing-label
  - id: ne2
    loc: other
    rel: new-label
    priority: 7
    condition:
      flag: x
---

body
`

// --with-edges wires only the new edge: the (target, label) already on the node
// is skipped (idempotent re-import), and condition + priority are forwarded.
func TestNodeImportWithEdgesIdempotent(t *testing.T) {
	const existingEdges = `{"data":{"nodeById":{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI",
		"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"task","tags":[],
		"content":"x","data":null,"seq":null,"createdAt":"2026-06-11T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",
		"outgoingEdges":[{"id":"e0","name":"existing-label","priority":0,"target":{"id":"n2","loc":"start","memoryId":"mem1"}}],
		"incomingEdges":[]}}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":  `{"data":{"resolveUrn":{"id":"n2","kind":"node","memoryId":"mem1"}}}`,
		"UpsertNode":  `{"data":{"upsertNode":` + nodeJSON + `}}`,
		"GetNodeById": existingEdges,
		"CreateEdge":  `{"data":{"createEdge":` + edgeJSON + `}}`,
	})
	f, out := testFactory(t)
	file := filepath.Join(t.TempDir(), "flaky.md")
	if err := os.WriteFile(file, []byte(importEdgesMd), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "--with-edges", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var summary struct {
		EdgesWired   int              `json:"edgesWired"`
		UnwiredEdges []map[string]any `json:"unwiredEdges"`
	}
	_ = json.Unmarshal([]byte(out.String()), &summary)
	if summary.EdgesWired != 1 {
		t.Errorf("edgesWired = %d, want 1 (existing edge skipped)", summary.EdgesWired)
	}

	// The single createEdge is the NEW edge, with its condition + priority.
	var ce map[string]any
	_ = json.Unmarshal(captured["CreateEdge"], &ce)
	if ce["name"] != "new-label" {
		t.Errorf("wired the wrong edge: %v", ce)
	}
	if ce["sourceNodeId"] != "n1" || ce["targetNodeId"] != "n2" {
		t.Errorf("createEdge endpoints = %v", ce)
	}
	if ce["priority"] != float64(7) {
		t.Errorf("priority not forwarded: %v", ce["priority"])
	}
	if cond, _ := ce["condition"].(map[string]any); cond["flag"] != "x" {
		t.Errorf("condition not forwarded: %v", ce["condition"])
	}
}

// An edge whose target can't be resolved is reported in unwiredEdges, never
// fatal — the import still succeeds (exit 0).
func TestNodeImportWithEdgesUnwiredTarget(t *testing.T) {
	const noEdges = `{"data":{"nodeById":{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI",
		"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"task","tags":[],
		"content":"x","data":null,"seq":null,"createdAt":"2026-06-11T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":  `{"data":{"resolveUrn":null}}`, // node absent (created) + target unresolvable
		"UpsertNode":  `{"data":{"upsertNode":` + nodeJSON + `}}`,
		"GetNodeById": noEdges,
	})
	f, out := testFactory(t)
	file := filepath.Join(t.TempDir(), "ghost.md")
	// An edge with no id and an unresolvable loc → nothing to wire.
	ghostMd := "---\nname: X\nid: n1\nloc: findings:flaky-ci\nmemory: acme.com:kb\nnodes:\n  - loc: ghost\n    rel: routes-to\n---\n\nbody\n"
	if err := os.WriteFile(file, []byte(ghostMd), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "--with-edges", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute should succeed despite unwired edges: %v", err)
	}
	if captured["CreateEdge"] != nil {
		t.Error("no edge should be created for an unresolvable target")
	}
	var summary struct {
		EdgesWired   int `json:"edgesWired"`
		UnwiredEdges []struct {
			Target string `json:"target"`
			Reason string `json:"reason"`
		} `json:"unwiredEdges"`
	}
	_ = json.Unmarshal([]byte(out.String()), &summary)
	if summary.EdgesWired != 0 || len(summary.UnwiredEdges) != 1 || summary.UnwiredEdges[0].Target != "ghost" {
		t.Errorf("expected one unwired edge 'ghost', got wired=%d unwired=%+v", summary.EdgesWired, summary.UnwiredEdges)
	}
	if !strings.Contains(summary.UnwiredEdges[0].Reason, "unresolved") {
		t.Errorf("unwired reason should explain the unresolved target, got %q", summary.UnwiredEdges[0].Reason)
	}
}

// An edge the server rejects (e.g. a condition operator outside the v1
// allowlist) is reported with the reason, and the import still succeeds.
func TestNodeImportWithEdgesRejectedReason(t *testing.T) {
	const noEdges = `{"data":{"nodeById":{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI",
		"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"task","tags":[],
		"content":"x","data":null,"seq":null,"createdAt":"2026-06-11T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}}}`
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":  `{"data":{"resolveUrn":{"id":"n2","kind":"node","memoryId":"mem1"}}}`,
		"UpsertNode":  `{"data":{"upsertNode":` + nodeJSON + `}}`,
		"GetNodeById": noEdges,
		"CreateEdge":  `{"errors":[{"message":"createEdge operator 'flag' is not in the v1 allowlist"}]}`,
	})
	f, out := testFactory(t)
	file := filepath.Join(t.TempDir(), "edge.md")
	oneEdgeMd := "---\nname: X\nid: n1\nloc: findings:flaky-ci\nmemory: acme.com:kb\nnodes:\n  - id: ne\n    loc: other\n    rel: routes-to\n    condition:\n      flag: x\n---\n\nbody\n"
	if err := os.WriteFile(file, []byte(oneEdgeMd), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "--with-edges", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute should succeed (best-effort edges): %v", err)
	}
	var summary struct {
		EdgesWired   int `json:"edgesWired"`
		UnwiredEdges []struct {
			Target string `json:"target"`
			Reason string `json:"reason"`
		} `json:"unwiredEdges"`
	}
	_ = json.Unmarshal([]byte(out.String()), &summary)
	if summary.EdgesWired != 0 || len(summary.UnwiredEdges) != 1 {
		t.Fatalf("expected 1 unwired edge, got wired=%d unwired=%+v", summary.EdgesWired, summary.UnwiredEdges)
	}
	u := summary.UnwiredEdges[0]
	if u.Target != "other" {
		t.Errorf("unwired target = %q, want other", u.Target)
	}
	if !strings.Contains(u.Reason, "rejected") || !strings.Contains(u.Reason, "allowlist") {
		t.Errorf("reason should surface the server rejection, got %q", u.Reason)
	}
}
