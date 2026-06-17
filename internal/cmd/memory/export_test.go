package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

type batchTarget = gen.NodeBatchNodeBatchNodeBatchResultNodesNodeOutgoingEdgesEdgeTargetNode

func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }
func raw(s string) *json.RawMessage {
	r := json.RawMessage(s)
	return &r
}

func oracleHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// TestContentHashMatchesServer pins the algorithm to hadron-server's
// computeContentHash: sha256 hex, first 8 chars; empty content has no hash.
func TestContentHashMatchesServer(t *testing.T) {
	// Known vector: sha256("abc") = ba7816bf...; first 8 hex = "ba7816bf".
	if got := contentHash("abc"); got != "ba7816bf" {
		t.Errorf("contentHash(abc) = %q, want ba7816bf", got)
	}
	if got := contentHash(""); got != "" {
		t.Errorf("contentHash(\"\") = %q, want empty", got)
	}
}

// TestRenderNodeMarkdownGolden locks the full file shape: frontmatter field
// order, the framing (`---\n…\n---\n\n<body>\n`), and the recomputed contentHash.
func TestRenderNodeMarkdownGolden(t *testing.T) {
	content := "# Intro\n\nBody."
	n := &batchNode{
		Id:                 "n-1",
		Loc:                "guide:intro",
		Name:               "Intro",
		NodeType:           "task",
		Alias:              strptr("intro"),
		Description:        strptr("One-liner"),
		Abstract:           strptr("A longer summary."),
		AbstractOriginHash: strptr("deadbeef"),
		Content:            strptr(content),
		Tags:               []string{"guide", "intro"},
		Seq:                intptr(0),
		Data:               raw(`{"k":"v"}`),
		OutgoingEdges: []*batchEdge{
			{Label: "next", Priority: 5, Target: &batchTarget{Id: "n-2", Loc: "guide:more"}},
		},
	}

	want := fmt.Sprintf(`---
name: Intro
id: n-1
alias: intro
type: task
description: One-liner
abstract: A longer summary.
abstractOriginHash: deadbeef
contentHash: %s
tags:
  - guide
  - intro
seq: 0
data:
  k: v
nodes:
  - id: n-2
    loc: guide:more
    rel: next
    priority: 5
---

# Intro

Body.
`, oracleHash(content))

	got, err := renderNodeMarkdown(n)
	if err != nil {
		t.Fatalf("renderNodeMarkdown: %v", err)
	}
	if got != want {
		t.Errorf("rendered markdown mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestBuildFrontmatterOmitRules checks the omit-on-default behavior for a
// minimal node: only name + id, with type omitted for the default `info`.
func TestBuildFrontmatterOmitRules(t *testing.T) {
	n := &batchNode{Id: "x", Loc: "x", Name: "X", NodeType: "info"}
	got, err := renderNodeMarkdown(n)
	if err != nil {
		t.Fatalf("renderNodeMarkdown: %v", err)
	}
	want := "---\nname: X\nid: x\n---\n\n\n"
	if got != want {
		t.Errorf("minimal node mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestBuildEdgeEntries covers the per-edge omit rules and the target guard.
func TestBuildEdgeEntries(t *testing.T) {
	edges := []*batchEdge{
		{Label: "a", Priority: 0, Target: &batchTarget{Id: "t1", Loc: "loc1"}},      // priority 0 omitted
		{Label: "", Priority: 3, Target: &batchTarget{Id: "t2", Loc: ""}},           // empty rel kept, loc omitted
		{Label: "c", Condition: raw(`{"==":[1,1]}`), Target: &batchTarget{Id: "t3"}}, // condition kept
		{Label: "orphan", Target: nil},                                              // no target → skipped
	}
	got := buildEdgeEntries(edges)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3 (orphan skipped)", len(got))
	}
	if got[0].Priority != 0 || got[0].Loc != "loc1" {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1].Rel != "" || got[1].Priority != 3 || got[1].Loc != "" {
		t.Errorf("entry 1 = %+v", got[1])
	}
	if got[2].Condition == nil {
		t.Errorf("entry 2 condition dropped: %+v", got[2])
	}

	if buildEdgeEntries(nil) != nil {
		t.Error("no edges should yield nil (omit nodes: key)")
	}
}

func TestNodeFilePath(t *testing.T) {
	root := "/tmp/kb"
	cases := []struct {
		loc, want string
	}{
		{"", filepath.Join(root, "README.md")},
		{"a", filepath.Join(root, "a.md")},
		{"a:b:c", filepath.Join(root, "a", "b", "c.md")},
		{"msg:010:02", filepath.Join(root, "msg", "010", "02.md")},
	}
	for _, c := range cases {
		got, err := nodeFilePath(root, c.loc)
		if err != nil {
			t.Errorf("nodeFilePath(%q): unexpected err %v", c.loc, err)
			continue
		}
		if got != c.want {
			t.Errorf("nodeFilePath(%q) = %q, want %q", c.loc, got, c.want)
		}
	}

	for _, bad := range []string{"a::b", "a:", ":b", "a:..:b", "a:.:b"} {
		if _, err := nodeFilePath(root, bad); err == nil {
			t.Errorf("nodeFilePath(%q): expected error for unsafe loc", bad)
		}
	}
}

func TestDecodeJSON(t *testing.T) {
	if decodeJSON(nil) != nil {
		t.Error("nil raw should decode to nil")
	}
	if decodeJSON(raw("null")) != nil {
		t.Error("literal null should decode to nil")
	}
	if decodeJSON(raw("  ")) != nil {
		t.Error("blank should decode to nil")
	}
	m, ok := decodeJSON(raw(`{"a":1}`)).(map[string]any)
	if !ok || m["a"] == nil {
		t.Errorf("object decode failed: %#v", decodeJSON(raw(`{"a":1}`)))
	}
}

// --- collectNodeBatch: chunking + truncation + unavailable ---

func nodesWithIDs(ids ...string) []*batchNode {
	out := make([]*batchNode, len(ids))
	for i, id := range ids {
		out[i] = &batchNode{Id: id}
	}
	return out
}

func TestCollectNodeBatchChunksByCap(t *testing.T) {
	ids := make([]string, 450)
	for i := range ids {
		ids[i] = fmt.Sprintf("id-%d", i)
	}
	var chunkSizes []int
	got, unavail, err := collectNodeBatch(ids, func(chunk []string) (*batchResult, error) {
		chunkSizes = append(chunkSizes, len(chunk))
		return &batchResult{Nodes: nodesWithIDs(chunk...)}, nil
	})
	if err != nil {
		t.Fatalf("collectNodeBatch: %v", err)
	}
	if len(got) != 450 {
		t.Fatalf("got %d nodes, want 450", len(got))
	}
	if len(unavail) != 0 {
		t.Errorf("unexpected unavailable: %v", unavail)
	}
	if want := []int{nodeBatchCap, nodeBatchCap, 50}; !equalInts(chunkSizes, want) {
		t.Errorf("chunk sizes = %v, want %v", chunkSizes, want)
	}
}

func TestCollectNodeBatchRequeuesTruncatedOmitted(t *testing.T) {
	ids := []string{"a", "b", "c"}
	calls := 0
	got, _, err := collectNodeBatch(ids, func(chunk []string) (*batchResult, error) {
		calls++
		if calls == 1 {
			// First call: server hit the byte cap — returns one node, omits the rest.
			return &batchResult{Nodes: nodesWithIDs(chunk[0]), Truncated: true, Omitted: chunk[1:]}, nil
		}
		return &batchResult{Nodes: nodesWithIDs(chunk...)}, nil
	})
	if err != nil {
		t.Fatalf("collectNodeBatch: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d nodes, want 3 (omitted re-fetched)", len(got))
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestCollectNodeBatchCollectsUnavailable(t *testing.T) {
	_, unavail, err := collectNodeBatch([]string{"a", "b"}, func(chunk []string) (*batchResult, error) {
		return &batchResult{Nodes: nodesWithIDs("a"), Unavailable: []string{"b"}}, nil
	})
	if err != nil {
		t.Fatalf("collectNodeBatch: %v", err)
	}
	if len(unavail) != 1 || unavail[0] != "b" {
		t.Errorf("unavailable = %v, want [b]", unavail)
	}
}

func TestCollectNodeBatchTruncatedNoProgressErrors(t *testing.T) {
	_, _, err := collectNodeBatch([]string{"a"}, func(chunk []string) (*batchResult, error) {
		// Pathological: truncated but zero nodes returned — must not hang.
		return &batchResult{Nodes: nil, Truncated: true, Omitted: chunk}, nil
	})
	if err == nil {
		t.Fatal("expected error when a truncated call returns no nodes")
	}
}

func TestCollectNodeBatchPropagatesError(t *testing.T) {
	want := errors.New("boom")
	_, _, err := collectNodeBatch([]string{"a"}, func(chunk []string) (*batchResult, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestCollectNodeBatchNilResultErrors(t *testing.T) {
	_, _, err := collectNodeBatch([]string{"a"}, func(chunk []string) (*batchResult, error) {
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected error on nil result")
	}
}

func TestCollectNodeBatchEmpty(t *testing.T) {
	got, unavail, err := collectNodeBatch(nil, func(chunk []string) (*batchResult, error) {
		t.Fatal("fetch should not be called for empty ids")
		return nil, nil
	})
	if err != nil || len(got) != 0 || len(unavail) != 0 {
		t.Errorf("empty input: got=%v unavail=%v err=%v", got, unavail, err)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
