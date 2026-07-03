package api

// PageLimit is the server's hard cap on a single page of the uniform
// paginated list queries (hadron-server#473: limit default 50, cap 200).
const PageLimit = 200

// CollectAll drains a uniform paginated { items, total } list query
// (hadron-server#473) to exhaustion, so "whole scope" commands never
// silently truncate at one server page. fetch receives (limit, offset)
// and returns one page's items plus the envelope total.
func CollectAll[T any](fetch func(limit, offset int) ([]T, int, error)) ([]T, error) {
	items := make([]T, 0)
	for offset := 0; ; offset += PageLimit {
		page, total, err := fetch(PageLimit, offset)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		// A short page is authoritative "no more"; the total guard saves
		// the trailing empty round-trip when a page lands exactly on it.
		if len(page) < PageLimit || len(items) >= total {
			return items, nil
		}
	}
}
