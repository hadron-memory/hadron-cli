package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// nodeExportResp builds a TEXT-encoded NodeExport response (MD/JSON), the way the
// server's single-node renderer returns it (#106).
func nodeExportResp(format, mime, fname, data string) string {
	return nodeExportRespEnc(format, "TEXT", mime, fname, data)
}

// nodeExportRespEnc builds a NodeExport response with an explicit encoding — TEXT
// for MD/JSON, BASE64 for PDF (#109). `data` is the raw payload string exactly as
// the server would put it in the `data` field (already base64 for BASE64).
func nodeExportRespEnc(format, encoding, mime, fname, data string) string {
	d, _ := json.Marshal(data)
	return fmt.Sprintf(`{"data":{"nodeExport":{"format":%q,"encoding":%q,"mimeType":%q,"filename":%q,"data":%s,"bytes":%d}}}`,
		format, encoding, mime, fname, d, len(data))
}

// The minimal NodeExportMeta read for the file-write summary (loc/name/memory)
// — the render itself returns no identifying metadata. memory { urn } here means
// no second memory-list round-trip.
const exportMetaJSON = `{"data":{"node":{"loc":"findings:flaky-ci","name":"Flaky CI",
	"memoryId":"mem1","memory":{"urn":"acme.com:kb"}}}}`

// node export routes through the SERVER renderer (#106): the CLI writes exactly
// the bytes nodeExport returns, so its output is identical to the portal and
// every other API client.
func TestNodeExportToFile(t *testing.T) {
	const exportedMD = "---\nname: Flaky CI\nloc: findings:flaky-ci\nmemory: acme.com:kb\ntype: task\n---\n\nThe body.\n"
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":     resolveNodeJSON,
		"NodeExport":     nodeExportResp("MD", "text/markdown", "flaky-ci.md", exportedMD),
		"NodeExportMeta": exportMetaJSON,
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

// When the best-effort metadata read comes up empty, the file is still written
// verbatim and the summary falls back to the original ref — never "exported
// to" with a blank name.
func TestNodeExportFileSummaryFallback(t *testing.T) {
	const md = "---\nloc: findings:flaky-ci\n---\n\nbody\n"
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":     resolveNodeJSON,
		"NodeExport":     nodeExportResp("MD", "text/markdown", "f.md", md),
		"NodeExportMeta": `{"data":{"node":null}}`, // metadata unreadable
	})
	f, out := testFactory(t)
	file := filepath.Join(t.TempDir(), "f.md")
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "-o", file, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("export should still succeed when metadata is unreadable: %v", err)
	}
	if got := mustRead(t, file); got != md {
		t.Errorf("file must be written verbatim even without metadata: %q", got)
	}
	if s := out.String(); !strings.Contains(s, nodeURN) || strings.Contains(s, "exported  to") {
		t.Errorf("summary must name the node via the original ref, got:\n%s", s)
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

// PDF is server-rendered and returned BASE64-encoded (#109): the CLI must decode
// it to the real bytes and write those, never the base64 text.
func TestNodeExportPDFDecodesToFile(t *testing.T) {
	pdfBytes := "%PDF-1.7\n\x00\x01binary\xff\n%%EOF"
	b64 := base64.StdEncoding.EncodeToString([]byte(pdfBytes))
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":     resolveNodeJSON,
		"NodeExport":     nodeExportRespEnc("PDF", "BASE64", "application/pdf", "flaky-ci.pdf", b64),
		"NodeExportMeta": exportMetaJSON,
	})
	f, out := testFactory(t)
	file := filepath.Join(t.TempDir(), "flaky.pdf")
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "--format", "pdf", "-o", file, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The file holds the DECODED bytes, byte-for-byte — not the base64 string.
	if got := mustRead(t, file); got != pdfBytes {
		t.Errorf("file must be the decoded PDF bytes:\ngot:  %q\nwant: %q", got, pdfBytes)
	}
	var summary struct {
		Format string `json:"format"`
		Bytes  int    `json:"bytes"`
	}
	_ = json.Unmarshal([]byte(out.String()), &summary)
	if summary.Format != "pdf" || summary.Bytes != len(pdfBytes) {
		t.Errorf("summary should report pdf + decoded byte count, got %+v", summary)
	}
	var vars struct {
		Format string `json:"format"`
	}
	_ = json.Unmarshal(captured["NodeExport"], &vars)
	if vars.Format != "PDF" {
		t.Errorf("--format pdf must ask the server for PDF, got %q", vars.Format)
	}
}

// A binary (PDF) export has no safe stdout form, so it's refused without -o.
func TestNodeExportPDFToStdoutRejected(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("%PDF-1.7"))
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"NodeExport": nodeExportRespEnc("PDF", "BASE64", "application/pdf", "f.pdf", b64),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "--format", "pdf", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a usage error exporting PDF to stdout")
	}
	if out.String() != "" {
		t.Errorf("nothing should be written to stdout for a rejected PDF export, got %q", out.String())
	}
}

