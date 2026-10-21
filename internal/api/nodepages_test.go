package api

import (
	"errors"
	"fmt"
	"testing"
)

// serveListNodes answers over total nodes, never more than serve per page.
func serveListNodes(total, serve int, calls *int) func(limit, offset int) ([]*ListNode, error) {
	return func(limit, offset int) ([]*ListNode, error) {
		*calls++
		n := limit
		if serve < n {
			n = serve
		}
		page := []*ListNode{}
		for i := offset; i < total && i < offset+n; i++ {
			page = append(page, &ListNode{Id: fmt.Sprintf("id%d", i), Loc: fmt.Sprintf("l:%d", i)})
		}
		return page, nil
	}
}

func TestPageNodesNeverStopsOnAShortPage(t *testing.T) {
	var calls int
	var got []string
	err := PageNodes(500, serveListNodes(130, 50, &calls), func(n *ListNode) bool {
		got = append(got, n.Loc)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 130 {
		t.Fatalf("visited %d nodes, want 130 (stopped on a short page)", len(got))
	}
	for i, l := range got {
		if l != fmt.Sprintf("l:%d", i) {
			t.Fatalf("row %d is %s: skipped or duplicated", i, l)
		}
	}
	if calls != 4 {
		t.Errorf("want 4 requests (50+50+30+empty), got %d", calls)
	}
}

func TestPageNodesVisitCanStopEarly(t *testing.T) {
	var calls int
	err := PageNodes(500, serveListNodes(130, 50, &calls), func(n *ListNode) bool { return n.Loc != "l:60" })
	if err != nil || calls != 2 {
		t.Fatalf("err=%v calls=%d, want a stop on page 2", err, calls)
	}
}

func TestPageNodesLaterPageErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	n := 0
	err := PageNodes(500, func(limit, offset int) ([]*ListNode, error) {
		n++
		if n == 2 {
			return nil, boom
		}
		return []*ListNode{{Id: "a"}}, nil
	}, func(*ListNode) bool { return true })
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
}

// A server that ignores the offset returns the same page forever: stop, with
// each node visited once.
func TestPageNodesStopsWhenThePageRepeats(t *testing.T) {
	visits := 0
	err := PageNodes(500, func(limit, offset int) ([]*ListNode, error) {
		return []*ListNode{{Id: "a"}, {Id: "b"}}, nil
	}, func(*ListNode) bool { visits++; return true })
	if err != nil || visits != 2 {
		t.Fatalf("err=%v visits=%d, want each node once", err, visits)
	}
}
