package api

// PageNodes drains a findNodes-shaped listing, calling visit once per node
// until visit returns false or the list is exhausted. fetch receives
// (limit, offset).
//
// It stops only on an EMPTY page: the server may serve fewer rows than asked
// for (a clamp, a concurrent delete) and findNodes.total is nullable, so a short
// page proves nothing (cli#967, cor:api:120:04). It advances by the rows actually
// received, visits each node id once, and stops when a page brings nothing new
// (a server ignoring the offset) rather than looping. A fetch error on any
// page is returned.
func PageNodes(pageSize int, fetch func(limit, offset int) ([]*ListNode, error), visit func(*ListNode) bool) error {
	seen := map[string]bool{}
	for offset := 0; ; {
		page, err := fetch(pageSize, offset)
		if err != nil {
			return err
		}
		fresh := 0
		for _, n := range page {
			if n == nil {
				continue
			}
			key := n.Id
			if key == "" {
				key = n.Loc // a projection without ids still has unique locs
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			fresh++
			if !visit(n) {
				return nil
			}
		}
		if len(page) == 0 || fresh == 0 {
			return nil
		}
		offset += len(page)
	}
}
