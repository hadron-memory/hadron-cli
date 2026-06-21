package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMemoryExport drives the whole command: resolve → list → skip data →
// bulk-fetch → write files → summary. The memory ref is a bare id (no colon)
// so resolveMemoryID returns it without a MyMemories round-trip, and one
// node has a short colon-free loc so the manifest is skipped (no GetMemory).
func TestMemoryExport(t *testing.T) {
	const nodesResp = `{"data":{"nodes":[
		{"id":"n-root","memoryId":"mem1","loc":"root","name":"Root","nodeType":"info","tags":[],"updatedAt":"2026-06-11T00:00:00Z"},
		{"id":"n-intro","memoryId":"mem1","loc":"guide:intro","name":"Intro","nodeType":"task","tags":["g"],"updatedAt":"2026-06-11T00:00:00Z"},
		{"id":"n-blob","memoryId":"mem1","loc":"blob","name":"Blob","nodeType":"data","tags":[],"updatedAt":"2026-06-11T00:00:00Z"}
	]}}`
	const batchResp = `{"data":{"nodeBatch":{
		"truncated":false,"omitted":[],"unavailable":[],
		"nodes":[
			{"id":"n-root","memoryId":"mem1","loc":"root","name":"Root","alias":null,"nodeType":"info","description":null,"abstract":null,"abstractOriginHash":null,"tags":[],"seq":null,"data":null,"properties":null,"content":"Root body.","outgoingEdges":[]},
			{"id":"n-intro","memoryId":"mem1","loc":"guide:intro","name":"Intro","alias":null,"nodeType":"task","description":"One liner","abstract":null,"abstractOriginHash":null,"tags":["g"],"seq":null,"data":null,"properties":null,"content":"Intro body.","outgoingEdges":[{"name":"next","priority":0,"condition":null,"target":{"id":"n-root","loc":"root"}}]}
		]
	}}}`

	gql, captured := captureGraphQL(t, map[string]string{
		"Nodes":     nodesResp,
		"NodeBatch": batchResp,
	})
	f, out := testFactory(t)
	dir := t.TempDir()

	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "export", "mem1", "--out", dir, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Summary contract.
	var summary struct {
		NodeCount     int      `json:"nodeCount"`
		SkippedData   int      `json:"skippedData"`
		WroteManifest bool     `json:"wroteManifest"`
		Unavailable   []string `json:"unavailable"`
	}
	if err := json.Unmarshal([]byte(out.String()), &summary); err != nil {
		t.Fatalf("summary not JSON: %v\n%s", err, out.String())
	}
	if summary.NodeCount != 2 || summary.SkippedData != 1 || summary.WroteManifest {
		t.Errorf("summary = %+v, want nodeCount=2 skippedData=1 wroteManifest=false", summary)
	}
	if summary.Unavailable == nil {
		t.Error("unavailable should be [] (stable shape), not null")
	}

	// The data node must be excluded from the batch request.
	var batchVars struct {
		Ids []string `json:"ids"`
	}
	_ = json.Unmarshal(captured["NodeBatch"], &batchVars)
	if strings.Contains(strings.Join(batchVars.Ids, ","), "n-blob") {
		t.Errorf("data node should not be batched, got ids %v", batchVars.Ids)
	}
	if len(batchVars.Ids) != 2 {
		t.Errorf("want 2 batched ids, got %v", batchVars.Ids)
	}

	// Files land at <out>/<loc>.md with colons as path segments.
	rootMd := mustRead(t, filepath.Join(dir, "root.md"))
	if !strings.Contains(rootMd, "name: Root") || !strings.Contains(rootMd, "Root body.") {
		t.Errorf("root.md unexpected:\n%s", rootMd)
	}
	if strings.Contains(rootMd, "type:") {
		t.Error("info node must not emit a type key")
	}

	introMd := mustRead(t, filepath.Join(dir, "guide", "intro.md"))
	for _, want := range []string{"name: Intro", "type: task", "description: One liner", "contentHash:", "nodes:", "id: n-root", "rel: next", "Intro body."} {
		if !strings.Contains(introMd, want) {
			t.Errorf("guide/intro.md missing %q:\n%s", want, introMd)
		}
	}
	// priority 0 is the default — it must be omitted from the edge entry.
	if strings.Contains(introMd, "priority:") {
		t.Errorf("priority 0 should be omitted:\n%s", introMd)
	}

	// The skipped data node must not produce a file.
	if _, err := os.Stat(filepath.Join(dir, "blob.md")); !os.IsNotExist(err) {
		t.Error("data node should not have been written")
	}
}

// TestMemoryExportDefaultsOutToCwd pins issue #31: --out is optional and
// defaults to "." (the current directory). With no --out, files land in cwd
// and the summary reports outDir ".".
func TestMemoryExportDefaultsOutToCwd(t *testing.T) {
	const nodesResp = `{"data":{"nodes":[
		{"id":"n-root","memoryId":"mem1","loc":"root","name":"Root","nodeType":"info","tags":[],"updatedAt":"2026-06-11T00:00:00Z"}
	]}}`
	const batchResp = `{"data":{"nodeBatch":{
		"truncated":false,"omitted":[],"unavailable":[],
		"nodes":[
			{"id":"n-root","memoryId":"mem1","loc":"root","name":"Root","alias":null,"nodeType":"info","description":null,"abstract":null,"abstractOriginHash":null,"tags":[],"seq":null,"data":null,"properties":null,"content":"Root body.","outgoingEdges":[]}
		]
	}}}`

	gql, _ := captureGraphQL(t, map[string]string{
		"Nodes":     nodesResp,
		"NodeBatch": batchResp,
	})
	f, out := testFactory(t)
	dir := t.TempDir()
	t.Chdir(dir) // export should write here when --out is omitted

	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "export", "mem1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var summary struct {
		OutDir string `json:"outDir"`
	}
	if err := json.Unmarshal([]byte(out.String()), &summary); err != nil {
		t.Fatalf("summary not JSON: %v\n%s", err, out.String())
	}
	if summary.OutDir != "." {
		t.Errorf("outDir = %q, want %q (default)", summary.OutDir, ".")
	}
	// The file lands in cwd (the temp dir we chdir'd into).
	if got := mustRead(t, filepath.Join(dir, "root.md")); !strings.Contains(got, "name: Root") {
		t.Errorf("root.md not written to cwd:\n%s", got)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
