package api

import (
	"context"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// ListNode is the shallow node projection the unified `findNodes` field returns
// under hits[].node — id/loc/name/type/tags/seq/isRunnable/updatedAt. Aliased
// here so callers don't spell the deeply-nested genqlient type name (and so a
// future projection change is a one-line edit), matching the batchNode alias
// pattern in nodedoc.go.
type ListNode = gen.FindNodesFindNodesFindNodesResultHitsNodeHitNode

// FindNodesPage is the flattened result of one findNodes call: the hit nodes
// (hits[].node hoisted to a bare slice, the shape every old `nodes`/`nodeSearch`
// caller expects), plus the envelope's total and the degraded/reason notes that
// `spec find` surfaces on a vector-less memory. Nodes is always non-nil.
type FindNodesPage struct {
	Nodes    []*ListNode
	Total    *int
	Degraded *string
	Reason   *string
}

// FindNodes runs the unified node search/list (cor:api:090) and flattens the
// hits[].node envelope into a bare node slice. Omit query for a filtered list
// in deterministic order (the old `nodes` semantics); pass query + mode to rank
// (the old `nodeSearch`). All args are optional; nil pointers are omitted from
// the wire so the server applies no constraint. The caller maps GraphQL errors
// through MapError as usual.
func FindNodes(
	ctx context.Context,
	client graphql.Client,
	query *string,
	mode *gen.FindNodesMode,
	filter *gen.NodeFilter,
	sort *gen.NodeSort,
	limit, offset *int,
) (*FindNodesPage, error) {
	resp, err := gen.FindNodes(ctx, client, query, mode, filter, sort, limit, offset)
	if err != nil {
		return nil, err
	}
	page := &FindNodesPage{Nodes: []*ListNode{}}
	if r := resp.FindNodes; r != nil {
		page.Total = r.Total
		page.Degraded = r.Degraded
		page.Reason = r.Reason
		for _, h := range r.Hits {
			if h == nil || h.Node == nil {
				continue
			}
			page.Nodes = append(page.Nodes, h.Node)
		}
	}
	return page, nil
}
