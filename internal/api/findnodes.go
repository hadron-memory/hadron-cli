package api

import (
	"context"
	"fmt"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/api/gqltypes"
)

// ListNode is the shallow node projection the unified `findNodes` field returns
// under hits[].node — id/loc/name/type/tags/seq/isRunnable/updatedAt.
// Canonical urn and nullable inheritedFrom preserve server-authorized provenance
// (cli#1708); they never cause a separate base-memory read. Aliased
// here so callers don't spell the deeply-nested genqlient type name (and so a
// future projection change is a one-line edit), matching the batchNode alias
// pattern in nodedoc.go.
//
// Properties and Data are the two JSONB columns, and they are OPT-IN (#602):
// they arrive only when FindNodesProjected was asked for them, and are nil
// otherwise. A nil here therefore means "not requested", NEVER "the node has
// none" — the two are indistinguishable at this type, which is the whole defect
// #602 was filed for. Read them only on the path that asked for them.
type ListNode = gen.FindNodesWithCommentSummaryFindNodesFindNodesResultHitsNodeHitNode

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
// (the old `nodeSearch`). sortProperty orders by a properties/data JSON path and
// overrides sort when set (#719). All args are optional; nil pointers are omitted
// from the wire so the server applies no constraint. The caller maps GraphQL
// errors through MapError as usual.
func FindNodes(
	ctx context.Context,
	client graphql.Client,
	query *string,
	mode *gen.FindNodesMode,
	filter *gen.NodeFilter,
	sort *gen.NodeSort,
	sortProperty *gqltypes.NodePropertySort,
	limit, offset *int,
) (*FindNodesPage, error) {
	return FindNodesProjected(ctx, client, query, mode, filter, sort, sortProperty, limit, offset, false, false)
}

// FindNodesProjected is FindNodes with the two JSONB columns opt-in (#602).
// Pass withProperties / withData to select `properties` / `data` on the hit
// nodes; each is independent, so a caller that wants only the data envelope
// does not pay for properties. Every other caller should use FindNodes, whose
// projection is deliberately thin — a listing is an index.
//
// The columns are separate arguments rather than one flag because they are
// separate payloads: this is the read half of `--where`'s `field` key, which
// names exactly these two columns, and the symmetry is the point (#602).
func FindNodesProjected(
	ctx context.Context,
	client graphql.Client,
	query *string,
	mode *gen.FindNodesMode,
	filter *gen.NodeFilter,
	sort *gen.NodeSort,
	sortProperty *gqltypes.NodePropertySort,
	limit, offset *int,
	withProperties, withData bool,
) (*FindNodesPage, error) {
	resp, err := gen.FindNodes(ctx, client, query, mode, filter, sort, sortProperty, limit, offset, withProperties, withData)
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
			page.Nodes = append(page.Nodes, ListNodeFromProjection(h.Node))
		}
	}
	return page, nil
}

// FindNodesCommentSummaryProjected uses a distinct opt-in document so the
// ordinary query does not name provider-new fields, even behind @include.
// There is no unfiltered fallback when a provider cannot answer this document.
func FindNodesCommentSummaryProjected(
	ctx context.Context, client graphql.Client, query *string, mode *gen.FindNodesMode,
	filter *gen.NodeFilter, sort *gen.NodeSort, sortProperty *gqltypes.NodePropertySort,
	limit, offset *int, withProperties, withData bool,
) (*FindNodesPage, error) {
	resp, err := gen.FindNodesWithCommentSummary(ctx, client, query, mode, filter, sort, sortProperty, limit, offset, withProperties, withData)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.FindNodes == nil {
		return nil, fmt.Errorf("server returned no comment-summary listing")
	}
	r := resp.FindNodes
	// A null or absent hits list is an incomplete answer, not an empty page;
	// only an explicit [] is empty.
	if r.Hits == nil {
		return nil, fmt.Errorf("server returned no hits list in comment-summary listing")
	}
	page := &FindNodesPage{Nodes: []*ListNode{}, Total: r.Total, Degraded: r.Degraded, Reason: r.Reason}
	for _, h := range r.Hits {
		if h == nil || h.Node == nil {
			return nil, fmt.Errorf("server returned a missing node in comment-summary listing")
		}
		page.Nodes = append(page.Nodes, h.Node)
	}
	return page, nil
}

// ListNodeFromProjection preserves the legacy projection for callers that do
// not opt into feedback. In particular it invents no comment summary.
func ListNodeFromProjection(n *gen.FindNodesFindNodesFindNodesResultHitsNodeHitNode) *ListNode {
	if n == nil {
		return nil
	}
	row := &ListNode{Id: n.Id, Urn: n.Urn, MemoryId: n.MemoryId, Loc: n.Loc, Name: n.Name, NodeType: n.NodeType, Tags: n.Tags, Seq: n.Seq, IsRunnable: n.IsRunnable, Role: n.Role, UpdatedAt: n.UpdatedAt, Properties: n.Properties, Data: n.Data}
	if n.InheritedFrom != nil {
		row.InheritedFrom = &gen.FindNodesWithCommentSummaryFindNodesFindNodesResultHitsNodeHitNodeInheritedFrom{AgentId: n.InheritedFrom.AgentId, AgentUrn: n.InheritedFrom.AgentUrn}
	}
	return row
}
