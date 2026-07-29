package node

import (
	"fmt"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

// paginateAllNodes must page past the server's default first page — the #319
// bug was that --seq-gt/--sort-seq only ever saw one page. It stops on the
// first short page.
func TestPaginateAllNodes(t *testing.T) {
	// Two full pages then a short one: 500 + 500 + 3 = 1003 nodes across 3 calls.
	total := 2*lsPageSize + 3
	var calls int
	got, err := paginateAllNodes(func(limit, offset int) ([]*api.ListNode, error) {
		calls++
		if limit != lsPageSize {
			t.Fatalf("fetch called with limit %d, want %d", limit, lsPageSize)
		}
		n := total - offset
		if n > lsPageSize {
			n = lsPageSize
		}
		if n < 0 {
			n = 0
		}
		page := make([]*api.ListNode, n)
		for i := range page {
			page[i] = &api.ListNode{Loc: fmt.Sprintf("m:%d", offset+i)}
		}
		return page, nil
	})
	if err != nil {
		t.Fatalf("paginateAllNodes: %v", err)
	}
	if len(got) != total {
		t.Errorf("collected %d nodes, want %d", len(got), total)
	}
	if calls != 3 {
		t.Errorf("made %d fetch calls, want 3 (two full pages + one short)", calls)
	}
}

// A single short page stops after one call (the common small-collection case).
func TestPaginateAllNodesSinglePage(t *testing.T) {
	var calls int
	got, err := paginateAllNodes(func(limit, offset int) ([]*api.ListNode, error) {
		calls++
		return []*api.ListNode{{Loc: "m:1"}, {Loc: "m:2"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || calls != 1 {
		t.Errorf("got %d nodes in %d calls, want 2 in 1", len(got), calls)
	}
}
