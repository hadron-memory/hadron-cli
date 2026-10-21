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
	id, ok, err := findExactLoc("p:120", locPages(130, 50))
	if err != nil || !ok || id != "id120" {
		t.Fatalf("got %q %v %v, want id120 true nil (stopped on a short page)", id, ok, err)
	}
	if _, ok, err := findExactLoc("p:999", locPages(130, 50)); ok || err != nil {
		t.Fatalf("a loc that does not exist must read as plain absence, got ok=%v err=%v", ok, err)
	}
}

// A later-page failure is an error, distinct from true absence: the probe must
// not tell an import it is safe to create (cli#967, Gene's P2-2).
func TestFindExactLocLaterPageFailureIsAnErrorNotAbsence(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	_, ok, err := findExactLoc("p:999", func(limit, offset int) ([]*api.ListNode, error) {
		calls++
		if calls == 2 {
			return nil, boom
		}
		return locPages(130, 50)(limit, offset)
	})
	if ok || !errors.Is(err, boom) {
		t.Fatalf("want ok=false err=boom, got ok=%v err=%v", ok, err)
	}
}
