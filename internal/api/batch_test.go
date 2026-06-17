package api

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

type (
	batchResult = gen.NodeBatchNodeBatchNodeBatchResult
	batchNode   = gen.NodeBatchNodeBatchNodeBatchResultNodesNode
)

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
	got, unavail, err := CollectNodeBatch(ids, func(chunk []string) (*batchResult, error) {
		chunkSizes = append(chunkSizes, len(chunk))
		return &batchResult{Nodes: nodesWithIDs(chunk...)}, nil
	})
	if err != nil {
		t.Fatalf("CollectNodeBatch: %v", err)
	}
	if len(got) != 450 {
		t.Fatalf("got %d nodes, want 450", len(got))
	}
	if len(unavail) != 0 {
		t.Errorf("unexpected unavailable: %v", unavail)
	}
	if want := []int{NodeBatchCap, NodeBatchCap, 50}; !equalInts(chunkSizes, want) {
		t.Errorf("chunk sizes = %v, want %v", chunkSizes, want)
	}
}

func TestCollectNodeBatchRequeuesTruncatedOmitted(t *testing.T) {
	ids := []string{"a", "b", "c"}
	calls := 0
	got, _, err := CollectNodeBatch(ids, func(chunk []string) (*batchResult, error) {
		calls++
		if calls == 1 {
			// First call: server hit the byte cap — returns one node, omits the rest.
			return &batchResult{Nodes: nodesWithIDs(chunk[0]), Truncated: true, Omitted: chunk[1:]}, nil
		}
		return &batchResult{Nodes: nodesWithIDs(chunk...)}, nil
	})
	if err != nil {
		t.Fatalf("CollectNodeBatch: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d nodes, want 3 (omitted re-fetched)", len(got))
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestCollectNodeBatchCollectsUnavailable(t *testing.T) {
	_, unavail, err := CollectNodeBatch([]string{"a", "b"}, func(chunk []string) (*batchResult, error) {
		return &batchResult{Nodes: nodesWithIDs("a"), Unavailable: []string{"b"}}, nil
	})
	if err != nil {
		t.Fatalf("CollectNodeBatch: %v", err)
	}
	if len(unavail) != 1 || unavail[0] != "b" {
		t.Errorf("unavailable = %v, want [b]", unavail)
	}
}

func TestCollectNodeBatchTruncatedNoProgressErrors(t *testing.T) {
	_, _, err := CollectNodeBatch([]string{"a"}, func(chunk []string) (*batchResult, error) {
		// Pathological: truncated but zero nodes returned — must not hang.
		return &batchResult{Nodes: nil, Truncated: true, Omitted: chunk}, nil
	})
	if err == nil {
		t.Fatal("expected error when a truncated call returns no nodes")
	}
}

func TestCollectNodeBatchPropagatesError(t *testing.T) {
	want := errors.New("boom")
	_, _, err := CollectNodeBatch([]string{"a"}, func(chunk []string) (*batchResult, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestCollectNodeBatchNilResultErrors(t *testing.T) {
	_, _, err := CollectNodeBatch([]string{"a"}, func(chunk []string) (*batchResult, error) {
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected error on nil result")
	}
}

func TestCollectNodeBatchEmpty(t *testing.T) {
	got, unavail, err := CollectNodeBatch(nil, func(chunk []string) (*batchResult, error) {
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
