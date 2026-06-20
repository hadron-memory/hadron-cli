package node

import (
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <node-urn>",
		Short: "Show a node, including its content and edges",
		Long: `Show a node by its fully-qualified URN: <org>:<memory>:<loc>
(e.g. hadronmemory.com:dev:start-here). The hrn:node: prefix is
optional (legacy urn:node: also accepted). Bare locs are not accepted —
the same loc can exist in several memories, so node references must
always name the memory.`,
		Example: `  hadron node get hadronmemory.com:dev:start-here
  hadron node get hrn:node:hadronmemory.com:dev:start-here --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			node, err := fetchNode(cmd, client, args[0])
			if err != nil {
				return err
			}

			dto := detailDTO(node)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "%s\n  loc: %s\n  type: %s\n", dto.Name, dto.Loc, dto.NodeType)
				if dto.Description != nil && *dto.Description != "" {
					fmt.Fprintf(w, "  about: %s\n", *dto.Description)
				}
				if len(dto.Tags) > 0 {
					fmt.Fprintf(w, "  tags: %v\n", dto.Tags)
				}
				fmt.Fprintf(w, "  updated: %s\n", dto.UpdatedAt)
				if dto.Data != nil && len(*dto.Data) > 0 && string(*dto.Data) != "null" {
					fmt.Fprintf(w, "  data: %s\n", string(*dto.Data))
				}
				if len(dto.OutgoingEdges) > 0 || len(dto.IncomingEdges) > 0 {
					fmt.Fprintln(w, "  edges:")
					for _, e := range dto.OutgoingEdges {
						fmt.Fprintf(w, "    → %s (%s)\n", e.Loc, e.Label)
					}
					for _, e := range dto.IncomingEdges {
						fmt.Fprintf(w, "    ← %s (%s)\n", e.Loc, e.Label)
					}
				}
				if dto.Content != nil && *dto.Content != "" {
					fmt.Fprintf(w, "\n%s\n", *dto.Content)
				} else if dto.Abstract != nil && *dto.Abstract != "" {
					fmt.Fprintf(w, "\n(abstract)\n%s\n", *dto.Abstract)
				}
				return nil
			})
		},
	}
}

// edgeRefDTO is one edge endpoint in node output.
type edgeRefDTO struct {
	EdgeID   string `json:"edgeId"`
	Label    string `json:"label"`
	Priority int    `json:"priority"`
	NodeID   string `json:"nodeId"`
	Loc      string `json:"loc"`
	MemoryID string `json:"memoryId"`
}

func detailDTO(n *gen.GetNodeByIdNodeByIdNode) nodeDetailDTO {
	dto := nodeDetailDTO{
		nodeDTO: nodeDTO{
			ID:        n.Id,
			MemoryID:  n.MemoryId,
			Loc:       n.Loc,
			Name:      n.Name,
			NodeType:  n.NodeType,
			Tags:      n.Tags,
			UpdatedAt: n.UpdatedAt,
		},
		Description:   n.Description,
		Abstract:      n.Abstract,
		Content:       n.Content,
		Data:          n.Data,
		Seq:           n.Seq,
		CreatedAt:     n.CreatedAt,
		OutgoingEdges: []edgeRefDTO{},
		IncomingEdges: []edgeRefDTO{},
	}
	for _, e := range n.OutgoingEdges {
		dto.OutgoingEdges = append(dto.OutgoingEdges, edgeRefDTO{
			EdgeID: e.Id, Label: e.Label, Priority: e.Priority,
			NodeID: e.Target.Id, Loc: e.Target.Loc, MemoryID: e.Target.MemoryId,
		})
	}
	for _, e := range n.IncomingEdges {
		dto.IncomingEdges = append(dto.IncomingEdges, edgeRefDTO{
			EdgeID: e.Id, Label: e.Label, Priority: e.Priority,
			NodeID: e.Source.Id, Loc: e.Source.Loc, MemoryID: e.Source.MemoryId,
		})
	}
	return dto
}

// fetchNode resolves a node URN and returns the full node.
func fetchNode(cmd *cobra.Command, client graphql.Client, ref string) (*gen.GetNodeByIdNodeByIdNode, error) {
	id, err := cmdutil.ResolveNodeURN(cmd, client, ref)
	if err != nil {
		return nil, err
	}
	resp, err := gen.GetNodeById(cmd.Context(), client, id)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.NodeById == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "node %q not found", ref)
	}
	return resp.NodeById, nil
}
