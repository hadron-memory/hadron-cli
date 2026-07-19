// Package gqltypes holds hand-authored Go types that the generated genqlient
// client binds to (see genqlient.yaml `bindings`). They live outside the `gen`
// package so `gen` can import them without a cycle.
//
// NodeWhereInput / NodePropertySort are bound rather than generated on purpose:
// NodeWhereInput is recursive (and/or/not reference it), and genqlient's
// per-field `omitempty` resolution across the two operations that use it
// (FindNodes, SearchNodes) is non-deterministic for a self-referential type —
// the `,omitempty` tag flips between codegen runs, which both breaks the wire
// contract and reds CI's codegen-freshness gate. Binding to these explicit
// structs pins the json tags, so every unset field is reliably OMITTED, never
// serialized as null. That omit-vs-null contract is load-bearing here: the
// server's leaf validation (#719) counts any operator key that is not
// `undefined` — an explicit null included — so a null would trip "a leaf must
// carry exactly one operator".
package gqltypes

import "encoding/json"

// NodeWhereColumn is the JSONB column a NodeWhere leaf reads (#719). Default properties.
type NodeWhereColumn string

const (
	NodeWhereColumnProperties NodeWhereColumn = "properties"
	NodeWhereColumnData       NodeWhereColumn = "data"
)

// NodeWhereCast is how a NodeWhere leaf's extracted value is typed before
// comparison (#719). Default text.
type NodeWhereCast string

const (
	NodeWhereCastText     NodeWhereCast = "text"
	NodeWhereCastNumber   NodeWhereCast = "number"
	NodeWhereCastDatetime NodeWhereCast = "datetime"
	NodeWhereCastBoolean  NodeWhereCast = "boolean"
)

// SortDirection is the direction for a findNodes property-path sort (#719).
// Default asc.
type SortDirection string

const (
	SortDirectionAsc  SortDirection = "asc"
	SortDirectionDesc SortDirection = "desc"
)

// NodeWhereInput is a recursive structured predicate over a node's
// properties/data JSONB (#719). A node is EITHER a branch (exactly one of
// and/or/not) OR a leaf (a path plus exactly one operator). Every field is
// optional and carries `omitempty`, so an unset field is omitted from the wire
// rather than sent as null — see the package doc for why that matters.
type NodeWhereInput struct {
	And []*NodeWhereInput `json:"and,omitempty"`
	Or  []*NodeWhereInput `json:"or,omitempty"`
	Not *NodeWhereInput   `json:"not,omitempty"`

	Field *NodeWhereColumn `json:"field,omitempty"`
	Path  []string         `json:"path,omitempty"`
	As    *NodeWhereCast   `json:"as,omitempty"`

	Eq       *json.RawMessage  `json:"eq,omitempty"`
	Ne       *json.RawMessage  `json:"ne,omitempty"`
	In       []json.RawMessage `json:"in,omitempty"`
	Lt       *json.RawMessage  `json:"lt,omitempty"`
	Lte      *json.RawMessage  `json:"lte,omitempty"`
	Gt       *json.RawMessage  `json:"gt,omitempty"`
	Gte      *json.RawMessage  `json:"gte,omitempty"`
	Between  []json.RawMessage `json:"between,omitempty"`
	Exists   *bool             `json:"exists,omitempty"`
	Contains *json.RawMessage  `json:"contains,omitempty"`
}

// NodePropertySort orders findNodes by the value at a properties/data JSON path
// (#719). path is required; the rest are optional and omitempty.
type NodePropertySort struct {
	Path      []string         `json:"path"`
	Field     *NodeWhereColumn `json:"field,omitempty"`
	As        *NodeWhereCast   `json:"as,omitempty"`
	Direction *SortDirection   `json:"direction,omitempty"`
}
