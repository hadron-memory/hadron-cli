package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// nodeListDTO is the `node list` row: the shared nodeDTO plus the two JSONB
// columns, which arrive only when --with-properties / --with-data asked for
// them (#602).
//
// A SEPARATE type rather than two more fields on nodeDTO, for two reasons.
// nodeDetailDTO already declares `data` and `properties` at depth 0, so adding
// them to the embedded nodeDTO would shadow rather than extend — two same-named
// fields at different depths, where a reader cannot see which one is live
// (`Seq` is already shadowed that way, and once is enough). And an unset column
// keeps the listing's payload BYTE-IDENTICAL for every existing caller that
// does not pass a projection flag, which is what makes this additive against
// the --json contract.
//
// NON-POINTER json.RawMessage, and that is the load-bearing choice. The key
// must distinguish three states, and a *json.RawMessage collapses two of them:
//
//	not requested      -> key ABSENT   (nil, len 0 — omitempty drops it)
//	requested, a value -> key = value
//	requested, null    -> key = null   (the 4-byte literal, len 4 — survives)
//
// A pointer cannot express the third, because encoding/json sets a *RawMessage
// to nil for a JSON `null` — MEASURED, not assumed. The column would then vanish
// from the payload exactly as an unrequested one does, so a caller who asked for
// `data` and got no `data` key could not tell "this node has none" from "you did
// not ask" — which is #602's own defect, reintroduced inside its fix. It is also
// the #306 lesson already written down on nodeDetailDTO.AbstractOriginHash:
// `jq` returns null for a key that is not there.
//
// Because the two are indistinguishable on the wire, the FLAG decides what is
// present here, never the returned value. See fill below.
type nodeListDTO struct {
	nodeDTO
	Properties json.RawMessage `json:"properties,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
}

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
		memory         string
		prefix         string
		nodeType       string
		objectType     string
		runnable       bool
		tags           []string
		search         string
		where          string
		sortProp       string
		limit          int
		offset         int
		sortSeq        string
		seqGt          int
		withProperties bool
		withData       bool
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List nodes",
		Long: `List nodes you can access, optionally scoped to a memory.

-m/--memory takes a memory ID or fully-qualified URN (hrn:mem:<root>:<slug>) and
scopes the listing to that memory. --prefix filters on the node loc
(e.g. --prefix findings: lists one branch).

--sort-seq [asc|desc] sorts results by seq in ascending or descending order.
--seq-gt N filters to nodes with seq > N (useful for reading new messages
after a known seq number). Both scan the WHOLE collection in scope — not just
the server's default first page — so the newest nodes are never hidden past a
page boundary; with --sort-seq, --limit then means "the top N by seq".

--where takes a JSON predicate over one of the node's two JSONB columns (a leaf
is a path plus one of eq|ne|in|lt|lte|gt|gte|between|exists|contains; branch
with and/or/not). A leaf reads "properties" UNLESS it sets "field":"data" — so
a predicate aimed at a node's free-form data envelope must say so, or it
searches the wrong column and returns a silent zero. --sort-property takes the
same "field" key and the same default. --object-type filters the objectType
collection facet.

