package api

import "fmt"

// PageLimit is the server's hard cap on a single page of the uniform
// paginated list queries (hadron-server#473: cap 200; the server's own default is not relied on, cli#967).
const PageLimit = 200

// CollectAll drains a uniform paginated { items, total } list query
// (hadron-server#473) to exhaustion, so "whole scope" commands never
// silently truncate at one server page. fetch receives (limit, offset)
// and returns one page's items plus the envelope total.
func CollectAll[T any](fetch func(limit, offset int) ([]T, int, error)) ([]T, error) {
	return CollectUntil(fetch, func([]T) bool { return false })
}

// CollectUntil is CollectAll with an early exit: after each page, done
// receives the items accumulated so far, and returning true stops paging
// (the accumulated items are returned as-is). Use it when a caller can
// tell mid-scan that it already has what it needs — e.g. an exact match
// in a sorted list — without draining the remaining pages.
func CollectUntil[T any](fetch func(limit, offset int) ([]T, int, error), done func(items []T) bool) ([]T, error) {
	items := make([]T, 0)
	for offset := 0; ; {
		page, total, err := fetch(PageLimit, offset)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		// Advance by what was actually served — a page shorter than the
		// asked-for limit (a lower enforced cap, concurrent deletes) must
		// not skip rows. Termination is total-primary; the empty-page check
		// is the safety break when total overstates what the server will
		// actually serve.
		offset += len(page)
		if done(items) || len(items) >= total || len(page) == 0 {
			return items, nil
		}
	}
}

// CollectPages drains a list whose total may be unknown (findNodes returns a
// nullable total; sessions and revisions return none). fetch receives
// (limit, offset) and returns one page plus the total when the server gave one.
// It stops only on an empty page or when a known total is reached, and advances
// by the rows actually received, so a server that clamps pages below pageSize
// can neither end the scan early nor skip rows (cor:api:120:04). A fetch error
// on any page fails the whole collection.
func CollectPages[T any](pageSize int, fetch func(limit, offset int) ([]T, *int, error)) ([]T, error) {
	items := make([]T, 0)
	for offset := 0; ; {
		page, total, err := fetch(pageSize, offset)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		offset += len(page)
		if len(page) == 0 || (total != nil && len(items) >= *total) {
			return items, nil
		}
	}
}

// InventoryPageSize follows the platform's default list limit; inventory
// commands have no user-requested larger page size.
const InventoryPageSize = 20

// CollectHasMore reads a complete offset inventory. Only hasMore ends traversal;
// neither total nor a short page proves that the inventory is complete. Total
// changes and repeated identities detect some concurrent drift, not a snapshot:
// equal-count changes without a repeated identity remain undetectable. A nil
// identity lets the caller retain its existing nullable-row handling.
// Do not return partial rows on failure: callers must not print a partial list.
func CollectHasMore[T any](fetch func(offset int) ([]T, int, bool, error), identity func(T) *string) ([]T, error) {
	rows := []T{}
	seen := map[string]struct{}{}
	initialTotal := 0
	for offset := 0; ; {
		items, total, more, err := fetch(offset)
		if err != nil {
			return nil, err
		}
		if offset == 0 {
			initialTotal = total
		} else if total != initialTotal {
			return nil, fmt.Errorf("the inventory changed while paging; retry after changes settle (no partial inventory)")
		}
		for _, item := range items {
			id := identity(item)
			if id == nil {
				continue
			}
			if _, exists := seen[*id]; exists {
				return nil, fmt.Errorf("the inventory changed while paging (repeated ID); retry after changes settle (no partial inventory)")
			}
			seen[*id] = struct{}{}
		}
		rows = append(rows, items...)
		if !more {
			return rows, nil
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("the server returned an empty inventory page with hasMore=true")
		}
		offset += len(items)
	}
}
