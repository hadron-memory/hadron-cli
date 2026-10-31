package spec

import (
	"context"
	"strings"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// replacement is the display state of a spec `spec supersede` retired: the
// persisted convention is the `superseded` tag plus a `superseded-by` edge,
// unchanged by cli#1145. The word shown is "replaced" because SUPERSEDED is
// already the approval state "approved, then edited" and "retired" is the
// repealed-number overlay in allocation; the three are independent.
type replacement struct {
	Replaced bool
	// By is the replacement's loc, nil when there is no edge, its target is not
	// readable, it lies in another memory, or the edges disagree. An identity is
	// never guessed (cli#1145).
	By *string
}

// replacedCandidate is what a listing knows about a node before any edge read.
type replacedCandidate struct {
	ID       string
	Loc      string
	MemoryID string
	Tags     []string
}

// loadReplacements returns the replacement state per node id for the
// candidates carrying the superseded tag. Only those are read again (one bulk
// nodeBatch for their outgoing edges), so a corpus with no retired spec costs
// no extra call. A tagged node whose edges cannot be read is still replaced,
// with no identity.
func loadReplacements(ctx context.Context, client graphql.Client, cands []replacedCandidate) (map[string]replacement, error) {
	out := map[string]replacement{}
	var ids []string
	for _, c := range cands {
		if hasTag(c.Tags, supersededTag) {
			out[c.ID] = replacement{Replaced: true}
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	nodes, _, err := api.CollectNodeBatch(ids, func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
		resp, ferr := gen.NodeBatch(ctx, client, chunk, nil, nil)
		if ferr != nil {
			return nil, api.MapError(ferr)
		}
		if resp == nil {
			return nil, nil
		}
		return resp.NodeBatch, nil
	})
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n == nil {
			continue
		}
		if by, ok := replacedByLoc(nodeFromBatch(n)); ok {
			out[n.Id] = replacement{Replaced: true, By: &by}
		}
	}
	return out, nil
}

// replacedByLoc names the replacement only when it is unambiguous: exactly one
// distinct superseded-by target, readable (it carries a loc) and in the
// node's own memory.
func replacedByLoc(n specNode) (string, bool) {
	var found string
	for _, e := range n.OutEdges {
		if e.Name != supersededByLabel {
			continue
		}
		if e.Loc == "" || e.TargetMemoryID != n.MemoryID {
			return "", false
		}
		if found != "" && found != e.Loc {
			return "", false
		}
		found = e.Loc
	}
	return found, found != ""
}

// replacedMarker is the human suffix: "(replaced by <loc>)" or "(replaced)".
func replacedMarker(r replacement, shortenTo string) string {
	if !r.Replaced {
		return ""
	}
	if r.By == nil {
		return " (replaced)"
	}
	by := *r.By
	if shortenTo != "" && strings.HasPrefix(by, shortenTo+":") {
		by = strings.TrimPrefix(by, shortenTo+":")
	}
	return " (replaced by " + by + ")"
}
