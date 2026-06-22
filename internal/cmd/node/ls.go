package node

import (
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var (
		memory   string
		prefix   string
		nodeType string
		tags     []string
		search   string
		limit    int
		offset   int
		sortSeq  string
		seqGt    int
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List nodes",
		Long: `List nodes you can access, optionally scoped to a memory.

-m/--memory takes a memory ID or fully-qualified URN (org::memory) and
scopes the listing to that memory. --prefix filters on the node loc
(e.g. --prefix findings: lists one branch).

--sort-seq [asc|desc] sorts results by seq in ascending or descending order.
--seq-gt N filters to nodes with seq > N (useful for reading new messages
after a known seq number).`,
		Example: `  hadron node ls --memory hadronmemory.com:dev
  hadron node ls -m hadronmemory.com:dev --prefix findings: --json
  hadron node ls -m hadronmemory.com:dev --seq-gt 42 --sort-seq asc`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			var memoryArg, prefixArg, typeArg, searchArg *string
			var limitArg, offsetArg *int
			if memory != "" {
				memoryArg = &memory
			}
			if prefix != "" {
				prefixArg = &prefix
			}
			if nodeType != "" {
				typeArg = &nodeType
			}
			if search != "" {
				searchArg = &search
			}
			if limit > 0 {
				limitArg = &limit
			}
			if offset > 0 {
				offsetArg = &offset
			}

			resp, err := gen.Nodes(cmd.Context(), client, memoryArg, prefixArg, typeArg, tags, searchArg, limitArg, offsetArg)
			if err != nil {
				return api.MapError(err)
			}

			nodes := make([]nodeDTO, 0, len(resp.Nodes))
			for _, n := range resp.Nodes {
				nodes = append(nodes, nodeDTO{
					ID:        n.Id,
					MemoryID:  n.MemoryId,
					Loc:       n.Loc,
					Name:      n.Name,
					NodeType:  n.NodeType,
					Tags:      n.Tags,
					Seq:       n.Seq,
					UpdatedAt: n.UpdatedAt,
				})
			}

			// Filter by seq > N
			if seqGt > 0 {
				filtered := make([]nodeDTO, 0, len(nodes))
				for i := range nodes {
					if nodes[i].Seq != nil && *nodes[i].Seq > seqGt {
						filtered = append(filtered, nodes[i])
					}
				}
				nodes = filtered
			}

			// Sort by seq
			switch sortSeq {
			case "asc":
				sort.Slice(nodes, func(i, j int) bool {
					seqI := nodes[i].Seq
					seqJ := nodes[j].Seq
					if seqI == nil && seqJ == nil {
						return false
					}
					if seqI == nil {
						return false
					}
					if seqJ == nil {
						return true
					}
					return *seqI < *seqJ
				})
			case "desc":
				sort.Slice(nodes, func(i, j int) bool {
					seqI := nodes[i].Seq
					seqJ := nodes[j].Seq
					if seqI == nil && seqJ == nil {
						return false
					}
					if seqI == nil {
						return true
					}
					if seqJ == nil {
						return false
					}
					return *seqI > *seqJ
				})
			}

			return output.Write(f.IOStreams, f.JSON, nodes, func(w io.Writer) error {
				t := output.NewTable(w, "LOC", "NAME", "TYPE", "SEQ")
				for _, n := range nodes {
					seqStr := ""
					if n.Seq != nil {
						seqStr = fmt.Sprint(*n.Seq)
					}
					t.Row(n.Loc, n.Name, n.NodeType, seqStr)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "scope to a memory (ID or URN)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "filter by node loc prefix")
	cmd.Flags().StringVar(&nodeType, "type", "", "filter by node type")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "filter by tag (repeatable)")
	cmd.Flags().StringVar(&search, "search", "", "keyword filter on name/description")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of nodes")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	cmd.Flags().StringVar(&sortSeq, "sort-seq", "", "sort by seq: 'asc' or 'desc'")
	cmd.Flags().IntVar(&seqGt, "seq-gt", 0, "filter to nodes with seq > N")
	return cmd
}
