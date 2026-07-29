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

// lsPageSize bounds one page of the exhaustive browse scan. The server caps an
// unspecified limit at its default page and drops the rest (#23), so any
// "whole-collection" listing — here, --seq-gt / --sort-seq, which filter and
// sort client-side — must page explicitly to exhaustion (#319).
const lsPageSize = 500

// paginateAllNodes runs a browse fetch to exhaustion, returning every node in
// scope. The fetch is injected so the loop is unit-testable without a server.
func paginateAllNodes(fetch func(limit, offset int) ([]*api.ListNode, error)) ([]*api.ListNode, error) {
	var all []*api.ListNode
	for offset := 0; ; offset += lsPageSize {
		page, err := fetch(lsPageSize, offset)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < lsPageSize {
			return all, nil
		}
	}
}

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var (
		memory     string
		prefix     string
		nodeType   string
		objectType string
		runnable   bool
		tags       []string
		search     string
		where      string
		sortProp   string
		limit      int
		offset     int
		sortSeq    string
		seqGt      int
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
after a known seq number). Both scan the WHOLE collection in scope — not just
the server's default first page — so the newest nodes are never hidden past a
page boundary; with --sort-seq, --limit then means "the top N by seq".

--where takes a JSON predicate over the node's properties/data JSONB (a leaf is
a path plus one of eq|ne|in|lt|lte|gt|gte|between|exists|contains; branch with
and/or/not). --object-type filters the objectType collection facet.
--sort-property orders by a properties/data JSON path.`,
		Example: `  hadron node list --memory hadronmemory.com::dev
  hadron node list -m hadronmemory.com::dev --prefix findings: --json
  hadron node list -m hadronmemory.com::dev --seq-gt 42 --sort-seq asc
  hadron node list -m acme.com::kb --object-type insight --where '{"path":["source"],"eq":"substack"}'
  hadron node list -m acme.com::kb --sort-property '{"path":["rank"],"as":"number","direction":"desc"}'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			whereArg, err := cmdutil.ParseNodeWhere(where)
			if err != nil {
				return err
			}
			sortPropArg, err := cmdutil.ParseNodePropertySort(sortProp)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			var searchArg *string
			var limitArg, offsetArg *int
			// Build the structured findNodes filter. Tri-state --runnable:
			// --runnable filters to runnable nodes, --runnable=false to nodes
			// explicitly marked non-runnable; omitting it (the common case)
			// constrains nothing. The server reads NULL isRunnable as neither,
			// so --runnable=false excludes the many NULL nodes too.
			var filter gen.NodeFilter
			var filterSet bool
			if cmd.Flags().Changed("runnable") {
				filter.IsRunnable = &runnable
				filterSet = true
			}
			if memory != "" {
				filter.MemoryIds = []string{memory}
				filterSet = true
			}
			if prefix != "" {
				filter.LocPrefix = &prefix
				filterSet = true
			}
			if nodeType != "" {
				filter.NodeType = &nodeType
				filterSet = true
			}
			if objectType != "" {
				filter.ObjectType = &objectType
				filterSet = true
			}
			if len(tags) > 0 {
				filter.Tags = tags
				filterSet = true
			}
			if whereArg != nil {
				filter.Where = whereArg
				filterSet = true
			}
			// Pass nil (not an empty &{}) when nothing is constrained, so a bare
			// `node list` sends no filter object at all — mirroring newNodeFilter
			// in the spec package.
			var filterArg *gen.NodeFilter
			if filterSet {
				filterArg = &filter
			}
			// A --search term ranks (keyword mode); without it the list is a
			// deterministic loc-ordered browse.
			var mode *gen.FindNodesMode
			var sortArg *gen.NodeSort
			if search != "" {
				searchArg = &search
				m := gen.FindNodesModeKeyword
				mode = &m
			} else {
				s := gen.NodeSortLoc
				sortArg = &s
			}
			if limit > 0 {
				limitArg = &limit
			}
			if offset > 0 {
				offsetArg = &offset
			}

			// --seq-gt / --sort-seq post-process client-side, so they must see the
			// WHOLE collection — not the server's default first page, which
			// silently hid the newest nodes once a collection exceeded one page
			// (#319: --seq-gt read empty as "no new messages"). Whenever either is
			// set — browse OR a ranked --search (there, --search is the filter and
			// seq the sort, so its later high-seq matches must be paged in too) —
			// page to exhaustion and apply --limit/--offset client-side, after the
			// seq filter/sort.
			seqMode := seqGt > 0 || sortSeq != ""

			var rawNodes []*api.ListNode
			if seqMode {
				rawNodes, err = paginateAllNodes(func(lim, off int) ([]*api.ListNode, error) {
					l, o := lim, off
					page, err := api.FindNodes(cmd.Context(), client, searchArg, mode, filterArg, sortArg, sortPropArg, &l, &o)
					if err != nil {
						return nil, err
					}
					return page.Nodes, nil
				})
			} else {
				var page *api.FindNodesPage
				page, err = api.FindNodes(cmd.Context(), client, searchArg, mode, filterArg, sortArg, sortPropArg, limitArg, offsetArg)
				if err == nil {
					rawNodes = page.Nodes
				}
			}
			if err != nil {
				return api.MapError(err)
			}

			nodes := make([]nodeDTO, 0, len(rawNodes))
			for _, n := range rawNodes {
				nodes = append(nodes, nodeDTO{
					ID:         n.Id,
					MemoryID:   n.MemoryId,
					Loc:        n.Loc,
					Name:       n.Name,
					NodeType:   n.NodeType,
					Tags:       n.Tags,
					Seq:        n.Seq,
					IsRunnable: boolVal(n.IsRunnable),
					UpdatedAt:  n.UpdatedAt,
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

			// In seq mode the server page was bypassed (we paged to exhaustion),
			// so --limit/--offset are applied here — over the seq-filtered, sorted
			// result, i.e. "the top N by seq" rather than an arbitrary first page.
			if seqMode {
				if offset > 0 {
					if offset >= len(nodes) {
						nodes = nodes[:0]
					} else {
						nodes = nodes[offset:]
					}
				}
				if limit > 0 && limit < len(nodes) {
					nodes = nodes[:limit]
				}
			}

			return output.Write(f.IOStreams, f.JSON, nodes, func(w io.Writer) error {
				t := output.NewTable(w, "LOC", "NAME", "TYPE", "SEQ", "RUN")
				for _, n := range nodes {
					seqStr := ""
					if n.Seq != nil {
						seqStr = fmt.Sprint(*n.Seq)
					}
					runStr := ""
					if n.IsRunnable {
						runStr = "✓"
					}
					t.Row(n.Loc, n.Name, n.NodeType, seqStr, runStr)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "scope to a memory (ID or URN)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "filter by node loc prefix")
	cmd.Flags().StringVar(&nodeType, "type", "", "filter by node type")
	cmd.Flags().StringVar(&objectType, "object-type", "", "filter by objectType collection facet (e.g. competitor)")
	cmd.Flags().BoolVar(&runnable, "runnable", false, "filter by runnable status (--runnable / --runnable=false); omit for all")
	cmd.Flags().Lookup("runnable").NoOptDefVal = "true"
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "filter by tag (repeatable)")
	cmd.Flags().StringVar(&search, "search", "", "keyword filter on name/description")
	cmd.Flags().StringVar(&where, "where", "", "structured predicate over properties/data as JSON (e.g. '{\"path\":[\"source\"],\"eq\":\"substack\"}')")
	cmd.Flags().StringVar(&sortProp, "sort-property", "", "order by a properties/data JSON path as JSON (e.g. '{\"path\":[\"rank\"],\"as\":\"number\",\"direction\":\"desc\"}')")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of nodes")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	cmd.Flags().StringVar(&sortSeq, "sort-seq", "", "sort by seq: 'asc' or 'desc'")
	cmd.Flags().IntVar(&seqGt, "seq-gt", 0, "filter to nodes with seq > N")
	return cmd
}
