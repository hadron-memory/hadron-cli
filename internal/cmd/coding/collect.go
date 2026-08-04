package coding

import (
	"context"

	"github.com/Khan/genqlient/graphql"
)

// Reading the graph, once. `review lint` and `review list` answer different
// questions about the same corpus — which checks are broken, and which checks
// exist — so they must agree on what a check IS. Sharing the collection step
// (and, for the status column, the rule engine itself) is what keeps them from
// drifting: a node `list` shows and `lint` never sees would be exactly the
// silent skip this command group exists to detect.

// collectReview gathers the review tree: the checklist members under the root's
// child prefix, each one's edge back to the parent, and the endpoints that
// could not be read.
//
// Toolchain and Suggest are lint-only knobs and are left zero here; the lint
// command sets them on the returned value.
func collectReview(ctx context.Context, client graphql.Client, mem codingMemory, root string) (reviewInput, error) {
	edges, _, err := fetchRootEdges(ctx, client, mem, root, true)
	if err != nil {
		return reviewInput{}, err
	}

	// Two sources of candidate checks: nodes under the root's child prefix
	// (which catches a check with no edge at all — the highest-severity
	// finding), and the far endpoints of the parent's inbound edges (which
	// catches one that is unreadable).
	listed, err := scanPrefix(ctx, client, mem, childPrefix(root))
	if err != nil {
		return reviewInput{}, err
	}
	candidates := map[string]string{} // node id → loc
	for _, n := range listed {
		if n == nil {
			continue
		}
		if isChecklistItemListing(root, n.Loc, n.Tags, n.IsRunnable) {
			candidates[n.Id] = n.Loc
		}
	}
	edgeByLoc := map[string]graphEdge{}
	var redacted []graphEdge
	for _, e := range edges {
		if e.OtherID == "" {
			// The server redacted the endpoint projection, so there is no node
			// to read or classify — report it rather than letting it vanish
			// from the sweep.
			redacted = append(redacted, e)
			continue
		}
		if e.Other != "" {
			edgeByLoc[e.Other] = e
		}
		candidates[e.OtherID] = e.Other
	}

	nodes, unavailable, err := fetchNodes(ctx, client, candidates, true)
	if err != nil {
		return reviewInput{}, err
	}
	for _, e := range redacted {
		unavailable = append(unavailable, e.endpointName())
	}

	members := map[string]checkNode{}
	for loc, n := range nodes {
		if isChecklistItem(root, n) {
			members[loc] = n
		}
	}
	// An unreadable node can't be tested against the predicate, so its
	// membership is indeterminate — reported, never dropped.
	return reviewInput{Members: members, Edges: edgeByLoc, Unavailable: unavailable}, nil
}

// collectPreflight gathers the router's outgoing routes and their targets.
func collectPreflight(ctx context.Context, client graphql.Client, mem codingMemory, root string) (preflightInput, error) {
	// Outgoing, so the far endpoint is the edge's target — the opposite end
	// from the review tree's inbound edges. The root's own memory id comes back
	// too: it is the only value comparable against an endpoint's MemoryId.
	routes, homeMemory, err := fetchRootEdges(ctx, client, mem, root, false)
	if err != nil {
		return preflightInput{}, err
	}
	// Read targets by id, so a route that legitimately crosses into another
	// memory resolves there instead of being looked up in this one.
	byID := map[string]string{}
	for _, r := range routes {
		if r.OtherID != "" {
			byID[r.OtherID] = r.Other
		}
	}
	targets, unavailable, err := fetchNodes(ctx, client, byID, false)
	if err != nil {
		return preflightInput{}, err
	}
	return preflightInput{
		Routes: routes, Targets: targets, Unavailable: unavailable, HomeMemory: homeMemory,
	}, nil
}

// brokenNodes is the set of nodes carrying an error-severity finding — what the
// `list` commands render as the `broken` status, derived from the SAME rule
// engine the linter runs so the two can never disagree.
func brokenNodes(findings []findingDTO) map[string]bool {
	out := map[string]bool{}
	for _, f := range findings {
		if f.Severity == sevError {
			out[f.Node] = true
		}
	}
	return out
}