// `-o file.pdf` without --format infers PDF from the extension.
func TestNodeExportPDFInferredFromExtension(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("%PDF-1.7\nx"))
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":     resolveNodeJSON,
		"NodeExport":     nodeExportRespEnc("PDF", "BASE64", "application/pdf", "f.pdf", b64),
		"NodeExportMeta": exportMetaJSON,
	})
	f, _ := testFactory(t)
	file := filepath.Join(t.TempDir(), "out.pdf")
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "-o", file, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Format string `json:"format"`
	}
	_ = json.Unmarshal(captured["NodeExport"], &vars)
	if vars.Format != "PDF" {
		t.Errorf(".pdf extension should infer PDF, got %q", vars.Format)
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
		// A new target: the probe misses, so no overwrite gate; the update-by-
		// (memory,loc) attempt then misses with NODE_NOT_FOUND and falls back to
		// createNode → "created".
		"ResolveUrn": resolveNullJSON,
		"UpdateNode": `{"errors":[{"message":"node not found","extensions":{"code":"NODE_NOT_FOUND"}}]}`,
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
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

	// The create carries the full body, including the richer fields node
	// add/update never populated (alias, data, properties, seq).
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(captured["CreateNode"], &vars); err != nil {
		t.Fatalf("CreateNode vars: %v", err)
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
	// The retired NodeInput.createOnly flag and the recompute-only hashes
	// must never be sent.
	for _, k := range []string{"createOnly", "contentHash", "abstractOriginHash", "id"} {
		if _, present := in[k]; present {
			t.Errorf("%q must not be sent in the create input", k)
		}
	}
	// The update attempt targeted the same (memory, loc) with the same name.
	var upd struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(captured["UpdateNode"], &upd); err != nil {
		t.Fatalf("UpdateNode vars: %v", err)
	}
	if upd.Input["memoryId"] != "acme.com:kb" || upd.Input["loc"] != "findings:flaky-ci" || upd.Input["name"] != "Flaky CI" {
		t.Errorf("update attempt must select by (memoryId, loc) and carry the file's name: %v", upd.Input)
	}
	if _, present := upd.Input["id"]; present {
		t.Errorf("update attempt must not send id, got %v", upd.Input["id"])
	}
}

func TestNodeImportUpdate(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		// The node exists (probe resolves it), so the overwrite is gated — --yes
		// bypasses the prompt; the update-by-(memory,loc) attempt then succeeds.
		"ResolveUrn": resolveNodeJSON,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	})
	f, out := testFactory(t)
	file := filepath.Join(t.TempDir(), "flaky.md")
	if err := os.WriteFile(file, []byte(importMd), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "--yes", "--json", "--server", gql.URL})
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

// Importing onto an EXISTING node is an overwrite: non-interactive without --yes
// it's refused (exit 2), and no write is attempted (#129).
func TestNodeImportOverwriteRefusedWithoutYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON, // target already exists
	})
	f, _ := testFactory(t)
	file := filepath.Join(t.TempDir(), "flaky.md")
	if err := os.WriteFile(file, []byte(importMd), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "--server", gql.URL})
	err := root.Execute()
	if err == nil || exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("overwrite without --yes should be a usage error, got %v", err)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error should mention --yes, got %v", err)
	}
	if captured["UpdateNode"] != nil || captured["CreateNode"] != nil {
		t.Error("no write must be attempted when the overwrite is refused")
	}
}

