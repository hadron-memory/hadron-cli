package api

import (
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/nodedoc"
)

// Short aliases for genqlient's deeply-nested NodeBatch projection names.
type (
	batchNode = gen.NodeBatchNodeBatchNodeBatchResultNodesNode
	batchEdge = gen.NodeBatchNodeBatchNodeBatchResultNodesNodeOutgoingEdgesEdge
)

// DocumentFromBatchNode maps a bulk-read node into the neutral nodedoc.Document
// the markdown/JSON codecs consume. It is the single gen→Document mapping shared
// by `memory export` and `node export`. Type "" carries the server default
// (info), so it never serializes and a re-import defaults correctly. The
// projection only carries the memory id, not the URN, so callers that emit a
// standalone file resolve and set Document.MemoryURN themselves.
func DocumentFromBatchNode(n *batchNode) *nodedoc.Document {
	if n == nil {
		return nil
	}
	doc := &nodedoc.Document{
		ID:         n.Id,
		Loc:        n.Loc,
		Name:       n.Name,
		Tags:       n.Tags,
		Seq:        n.Seq,
		Data:       nodedoc.DecodeJSON(n.Data),
		Properties: nodedoc.DecodeJSON(n.Properties),
		Edges:      edgesFromBatch(n.OutgoingEdges),
	}
	if n.NodeType != "" && n.NodeType != "info" {
		doc.Type = n.NodeType
	}
	if n.Alias != nil {
		doc.Alias = *n.Alias
	}
	if n.Description != nil {
		doc.Description = *n.Description
	}
	if n.Abstract != nil {
		doc.Abstract = *n.Abstract
	}
	if n.AbstractOriginHash != nil {
		doc.AbstractOriginHash = *n.AbstractOriginHash
	}
	if n.Content != nil {
		doc.Content = *n.Content
	}
	return doc
}

// edgesFromBatch projects outgoing edges into nodedoc.Edge, skipping any edge
// whose target can't be addressed (no target node).
func edgesFromBatch(edges []*batchEdge) []nodedoc.Edge {
	out := make([]nodedoc.Edge, 0, len(edges))
	for _, e := range edges {
		if e == nil || e.Target == nil {
			continue
		}
		out = append(out, nodedoc.Edge{
			TargetID:  e.Target.Id,
			TargetLoc: e.Target.Loc,
			Label:     e.Label,
			Condition: nodedoc.DecodeJSON(e.Condition),
			Priority:  e.Priority,
		})
	}
	return out
}
