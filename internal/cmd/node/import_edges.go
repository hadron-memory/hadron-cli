package node

import (
	"encoding/json"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/nodedoc"
)

// wireEdges wires the file's outgoing edges onto the freshly upserted node,
// best-effort and idempotent. Each target is resolved by loc within the target
// memory first (the portable key — it survives re-homing a node into another
// memory, where the source-server id no longer means anything), falling back to
// the frontmatter id. A target that can't be wired (unresolvable, gone, or a
// server-side constraint) is collected into unwired and reported — never fatal.
// An existing (target, label) edge is skipped, so a re-import converges instead
// of stacking duplicates.
func wireEdges(cmd *cobra.Command, client graphql.Client, memoryRef, sourceID string, edges []nodedoc.Edge) (int, []string, error) {
	existing, err := existingEdgeKeys(cmd, client, sourceID)
	if err != nil {
		return 0, nil, err
	}
	wired := 0
	unwired := []string{}
	for _, e := range edges {
		targetID := resolveEdgeTarget(cmd, client, memoryRef, e)
		if targetID == "" {
			unwired = append(unwired, edgeLabel(e))
			continue
		}
		if existing[edgeKey(targetID, e.Label)] {
			continue // idempotent: already wired
		}
		priority, condition, err := edgeArgs(e)
		if err != nil {
			// An un-encodable condition (e.g. a NaN from the file) can't be
			// wired; report it rather than dropping it silently.
			unwired = append(unwired, edgeLabel(e))
			continue
		}
		if _, err := gen.CreateEdge(cmd.Context(), client, sourceID, targetID, e.Label, priority, condition, nil); err != nil {
			// Best-effort: a missing target or edge constraint downgrades to an
			// unwired report rather than aborting the whole import.
			unwired = append(unwired, edgeLabel(e))
			continue
		}
		existing[edgeKey(targetID, e.Label)] = true
		wired++
	}
	return wired, unwired, nil
}

// existingEdgeKeys reads the upserted node's current outgoing edges so wireEdges
// can skip any (target, label) that already exists.
func existingEdgeKeys(cmd *cobra.Command, client graphql.Client, nodeID string) (map[string]bool, error) {
	resp, err := gen.GetNodeById(cmd.Context(), client, nodeID)
	if err != nil {
		return nil, api.MapError(err)
	}
	keys := map[string]bool{}
	if resp.NodeById != nil {
		for _, e := range resp.NodeById.OutgoingEdges {
			if e != nil && e.Target != nil {
				keys[edgeKey(e.Target.Id, e.Label)] = true
			}
		}
	}
	return keys, nil
}

// resolveEdgeTarget resolves an edge's target to a node id: by loc within the
// target memory first (portable across re-homing), then the frontmatter id.
func resolveEdgeTarget(cmd *cobra.Command, client graphql.Client, memoryRef string, e nodedoc.Edge) string {
	if e.TargetLoc != "" && strings.Contains(memoryRef, ":") {
		resp, err := gen.ResolveUrn(cmd.Context(), client, "hrn:node:"+memoryRef+":"+e.TargetLoc)
		if err == nil && resp.ResolveUrn != nil && resp.ResolveUrn.Kind == "node" {
			return resp.ResolveUrn.Id
		}
	}
	return e.TargetID
}

// edgeArgs builds the optional createEdge arguments: priority only when
// non-zero (the server rejects priority: null), condition as a JSON scalar.
func edgeArgs(e nodedoc.Edge) (*int, *json.RawMessage, error) {
	var priority *int
	if e.Priority != 0 {
		p := e.Priority
		priority = &p
	}
	condition, err := nodedoc.EncodeJSON(e.Condition)
	if err != nil {
		return nil, nil, err
	}
	return priority, condition, nil
}

// edgeLabel is a human handle for an unwired edge in the report: prefer the
// target loc, then its id, then the edge label.
func edgeLabel(e nodedoc.Edge) string {
	switch {
	case e.TargetLoc != "":
		return e.TargetLoc
	case e.TargetID != "":
		return e.TargetID
	default:
		return e.Label
	}
}

func edgeKey(targetID, label string) string { return targetID + "\x00" + label }
