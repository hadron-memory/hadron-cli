package node

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

func locPages(total, serve int) func(limit, offset int) ([]*api.ListNode, error) {
	return func(limit, offset int) ([]*api.ListNode, error) {
		n := limit
		if serve < n {
			n = serve
		}
		page := []*api.ListNode{}
		for i := offset; i < total && i < offset+n; i++ {
			page = append(page, &api.ListNode{Id: fmt.Sprintf("id%d", i), Loc: fmt.Sprintf("p:%d", i)})
		}
		return page, nil
	}
}

// cli#967: the overwrite probe must find a node behind a clamped page.
func TestFindExactLocPastAClampedPage(t *testing.T) {
	id, ok := findExactLoc("p:120", locPages(130, 50))
	if !ok || id != "id120" {
		t.Fatalf("got %q %v, want id120 true (stopped on a short page)", id, ok)
	}
	if _, ok := findExactLoc("p:999", locPages(130, 50)); ok {
		t.Fatal("a loc that does not exist must read as not found")
	}
}

func TestFindExactLocFetchErrorReadsAsNotFound(t *testing.T) {
	_, ok := findExactLoc("p:1", func(limit, offset int) ([]*api.ListNode, error) { return nil, errors.New("boom") })
	if ok {
		t.Fatal("a fetch error must read as not found (best-effort probe)")
	}
}
