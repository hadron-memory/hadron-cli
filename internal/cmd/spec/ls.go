package spec

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var memory, prefix string
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List spec nodes in a memory",
		Long: `List spec nodes, optionally scoped to a loc prefix.

--prefix filters by the citation prefix, e.g. --prefix msg lists one
module, --prefix msg:010 one feature and its rules/flows.

By default every matching spec is listed (the query is paged to
exhaustion). Pass --limit (with optional --offset) to fetch a single
explicit page instead.`,
		Example: `  hadron spec ls -m micromentor.org::platform-specs
  hadron spec ls -m micromentor.org::platform-specs --prefix msg:010 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var memoryArg *string
			if memory != "" {
				m, err := memoryURNFromFlag(memory)
				if err != nil {
					return err
				}
				memoryArg = &m
			}
			var prefixArg *string
			if prefix != "" {
				prefixArg = &prefix
			}
			// Bare `ls` lists the whole memory, so page to exhaustion (#23).
			// An explicit --limit/--offset is honored verbatim as a single
			// page — deliberate user-driven pagination, not the default.
			var rawNodes []*gen.NodesNodesNode
			if limit > 0 || offset > 0 {
				var limitArg, offsetArg *int
				if limit > 0 {
					limitArg = &limit
				}
				if offset > 0 {
					offsetArg = &offset
				}
				resp, rerr := gen.Nodes(cmd.Context(), client, memoryArg, prefixArg, nil, nil, []string{"spec"}, nil, limitArg, offsetArg)
				if rerr != nil {
					return api.MapError(rerr)
				}
				rawNodes = resp.Nodes
			} else {
				rawNodes, err = scanAllNodes(cmd.Context(), client, memoryArg, prefixArg, []string{"spec"})
				if err != nil {
					return err
				}
			}

			specs := make([]specDTO, 0, len(rawNodes))
			for _, n := range rawNodes {
				if n == nil {
					continue
				}
				if _, err := ParseCitation(n.Loc); err != nil {
					continue // only citation-shaped nodes are specs
				}
				specs = append(specs, specDTO{
					Citation:  n.Loc,
					MemoryID:  n.MemoryId,
					Name:      n.Name,
					NodeType:  n.NodeType,
					Tags:      n.Tags,
					UpdatedAt: n.UpdatedAt,
				})
			}

			return output.Write(f.IOStreams, f.JSON, specs, func(w io.Writer) error {
				t := output.NewTable(w, "CITATION", "NAME")
				for _, s := range specs {
					t.Row(s.Citation, s.Name)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "scope to a memory (ID or fully-qualified URN)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "filter by citation prefix (e.g. msg:010)")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of specs to fetch in one page (default: all)")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset (implies a single page)")
	return cmd
}
