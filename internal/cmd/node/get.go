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
	var memory string
	cmd := &cobra.Command{
		Use:   "get <node-urn> | <loc> -m <memory>",
		Short: "Show a node, including its content and edges",
		Long: `Show a node by its fully-qualified URN: <org>:<memory>:<loc>
(e.g. hadronmemory.com:dev:start-here). The hrn:node: prefix is
optional (legacy urn:node: also accepted). Pass -m/--memory to name a
node by a bare <loc> within that memory instead; without -m a bare loc
is rejected, since the same loc can exist in several memories.`,
		Example: `  hadron node get hadronmemory.com:dev:start-here
  hadron node get start-here -m hadronmemory.com:dev --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			node, err := fetchNode(cmd, client, memory, args[0])
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
				if dto.Data != nil && len(*dto.Data) > 0 {
					if dataStr := string(*dto.Data); dataStr != "null" {
						fmt.Fprintf(w, "  data: %s\n", dataStr)
					}
				}
				if len(dto.OutgoingEdges) > 0 || len(dto.IncomingEdges) > 0 {
					fmt.Fprintln(w, "  edges:")
					for _, e := range dto.OutgoingEdges {
						fmt.Fprintf(w, "    → %s (%s)\n", e.Loc, edgeRel(e))
					}
					for _, e := range dto.IncomingEdges {
						fmt.Fprintf(w, "    ← %s (%s)\n", e.Loc, edgeRel(e))
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
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org:memory) to resolve a bare <loc> against")
	return cmd
}

// edgeRefDTO is one edge endpoint in node output.
type edgeRefDTO struct {
	EdgeID     string `json:"edgeId"`
	Name       string `json:"name"`
	EdgeLoc    string `json:"edgeLoc"`
	IsRunnable bool   `json:"isRunnable"`
	Priority   int    `json:"priority"`
	NodeID     string `json:"nodeId"`
	Loc        string `json:"loc"`
	MemoryID   string `json:"memoryId"`
}

// edgeRel is the relationship shown for an edge: its name, or its loc when the
// name is empty (spec 037).
func edgeRel(e edgeRefDTO) string {
	if e.Name != "" {
		return e.Name
	}
	return e.EdgeLoc
}

func edgeRefOf(edgeID string, name *string, edgeLoc string, isRunnable *bool, priority int, nodeID, loc, memoryID string) edgeRefDTO {
	n := ""
	if name != nil {
		n = *name
	}
	run := false
	if isRunnable != nil {
		run = *isRunnable
	}
	return edgeRefDTO{
		EdgeID: edgeID, Name: n, EdgeLoc: edgeLoc, IsRunnable: run, Priority: priority,
		NodeID: nodeID, Loc: loc, MemoryID: memoryID,
	}
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
		dto.OutgoingEdges = append(dto.OutgoingEdges,
			edgeRefOf(e.Id, e.Name, e.Loc, e.IsRunnable, e.Priority, e.Target.Id, e.Target.Loc, e.Target.MemoryId))
	}
	for _, e := range n.IncomingEdges {
		dto.IncomingEdges = append(dto.IncomingEdges,
			edgeRefOf(e.Id, e.Name, e.Loc, e.IsRunnable, e.Priority, e.Source.Id, e.Source.Loc, e.Source.MemoryId))
	}
	return dto
}

// fetchNode resolves a node reference (a full URN, or a bare loc within
// memory) and returns the full node.
func fetchNode(cmd *cobra.Command, client graphql.Client, memory, ref string) (*gen.GetNodeByIdNodeByIdNode, error) {
	id, err := cmdutil.ResolveNodeRef(cmd, client, memory, ref)
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
