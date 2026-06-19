// Package nodedoc is the codec for a single node's portable file form — the
// frontmatter-markdown (and canonical JSON) representation that `hadron memory
// export`, `hadron node export`, and `hadron node import` all share.
//
// It is deliberately decoupled from genqlient's generated types: the command
// layer maps a gen node to a [Document] (see api.DocumentFromBatchNode) and the
// codec maps a [Document] to and from bytes. Keeping the on-disk format pinned
// to this neutral struct means a GraphQL schema reshuffle can't ripple into the
// file format, and the encode/decode pair lives in one place so the round-trip
// invariant (parse∘render == identity) holds by construction.
package nodedoc

// Document is the in-memory representation of one node that both directions of
// both codecs share. Optional string fields use "" for "absent" (the markdown
// codec omits them); Seq/Data/Properties use nil. NodeType "" means the server
// default (info) — it is never serialized, so a re-import defaults correctly.
type Document struct {
	ID                 string   `json:"id"`
	MemoryURN          string   `json:"memory"`
	Loc                string   `json:"loc"`
	Name               string   `json:"name"`
	Type               string   `json:"type"`
	Alias              string   `json:"alias"`
	Description        string   `json:"description"`
	Abstract           string   `json:"abstract"`
	AbstractOriginHash string   `json:"abstractOriginHash"`
	ContentHash        string   `json:"contentHash"`
	Tags               []string `json:"tags"`
	Seq                *int     `json:"seq"`
	Data               any      `json:"data"`
	Properties         any      `json:"properties"`
	Content            string   `json:"content"`
	Edges              []Edge   `json:"edges"`
}

// Edge is one outgoing edge carried in a node's file. TargetLoc travels
// alongside TargetID for readability and as the portable key when re-homing a
// node into another memory, where the source TargetID no longer resolves.
// Condition and Priority round-trip the edge's gating and routing order, which
// the upsert's NodeInput.edges can't carry (it is only {label, targetId}).
type Edge struct {
	TargetID  string `json:"targetId"`
	TargetLoc string `json:"targetLoc"`
	Label     string `json:"label"`
	Condition any    `json:"condition"`
	Priority  int    `json:"priority"`
}
