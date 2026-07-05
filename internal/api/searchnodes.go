package api

import (
	"context"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// SearchNode is the search-shaped node projection (abstract included).
type SearchNode = gen.SearchNodesFindNodesFindNodesResultHitsNodeHitNode

// SearchHit is one scored search result. Score is nil when the server
// returned an unscored hit; AbstractStale reports the vector index's
// abstract-staleness flag (false when absent).
type SearchHit struct {
	Score         *float64
	AbstractStale bool
	Node          *SearchNode
}

// SearchPage is the scored counterpart of FindNodesPage: it preserves
// per-hit scores rather than flattening to bare nodes.
type SearchPage struct {
	Hits     []*SearchHit
	Total    *int
	Degraded *string
	Reason   *string
}

// SearchNodes runs a ranked findNodes query (the `hadron search` backend),
// keeping per-hit score + vector metadata that FindNodes drops.
func SearchNodes(
	ctx context.Context,
	client graphql.Client,
	query string,
	mode *gen.FindNodesMode,
	filter *gen.NodeFilter,
	limit, offset *int,
) (*SearchPage, error) {
	resp, err := gen.SearchNodes(ctx, client, query, mode, filter, limit, offset)
	if err != nil {
		return nil, err
	}
	page := &SearchPage{Hits: []*SearchHit{}}
	if r := resp.FindNodes; r != nil {
		page.Total = r.Total
		page.Degraded = r.Degraded
		page.Reason = r.Reason
		for _, h := range r.Hits {
			if h == nil || h.Node == nil {
				continue
			}
			hit := &SearchHit{Score: h.Score, Node: h.Node}
			if h.Vector != nil && h.Vector.AbstractStale != nil {
				hit.AbstractStale = *h.Vector.AbstractStale
			}
			page.Hits = append(page.Hits, hit)
		}
	}
	return page, nil
}
