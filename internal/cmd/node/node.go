// Package node implements `hadron node ...`.
package node

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// nodeDTO is the stable --json shape for a node in list output.
type nodeDTO struct {
	ID        string   `json:"id"`
	MemoryID  string   `json:"memoryId"`
	Loc       string   `json:"loc"`
	Name      string   `json:"name"`
	NodeType  string   `json:"nodeType"`
	Tags      []string `json:"tags"`
	UpdatedAt string   `json:"updatedAt"`
}

// nodeDetailDTO extends the list shape for single-node output.
type nodeDetailDTO struct {
	nodeDTO
	Description *string `json:"description"`
	Abstract    *string `json:"abstract"`
	Content     *string `json:"content"`
	Seq         *int    `json:"seq"`
	CreatedAt   string  `json:"createdAt"`
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
	return cmd
}

func upsertDTO(n *gen.UpsertNodeUpsertNode) nodeDTO {
	return nodeDTO{
		ID:        n.Id,
		MemoryID:  n.MemoryId,
		Loc:       n.Loc,
		Name:      n.Name,
		NodeType:  n.NodeType,
		Tags:      n.Tags,
		UpdatedAt: n.UpdatedAt,
	}
}