func TestNodeImportTargetPrecedenceAndCreateOnly(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
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
	_ = json.Unmarshal(captured["CreateNode"], &vars)
	if vars.Input["memoryId"] != "acme.com:other" || vars.Input["loc"] != "moved:here" {
		t.Errorf("flags must override frontmatter target: %v", vars.Input)
	}
	// --create-only goes straight to createNode — no update attempt first
	// (a live node at the loc must reject with NodeLocConflictError).
	if _, updated := captured["UpdateNode"]; updated {
		t.Error("--create-only must not attempt UpdateNode")
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
		"ResolveUrn": resolveNullJSON, // new target — no overwrite gate
		"UpdateNode": `{"errors":[{"message":"node not found","extensions":{"code":"NODE_NOT_FOUND"}}]}`,
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
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
	_ = json.Unmarshal(captured["CreateNode"], &vars)
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
	if captured["CreateNode"] != nil || captured["UpdateNode"] != nil {
		t.Error("--dry-run must not call CreateNode/UpdateNode")
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
	const existingEdges = `{"data":{"node":{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI",
		"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"task","tags":[],
		"content":"x","data":null,"seq":null,"createdAt":"2026-06-11T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",
		"outgoingEdges":[{"id":"e0","name":"existing-label","priority":0,"target":{"id":"n2","loc":"start","memoryId":"mem1"}}],
		"incomingEdges":[]}}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"n2","kind":"node","memoryId":"mem1"}}}`,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
		"GetNode":    existingEdges,
		"CreateEdge": `{"data":{"createEdge":` + edgeJSON + `}}`,
	})
	f, out := testFactory(t)
	file := filepath.Join(t.TempDir(), "flaky.md")
	if err := os.WriteFile(file, []byte(importEdgesMd), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	// The node exists (probe resolves it), so overwriting is gated — --yes bypasses.
	root.SetArgs([]string{"node", "import", file, "--with-edges", "--yes", "--json", "--server", gql.URL})
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
	if ce["sourceRef"] != "n1" || ce["targetRef"] != "n2" {
		t.Errorf("createEdge endpoints = %v", ce)
	}
	if ce["priority"] != float64(7) {
		t.Errorf("priority not forwarded: %v", ce["priority"])
	}
	if cond, _ := ce["condition"].(map[string]any); cond["flag"] != "x" {
		t.Errorf("condition not forwarded: %v", ce["condition"])
	}
}

// An edge whose target can't be resolved is reported in unwiredEdges. The node
// is still written and the summary is emitted, but the command now exits
// non-zero so a partial success isn't read as complete (#127).
func TestNodeImportWithEdgesUnwiredTarget(t *testing.T) {
	const noEdges = `{"data":{"node":{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI",
		"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"task","tags":[],
		"content":"x","data":null,"seq":null,"createdAt":"2026-06-11T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":null}}`,                                                     // edge target unresolvable
		"UpdateNode": `{"errors":[{"message":"node not found","extensions":{"code":"NODE_NOT_FOUND"}}]}`, // node absent → created
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
		"GetNode":    noEdges,
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
	err := root.Execute()
	// Partial success — the node was imported but an edge is unwired, so exit 1.
	if err == nil {
		t.Fatal("an import that leaves edges unwired must exit non-zero")
	}
	if code := exitcode.FromError(err); code != exitcode.Error {
		t.Errorf("unwired-edge exit code = %d, want %d (Error)", code, exitcode.Error)
	}
	if captured["CreateEdge"] != nil {
		t.Error("no edge should be created for an unresolvable target")
	}
	// The summary (with unwiredEdges) is still emitted to stdout before the error.
	var summary struct {
		EdgesWired   int `json:"edgesWired"`
		UnwiredEdges []struct {
			Target string `json:"target"`
			Reason string `json:"reason"`
		} `json:"unwiredEdges"`
	}
	if uerr := json.Unmarshal([]byte(out.String()), &summary); uerr != nil {
		t.Fatalf("summary must still be emitted on stdout: %v\n%s", uerr, out.String())
	}
	if summary.EdgesWired != 0 || len(summary.UnwiredEdges) != 1 || summary.UnwiredEdges[0].Target != "ghost" {
		t.Errorf("expected one unwired edge 'ghost', got wired=%d unwired=%+v", summary.EdgesWired, summary.UnwiredEdges)
	}
	if !strings.Contains(summary.UnwiredEdges[0].Reason, "unresolved") {
		t.Errorf("unwired reason should explain the unresolved target, got %q", summary.UnwiredEdges[0].Reason)
	}
}

// An edge the server rejects (e.g. a condition operator outside the v1
// allowlist) is reported with the reason; the node is imported but the command
// exits non-zero to signal the partial failure (#127).
func TestNodeImportWithEdgesRejectedReason(t *testing.T) {
	const noEdges = `{"data":{"node":{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI",
		"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"task","tags":[],
		"content":"x","data":null,"seq":null,"createdAt":"2026-06-11T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}}}`
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"n2","kind":"node","memoryId":"mem1"}}}`,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
		"GetNode":    noEdges,
		"CreateEdge": `{"errors":[{"message":"createEdge operator 'flag' is not in the v1 allowlist"}]}`,
	})
	f, out := testFactory(t)
	file := filepath.Join(t.TempDir(), "edge.md")
	oneEdgeMd := "---\nname: X\nid: n1\nloc: findings:flaky-ci\nmemory: acme.com:kb\nnodes:\n  - id: ne\n    loc: other\n    rel: routes-to\n    condition:\n      flag: x\n---\n\nbody\n"
	if err := os.WriteFile(file, []byte(oneEdgeMd), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	// The node exists (probe resolves it), so overwriting is gated — --yes bypasses.
	root.SetArgs([]string{"node", "import", file, "--with-edges", "--yes", "--json", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Error {
		t.Fatalf("a rejected edge is a partial failure — exit %d, want %d (Error): %v", code, exitcode.Error, err)
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
