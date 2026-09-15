package node

import (
	"encoding/json"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
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
func wireEdges(cmd *cobra.Command, client graphql.Client, memoryRef, sourceID string, edges []nodedoc.Edge) (int, []unwiredEdgeDTO, error) {
	existing, err := existingEdgeKeys(cmd, client, sourceID)
	if err != nil {
		return 0, nil, err
	}
	wired := 0
	unwired := []unwiredEdgeDTO{}
	for _, e := range edges {
		targetID := resolveEdgeTarget(cmd, client, memoryRef, e)
		if targetID == "" {
			// Unresolvable now — but a just-created target may simply not be
			// resolveUrn-indexed yet (eventually consistent), so the reason
			// doesn't claim it's gone for good.
			unwired = append(unwired, unwiredEdgeDTO{Target: edgeLabel(e), Reason: "target unresolved (not found, or not yet indexed)"})
			continue
		}
		if existing[edgeKey(targetID, e.Name)] {
			continue // idempotent: already wired
		}
		priority, condition, err := edgeArgs(e)
		if err != nil {
			// An un-encodable condition (e.g. a NaN from the file).
			unwired = append(unwired, unwiredEdgeDTO{Target: edgeLabel(e), Reason: "invalid condition: " + err.Error()})
			continue
		}
		loc, desc := e.Loc, e.Description
		var locArg, descArg *string
		if loc != "" {
			locArg = &loc
		}
		if desc != "" {
			descArg = &desc
		}
		var runArg *bool
		if e.IsRunnable {
			run := true
			runArg = &run
		}
		if _, err := gen.CreateEdge(cmd.Context(), client, sourceID, targetID, e.Name, locArg, descArg, runArg, priority, condition, nil); err != nil {
			// Best-effort: a server-side rejection (e.g. a condition operator
			// outside the v1 allowlist) downgrades to a report, never fatal.
			unwired = append(unwired, unwiredEdgeDTO{Target: edgeLabel(e), Reason: "rejected: " + edgeRejectReason(err)})
			continue
		}
		existing[edgeKey(targetID, e.Name)] = true
		wired++
	}
	return wired, unwired, nil
}

// existingEdgeKeys reads the upserted node's current outgoing edges so wireEdges
// can skip any (target, label) that already exists.
func existingEdgeKeys(cmd *cobra.Command, client graphql.Client, nodeID string) (map[string]bool, error) {
	resp, err := gen.GetNode(cmd.Context(), client, nodeID)
	if err != nil {
		return nil, api.MapError(err)
	}
	keys := map[string]bool{}
	if resp.Node != nil {
		for _, e := range resp.Node.OutgoingEdges {
			if e != nil && e.Target != nil {
				name := ""
				if e.Name != nil {
					name = *e.Name
				}
				keys[edgeKey(e.Target.Id, name)] = true
			}
		}
	}
	return keys, nil
}

// resolveEdgeTarget resolves an edge's target to a node id: by loc within the
// target memory first (portable across re-homing), then the frontmatter id.
func resolveEdgeTarget(cmd *cobra.Command, client graphql.Client, memoryRef string, e nodedoc.Edge) string {
	if e.TargetLoc != "" && strings.Contains(memoryRef, ":") {
		targetURN := cmdutil.NodeURN(memoryRef, e.TargetLoc)
		if targetURN == "" {
			return e.TargetID
		}
		resp, err := gen.ResolveUrn(cmd.Context(), client, targetURN)
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
		return e.Name
	}
}

func edgeKey(targetID, label string) string { return targetID + "\x00" + label }

// edgeRejectReason renders a createEdge failure as a short, single-line reason.
//
// It asks the error for the server's own message (#566) rather than parsing one
// back out of genqlient's rendering. The structured read is exact where the
// strip was a guess: the prefix is BUILT from the error's Locations and Path,
// so reading those fields cannot mistake a colon in the server's own text
// ("field 'x': required") for a location separator.
//
// The text fallback below stays, and is not dead code — it is what a non-GraphQL
// failure hits, and it is still the only thing that can help there.
func edgeRejectReason(err error) string {
	if msg := api.ServerMessage(err); msg != "" {
		// First message, not the joined list: this renders one line in a
		// per-edge report where every row is one edge's own refusal.
		return firstLine(msg)
	}
	msg := firstLine(err.Error())
	if rest, ok := strings.CutPrefix(msg, "input:"); ok {
		msg = strings.TrimSpace(strings.TrimLeft(rest, "0123456789:"))
	}
	return msg
}

func firstLine(s string) string {
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[:nl]
	}
	return strings.TrimSpace(s)
}
