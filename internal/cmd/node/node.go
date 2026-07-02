// Package node implements `hadron node ...`.
package node

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// nodeDTO is the stable --json shape for a node in list output.
type nodeDTO struct {
	ID         string   `json:"id"`
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
	Description   *string          `json:"description"`
	Abstract      *string          `json:"abstract"`
	Content       *string          `json:"content"`
	Data          *json.RawMessage `json:"data,omitempty"`
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
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdExport(f))
	cmd.AddCommand(newCmdImport(f))
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