--with-properties / --with-data add those columns to the output, so a listing
can show the field it just filtered on. They are opt-in because a listing is an
index: a large result with full envelopes is a much bigger payload. In text
output either flag switches the table for a per-node block.`,
		Example: `  hadron node list --memory hrn:mem:hadronmemory.com:dev
  hadron node list -m hrn:mem:hadronmemory.com:dev --prefix findings: --json
  hadron node list -m hrn:mem:hadronmemory.com:dev --seq-gt 42 --sort-seq asc
  hadron node list -m hrn:mem:acme.com:kb --object-type insight --where '{"path":["source"],"eq":"substack"}'
  hadron node list -m hrn:mem:acme.com:team --where '{"field":"data","path":["authorName"],"exists":true}' --with-data
  hadron node list -m hrn:mem:acme.com:kb --sort-property '{"path":["rank"],"as":"number","direction":"desc"}'`,
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
					page, err := api.FindNodesProjected(cmd.Context(), client, searchArg, mode, filterArg, sortArg, sortPropArg, &l, &o, withProperties, withData)
					if err != nil {
						return nil, err
					}
					return page.Nodes, nil
				})
			} else {
				var page *api.FindNodesPage
				page, err = api.FindNodesProjected(cmd.Context(), client, searchArg, mode, filterArg, sortArg, sortPropArg, limitArg, offsetArg, withProperties, withData)
				if err == nil {
					rawNodes = page.Nodes
				}
			}
			if err != nil {
				return api.MapError(err)
			}

			// How many rows the PREDICATE matched — what #603's note actually
			// asks, and not the same as how many rows get displayed.
			//
			// The server will not answer it: `findNodes.total` is nullable and
			// this one leaves it NULL even when rows match — measured live, on
			// both the browse and the ranked path, after a first attempt built
			// the note on it and went silently dead. So the row count is all
			// there is, and it answers the question only when nothing narrowed
			// the result AFTER the predicate ran:
			//
			//	seq mode  — we page to exhaustion under our own offsets, so
			//	            rawNodes is the whole match set; --seq-gt and
			//	            --limit/--offset are applied below, client-side.
			//	            Trustworthy always.
			//	otherwise — the server applied --offset, so an empty page past
			//	            the last row says nothing about the predicate
			//	            (@codex, PR #604). Trustworthy only at offset 0.
			//
			// --limit never empties a non-empty match, so it does not disturb
			// either arm: zero rows under a limit really is zero matches.
			var matched *int
			if seqMode || offset == 0 {
				n := len(rawNodes)
				matched = &n
			}

			nodes := make([]nodeListDTO, 0, len(rawNodes))
			for _, n := range rawNodes {
				row := nodeListDTO{nodeDTO: nodeDTO{
					ID:         n.Id,
					MemoryID:   n.MemoryId,
					Loc:        n.Loc,
					Name:       n.Name,
					NodeType:   n.NodeType,
					Tags:       n.Tags,
					Seq:        n.Seq,
					IsRunnable: boolVal(n.IsRunnable),
					Role:       n.Role,
					UpdatedAt:  n.UpdatedAt,
				}}
				// Keyed off the FLAG, not off whether the server sent a value:
				// an unselected column and a selected-but-null one both arrive
				// as a nil pointer, so the response cannot tell them apart and
				// only the flag knows which was asked for (#602).
				if withProperties {
					row.Properties = cmdutil.ProjectedColumn(n.Properties)
				}
				if withData {
					row.Data = cmdutil.ProjectedColumn(n.Data)
				}
				nodes = append(nodes, row)
			}

			// Filter by seq > N
			if seqGt > 0 {
				filtered := make([]nodeListDTO, 0, len(nodes))
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

			// The silent-zero note (#603). Printed in BOTH output modes, unlike
			// search's degraded note, which is text-only because --json already
			// carries degraded/reason in its envelope. This listing marshals a
			// bare array with nowhere to put a caveat, so suppressing the note
			// under --json would leave an agent holding exactly the unqualified
			// `0` the note exists to qualify. It goes to stderr, so the --json
			// contract on stdout is untouched.
			if note := cmdutil.WhereDefaultColumnNote(whereArg, matched); note != "" {
				fmt.Fprintf(f.IOStreams.ErrOut, "note: %s\n", note)
			}

			return output.Write(f.IOStreams, f.JSON, nodes, func(w io.Writer) error {
				// A projected listing cannot use the table: the JSON values are
				// arbitrarily long, and a column would have to truncate them —
				// which is the same defect as not showing them, in a costume
				// that looks like an answer.
				if withProperties || withData {
					return writeProjected(w, nodes)
				}
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
	cmd.Flags().StringVar(&where, "where", "", cmdutil.WhereFlagUsage)
	cmd.Flags().StringVar(&sortProp, "sort-property", "", cmdutil.SortPropertyFlagUsage)
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of nodes")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	cmd.Flags().StringVar(&sortSeq, "sort-seq", "", "sort by seq: 'asc' or 'desc'")
	cmd.Flags().IntVar(&seqGt, "seq-gt", 0, "filter to nodes with seq > N")
	cmd.Flags().BoolVar(&withProperties, "with-properties", false, "include each node's properties JSONB in the output")
	cmd.Flags().BoolVar(&withData, "with-data", false, "include each node's data JSONB in the output")
	return cmd
}

// writeProjected renders the per-node block used when --with-properties or
// --with-data is set: the loc/name/type header, then each requested JSON column
// indented beneath it, whole and untruncated.
//
// A column that was requested but is null on the node prints `null` rather than
// being skipped, for the same reason the JSON key stays present: an absent line
// reads as "not requested", and the reader has no way to tell that from "this
// node has none". The DTO has already encoded that distinction — a requested
// column is non-empty here even when its value is null — so this loop tests the
// field's length and never needs the flags.
func writeProjected(w io.Writer, nodes []nodeListDTO) error {
	for i, n := range nodes {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		seqStr := ""
		if n.Seq != nil {
			seqStr = fmt.Sprintf("  seq %d", *n.Seq)
		}
		if _, err := fmt.Fprintf(w, "%s  %s  (%s)%s\n", n.Loc, n.Name, n.NodeType, seqStr); err != nil {
			return err
		}
		for _, col := range []struct {
			label string
			raw   json.RawMessage
		}{{"properties", n.Properties}, {"data", n.Data}} {
			if len(col.raw) == 0 {
				continue
			}
			if _, err := fmt.Fprintf(w, "  %s: %s\n", col.label, compactJSON(col.raw)); err != nil {
				return err
			}
		}
	}
	return nil
}

// compactJSON strips insignificant whitespace so one column is one line. A value
// json.Compact cannot parse is returned VERBATIM rather than dropped or
// error-rendered: it came off the wire as the node's stored column, and showing
// it as-is is strictly more informative than showing nothing.
func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}
