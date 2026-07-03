package api

import (
	"errors"
	"testing"
)

// fakePages serves a fixed item set in PageLimit-sized windows and counts
// the fetch calls.
func fakePages(total int, calls *int) func(limit, offset int) ([]int, int, error) {
	return func(limit, offset int) ([]int, int, error) {
		*calls++
		items := make([]int, 0, limit)
		for i := offset; i < total && i < offset+limit; i++ {
			items = append(items, i)
		}
		return items, total, nil
	}
}

func TestCollectAllDrainsAllPages(t *testing.T) {
	var calls int
	items, err := CollectAll(fakePages(2*PageLimit+1, &calls))
	if err != nil {
		t.Fatalf("CollectAll: %v", err)
	}
	if len(items) != 2*PageLimit+1 {
		t.Errorf("expected %d items, got %d", 2*PageLimit+1, len(items))
	}
	if calls != 3 {
		t.Errorf("expected 3 pages, got %d", calls)
	}
}

func TestCollectAllStopsOnExactTotal(t *testing.T) {
	// A page landing exactly on total must not cost a trailing empty fetch.
	var calls int
	items, err := CollectAll(fakePages(PageLimit, &calls))
	if err != nil {
		t.Fatalf("CollectAll: %v", err)
	}
	if len(items) != PageLimit || calls != 1 {
		t.Errorf("expected %d items in 1 page, got %d in %d", PageLimit, len(items), calls)
	}
}

func TestCollectUntilShortCircuits(t *testing.T) {
	// The sentinel value sits on page 2 of 3 — paging must stop there.
	var calls int
	sentinel := PageLimit + 5
	items, err := CollectUntil(fakePages(3*PageLimit, &calls), func(acc []int) bool {
		for _, v := range acc {
			if v == sentinel {
				return true
			}
		}
		return false
	})
	if err != nil {
		t.Fatalf("CollectUntil: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected the scan to stop after page 2, got %d pages", calls)
	}
	if len(items) != 2*PageLimit {
		t.Errorf("expected the 2 scanned pages' items (%d), got %d", 2*PageLimit, len(items))
	}
}

func TestCollectUntilPropagatesError(t *testing.T) {
	boom := errors.New("boom")
	_, err := CollectUntil(func(limit, offset int) ([]int, int, error) {
		return nil, 0, boom
	}, func([]int) bool { return false })
	if !errors.Is(err, boom) {
		t.Errorf("expected fetch error to propagate, got %v", err)
	}
}
