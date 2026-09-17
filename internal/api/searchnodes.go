package api

import (
	"context"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/api/gqltypes"
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

// SearchScope is the scope a search actually ran under, as the SERVER
// resolved it (spec 049). Nil when no scope applied.
//
// Source is which rung of the resolution ladder won, and DroppedCount is how
// many of the scope's memories the caller may not read. Both are reported
// rather than derived: the ladder is the server's, and a client that inferred
// either would be a second implementation of it.
type SearchScope = gen.SearchNodesFindNodesFindNodesResultScopeSearchScopeInfo

// SearchPage is the scored counterpart of FindNodesPage: it preserves
// per-hit scores rather than flattening to bare nodes.
type SearchPage struct {
	Hits     []*SearchHit
	Total    *int
	Degraded *string
	Reason   *string
	Scope    *SearchScope
}

// SearchNodes runs a ranked findNodes query (the `hadron search` backend),
// keeping per-hit score + vector metadata that FindNodes drops. sortProperty
// orders by a properties/data JSON path and overrides the mode ranking window
// when set (#719); nil pointers are omitted from the wire. scope names the
// search lens — a scope id, a bare name, `app` or `global` — and is resolved
// server-side; the resolution it reports comes back on SearchPage.Scope.
//
// The two context arguments are for DIFFERENT scope forms and are not
// interchangeable: appRef is REQUIRED for `scope: "app"` and for a bare scope
// NAME (a name is unique only per owner), while orgID selects the member's
// "active organization" view (cor:api:100:01), which is what `scope: "global"`
// resolves against.
func SearchNodes(
	ctx context.Context,
	client graphql.Client,
	query string,
	mode *gen.FindNodesMode,
	filter *gen.NodeFilter,
	sortProperty *gqltypes.NodePropertySort,
	limit, offset *int,
	scope *string,
	orgID *string,
	appRef *string,
) (*SearchPage, error) {
	return SearchNodesProjected(ctx, client, query, mode, filter, sortProperty, limit, offset, scope, orgID, appRef, false, false)
}

// SearchNodesProjected is SearchNodes with the two JSONB columns opt-in (#602),
// so a ranked result can show the field a --where predicate selected on. Each
// column is independent; callers that want neither should use SearchNodes,
// whose projection is deliberately thin.
//
// withProperties and withData are ADJACENT BOOLEANS, which is the same hazard
// the scope/appRef pair carries below: transposing them compiles and silently
// asks for the other column. The command tests assert each flag selects its own
// column and NOT its neighbour, which is what catches it.
func SearchNodesProjected(
	ctx context.Context,
	client graphql.Client,
	query string,
	mode *gen.FindNodesMode,
	filter *gen.NodeFilter,
	sortProperty *gqltypes.NodePropertySort,
	limit, offset *int,
	scope *string,
	orgID *string,
	appRef *string,
	withProperties, withData bool,
) (*SearchPage, error) {
	// NOTE the argument ORDER: the generated signature follows the operation's
	// variable order (scope, appRef, orgId), and both context arguments are
	// *string — so transposing them COMPILES and silently sends each as the
	// other. Caught by a test asserting the pair on the wire, not by the build.
	resp, err := gen.SearchNodes(ctx, client, query, mode, filter, sortProperty, limit, offset, scope, appRef, orgID, withProperties, withData)
	if err != nil {
		return nil, err
	}
	page := &SearchPage{Hits: []*SearchHit{}}
	if r := resp.FindNodes; r != nil {
		page.Total = r.Total
		page.Degraded = r.Degraded
		page.Reason = r.Reason
		page.Scope = r.Scope
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
