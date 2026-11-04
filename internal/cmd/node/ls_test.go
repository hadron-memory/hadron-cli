package node

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

// serveNodes answers fetches over `total` nodes, never more than `serve` rows a
// page whatever limit was asked for (a server clamp, cli#967), and reports the
// total only when asked to.
func serveNodes(total, serve int, reportTotal bool, calls *int) func(limit, offset int) ([]*api.ListNode, *int, error) {
	return func(limit, offset int) ([]*api.ListNode, *int, error) {
		*calls++
		n := limit
		if serve < n {
			n = serve
		}
		page := []*api.ListNode{}
		for i := offset; i < total && i < offset+n; i++ {
			page = append(page, &api.ListNode{Loc: fmt.Sprintf("m:%d", i)})
		}
		if !reportTotal {
			return page, nil, nil
		}
		return page, &total, nil
	}
}

// paginateAllNodes must page past the server's default first page (#319) AND
// past a page the server clamps below the size asked for (cli#967): the loop
// stops on an empty page or a reached total, never on a short page.
func TestPaginateAllNodesPastAClampedPage(t *testing.T) {
	for _, known := range []bool{false, true} {
		var calls int
		got, err := paginateAllNodes(serveNodes(1003, 200, known, &calls))
		if err != nil {
			t.Fatalf("known=%v: %v", known, err)
		}
		if len(got) != 1003 {
			t.Fatalf("known=%v: collected %d nodes, want 1003", known, len(got))
		}
		for i, n := range got {
			if n.Loc != fmt.Sprintf("m:%d", i) {
				t.Fatalf("known=%v: row %d is %s: skipped or duplicated", known, i, n.Loc)
			}
		}
	}
}

func TestPaginateAllNodesRequestsLsPageSize(t *testing.T) {
	var asked []int
	_, err := paginateAllNodes(func(limit, offset int) ([]*api.ListNode, *int, error) {
		asked = append(asked, limit)
		return nil, nil, nil
	})
	if err != nil || len(asked) != 1 || asked[0] != lsPageSize {
		t.Fatalf("asked %v err %v, want one request of %d", asked, err, lsPageSize)
	}
}

func TestPaginateAllNodesLaterPageErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	n := 0
	got, err := paginateAllNodes(func(limit, offset int) ([]*api.ListNode, *int, error) {
		n++
		if n == 2 {
			return nil, nil, boom
		}
		return []*api.ListNode{{Loc: "m:1"}}, nil, nil
	})
	if !errors.Is(err, boom) || got != nil {
		t.Fatalf("got %v, %v; want nil + boom (a partial list must not pass as complete)", got, err)
	}
}
