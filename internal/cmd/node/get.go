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
		Use:   "get <loc>",
		Short: "Show a node, including its content",
		Long: `Show a node by its loc (e.g. findings:flaky-ci).

Without --memory the loc is resolved across every memory you can
read; if the same loc exists in several memories the server picks
one. Pass -m/--memory to resolve within a specific memory.`,
		Example: `  hadron node get start-here -m hadronmemory.com:dev
  hadron node get findings:flaky-ci --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			node, err := fetchNode(cmd, client, args[0], memory)
			if err != nil {
				return err
			}

			dto := nodeDetailDTO{
				nodeDTO: nodeDTO{
					ID:        node.ID,
					MemoryID:  node.MemoryID,
					Loc:       node.Loc,
					Name:      node.Name,
					NodeType:  node.NodeType,
					Tags:      node.Tags,
					UpdatedAt: node.UpdatedAt,
				},
				Description: node.Description,
				Abstract:    node.Abstract,
				Content:     node.Content,
				Seq:         node.Seq,
				CreatedAt:   node.CreatedAt,
			}

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "%s\n  loc: %s\n  type: %s\n", dto.Name, dto.Loc, dto.NodeType)
				if dto.Description != nil && *dto.Description != "" {
					fmt.Fprintf(w, "  about: %s\n", *dto.Description)
				}
				if len(dto.Tags) > 0 {
					fmt.Fprintf(w, "  tags: %v\n", dto.Tags)
				}
				fmt.Fprintf(w, "  updated: %s\n", dto.UpdatedAt)
				if dto.Content != nil && *dto.Content != "" {
					fmt.Fprintf(w, "\n%s\n", *dto.Content)
				} else if dto.Abstract != nil && *dto.Abstract != "" {
					fmt.Fprintf(w, "\n(abstract)\n%s\n", *dto.Abstract)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "resolve the loc within this memory (ID or URN)")
	return cmd
}

// nodeDetail is the command-layer view of a fetched node, built from
// either the cross-memory node(loc) lookup or the memory-scoped path.
type nodeDetail struct {
	ID          string
	MemoryID    string
	Loc         string
	Name        string
	NodeType    string
	Tags        []string
	Description *string
	Abstract    *string
	Content     *string
	Seq         *int
	CreatedAt   string
	UpdatedAt   string
}

// fetchNode resolves a node by bare loc. Without a memory scope it
// uses node(loc) — cross-memory, server picks on collision. With one,
// it lists the memory's nodes with the loc as prefix, exact-matches
// client-side, and fetches the winner by ID.
func fetchNode(cmd *cobra.Command, client graphql.Client, loc, memory string) (*nodeDetail, error) {
	if memory == "" {
		resp, err := gen.GetNode(cmd.Context(), client, loc)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.Node == nil {
			return nil, exitcode.Newf(exitcode.NotFound, "node %q not found", loc)
		}
		n := resp.Node
		return &nodeDetail{
			ID: n.Id, MemoryID: n.MemoryId, Loc: n.Loc, Name: n.Name,
			NodeType: n.NodeType, Tags: n.Tags, Description: n.Description,
			Abstract: n.Abstract, Content: n.Content, Seq: n.Seq,
			CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
		}, nil
	}

	list, err := gen.Nodes(cmd.Context(), client, &memory, &loc, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, api.MapError(err)
	}
	for _, n := range list.Nodes {
		if n.Loc != loc {
			continue
		}
		resp, err := gen.GetNodeById(cmd.Context(), client, n.Id)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.NodeById == nil {
			break
		}
		d := resp.NodeById
		return &nodeDetail{
			ID: d.Id, MemoryID: d.MemoryId, Loc: d.Loc, Name: d.Name,
			NodeType: d.NodeType, Tags: d.Tags, Description: d.Description,
			Abstract: d.Abstract, Content: d.Content, Seq: d.Seq,
			CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		}, nil
	}
	return nil, exitcode.Newf(exitcode.NotFound, "node %q not found in memory %s", loc, memory)
}
