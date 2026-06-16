package spec

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// fakeServer holds n loc-shaped nodes and serves offset/limit windows the way
// the GraphQL nodes resolver does (slice past the end → empty), so the
// paginateNodes loop is exercised against realistic page boundaries.
func fakeServer(n int) []*gen.NodesNodesNode {
	out := make([]*gen.NodesNodesNode, n)
	for i := range out {
		out[i] = &gen.NodesNodesNode{Loc: fmt.Sprintf("msg:%03d", i)}
	}
	return out
}

func pageOf(all []*gen.NodesNodesNode, offset, limit int) []*gen.NodesNodesNode {
	if offset >= len(all) {
		return nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end]
}

func sameInts(a, b []int) bool {
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

func TestPaginateNodesExhaustsAllPages(t *testing.T) {
	// The #23 regression: a single unbounded Nodes call stopped at the
	// server's default page and silently dropped the tail. A corpus larger
	// than one page must come back whole, fetched page by page.
	total := nodesPageSize + 11 // a full page plus the truncated tail
	server := fakeServer(total)
	var offsets []int
	got, err := paginateNodes(func(limit, offset int) ([]*gen.NodesNodesNode, error) {
		if limit != nodesPageSize {
			t.Fatalf("page limit = %d, want %d", limit, nodesPageSize)
		}
		offsets = append(offsets, offset)
		return pageOf(server, offset, limit), nil
	})
	if err != nil {
		t.Fatalf("paginateNodes: %v", err)
	}
	if len(got) != total {
		t.Fatalf("got %d nodes, want %d (tail dropped → #23 regressed)", len(got), total)
	}
	if want := []int{0, nodesPageSize}; !sameInts(offsets, want) {
		t.Errorf("offsets requested = %v, want %v", offsets, want)
	}
	// The aggregated set must be the whole corpus, in order, not a repeated page.
	for i, n := range got {
		if n.Loc != server[i].Loc {
			t.Fatalf("node %d = %q, want %q", i, n.Loc, server[i].Loc)
		}
	}
}

func TestPaginateNodesShortFirstPage(t *testing.T) {
	// A corpus that fits in one page is a single request — no needless
	// second round-trip when the first page already signals the tail.
	server := fakeServer(12)
	pages := 0
	got, err := paginateNodes(func(limit, offset int) ([]*gen.NodesNodesNode, error) {
		pages++
		return pageOf(server, offset, limit), nil
	})
	if err != nil {
		t.Fatalf("paginateNodes: %v", err)
	}
	if len(got) != 12 || pages != 1 {
		t.Errorf("got %d nodes in %d page(s), want 12 in 1", len(got), pages)
	}
}

func TestPaginateNodesExactBoundary(t *testing.T) {
	// An exact-multiple corpus does one extra request that returns an empty
	// page, then stops — proving termination (no infinite loop) and that a
	// full final page is not mistaken for the end.
	server := fakeServer(2 * nodesPageSize)
	pages := 0
	got, err := paginateNodes(func(limit, offset int) ([]*gen.NodesNodesNode, error) {
		pages++
		if pages > 4 {
			t.Fatal("paginateNodes did not terminate")
		}
		return pageOf(server, offset, limit), nil
	})
	if err != nil {
		t.Fatalf("paginateNodes: %v", err)
	}
	if len(got) != 2*nodesPageSize {
		t.Fatalf("got %d nodes, want %d", len(got), 2*nodesPageSize)
	}
	if pages != 3 { // two full pages + one empty tail page
		t.Errorf("pages fetched = %d, want 3 (two full + empty tail)", pages)
	}
}

func TestPaginateNodesPropagatesError(t *testing.T) {
	want := errors.New("boom")
	_, err := paginateNodes(func(limit, offset int) ([]*gen.NodesNodesNode, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}
