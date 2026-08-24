// Package node implements `hadron node ...`.
package node

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// nodeDTO is the stable --json shape for a node in list output.
//
// URN is the fully-qualified node URN the server composes. It's omitempty
// because only the surfaces that select `urn` populate it (move/clone, and
// since #515 `node get`); others leave the key out rather than emit "".
type nodeDTO struct {
	ID  string `json:"id"`
	URN string `json:"urn,omitempty"`
	// PortalURL is the server-built link that opens this node
	// (hadron-server#881, cor:api:230:01) — the one to COPY when citing a node
	// to a human, rather than assembling <origin>/app/u/<urn> yourself, which
	// is the composition the field exists to remove.
	//
	// Nullable for two unrelated reasons — no usable web origin on the
	// deployment, or no canonical identifier to build from — and both render
	// as nothing. omitempty for the same reason as URN: a surface that does not
	// select it leaves the key out rather than asserting null.
	PortalURL  string   `json:"portalUrl,omitempty"`
	MemoryID   string   `json:"memoryId"`
	Loc        string   `json:"loc"`
	Name       string   `json:"name"`
	NodeType   string   `json:"nodeType"`
	Tags       []string `json:"tags"`
	Seq        *int     `json:"seq"`
	IsRunnable bool     `json:"isRunnable"`
	UpdatedAt  string   `json:"updatedAt"`
}

// nodeDetailDTO extends the list shape for single-node output.
type nodeDetailDTO struct {
	nodeDTO
	ObjectType    *string          `json:"objectType"`
	Description   *string          `json:"description"`
	Abstract      *string          `json:"abstract"`
	Content       *string          `json:"content"`
	Data          *json.RawMessage `json:"data,omitempty"`
	Properties    *json.RawMessage `json:"properties,omitempty"`
	Seq           *int             `json:"seq"`
	CreatedAt     string           `json:"createdAt"`
	OutgoingEdges []edgeRefDTO     `json:"outgoingEdges"`
	IncomingEdges []edgeRefDTO     `json:"incomingEdges"`
}

func NewCmdNode(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "node <command>",
		Aliases: []string{"nodes"},
		Short:   "Work with nodes in a memory",
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdAdd(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdMove(f))
	cmd.AddCommand(newCmdClone(f))
	cmd.AddCommand(newCmdMerge(f))
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdExport(f))
	cmd.AddCommand(newCmdImport(f))
	cmd.AddCommand(newCmdRevision(f))
	return cmd
}

func createDTO(n *gen.CreateNodeCreateNode) nodeDTO {
	return nodeDTO{
		ID:         n.Id,
		MemoryID:   n.MemoryId,
		Loc:        n.Loc,
		Name:       n.Name,
		NodeType:   n.NodeType,
		Tags:       n.Tags,
		Seq:        nil,
		IsRunnable: boolVal(n.IsRunnable),
		UpdatedAt:  n.UpdatedAt,
	}
}

func updateDTO(n *gen.UpdateNodeUpdateNode) nodeDTO {
	return nodeDTO{
		ID:         n.Id,
		MemoryID:   n.MemoryId,
		Loc:        n.Loc,
		Name:       n.Name,
		NodeType:   n.NodeType,
		Tags:       n.Tags,
		Seq:        nil,
		IsRunnable: boolVal(n.IsRunnable),
		UpdatedAt:  n.UpdatedAt,
	}
}

func mergeDTO(n *gen.UpdateNodeDataUpdateNodeDataNode) nodeDTO {
	return nodeDTO{
		ID:         n.Id,
		MemoryID:   n.MemoryId,
		Loc:        n.Loc,
		Name:       n.Name,
		NodeType:   n.NodeType,
		Tags:       n.Tags,
		Seq:        nil,
		IsRunnable: boolVal(n.IsRunnable),
		UpdatedAt:  n.UpdatedAt,
	}
}

// boolVal dereferences a nullable Boolean, treating an absent value as false
// (the server treats a null isRunnable as "not runnable").
func boolVal(b *bool) bool {
	return b != nil && *b
}

// strVal flattens a nullable server string. Absent becomes "", which the
// omitempty DTO fields then leave out of --json entirely rather than emitting
// an empty string that reads like a value.
func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
