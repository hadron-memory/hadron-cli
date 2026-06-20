package spec

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	var memory, prefix string
	var abstractOnly, bodyOnly bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "get [<citation>]",
		Short: "Show a spec by its citation, or a whole prefix with --prefix",
		Long: `Show a spec node: its abstract, edges, body, and a lint summary.

Pass a single <citation>, or --prefix <citation-prefix> to dump every spec
under that prefix (one feature, one module, or the whole product) with the
same per-node detail — a client-side fan-out over the existing reads, handy
for reviewing or context-stuffing a whole branch in one call. By default every
spec under the prefix is fetched (the listing is paged to exhaustion); pass
--limit (with optional --offset) to fetch a single explicit page instead.

--abstract-only prints metadata + abstract without the body. --body-only prints
just the raw markdown body of a single spec (no metadata) — pipe it into
` + "`hadron node update --content -`" + ` for a clean edit round-trip. --json emits
one object for a single citation, an array for --prefix.`,
		Example: `  hadron spec get msg:010:02 -m micromentor.org::platform-specs
  hadron spec get cor:dmo:060:02 -m hadronmemory.com::platform-specs --body-only
  hadron spec get --prefix cor:cht -m hadronmemory.com::platform-specs
  hadron spec get --prefix cor:dmo -m hadronmemory.com::platform-specs --abstract-only --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			memURN, err := memoryURNFromFlag(memory)
			if err != nil {
				return err
			}
			// Exactly one of <citation> or --prefix.
			if (len(args) == 0) == (prefix == "") {
				return exitcode.Newf(exitcode.Usage, "provide a <citation> or --prefix <prefix> (exactly one)")
			}
			if bodyOnly && abstractOnly {
				return exitcode.Newf(exitcode.Usage, "--body-only and --abstract-only are mutually exclusive")
			}
			if bodyOnly && prefix != "" {
				return exitcode.Newf(exitcode.Usage, "--body-only takes a single <citation>, not --prefix")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			// Single citation — behavior unchanged.
			if prefix == "" {
				n, err := fetchSpecNode(cmd, client, memURN, args[0])
				if err != nil {
					return err
				}
				if bodyOnly {
					body := ""
					if n.Content != nil {
						body = *n.Content
					}
					return output.Write(f.IOStreams, f.JSON, specBodyDTO{Citation: n.Loc, Content: body}, func(w io.Writer) error {
						fmt.Fprint(w, body)
						if !strings.HasSuffix(body, "\n") {
							fmt.Fprintln(w)
						}
						return nil
					})
				}
				dto := specDetailFromNode(n, !abstractOnly)
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					renderSpecDetail(w, memURN, dto)
					return nil
				})
			}

			// Prefix dump — list specs under the prefix, then fetch each
			// node's detail. By default page to exhaustion (#23); an explicit
			// --limit/--offset is honored verbatim as a single page, mirroring
			// `spec ls`.
			prefixArg := prefix
			var listed []*gen.NodesNodesNode
			if limit > 0 || offset > 0 {
				var limitArg, offsetArg *int
				if limit > 0 {
					limitArg = &limit
				}
				if offset > 0 {
					offsetArg = &offset
				}
				resp, rerr := gen.Nodes(cmd.Context(), client, &memURN, &prefixArg, nil, []string{"spec"}, nil, limitArg, offsetArg)
				if rerr != nil {
					return api.MapError(rerr)
				}
				listed = resp.Nodes
			} else {
				listed, err = scanAllNodes(cmd.Context(), client, &memURN, &prefixArg, []string{"spec"})
				if err != nil {
					return err
				}
			}

			ids := make([]string, 0, len(listed))
			for _, n := range listed {
				if n == nil {
					continue
				}
				if _, perr := ParseCitation(n.Loc); perr != nil {
					continue // only citation-shaped nodes are specs
				}
				ids = append(ids, n.Id)
			}

			// Bulk-read the full nodes (cor:api:040) rather than one GetNodeById
			// per spec — ceil(N/200) round-trips instead of N.
			batched, unavailable, berr := api.CollectNodeBatch(ids, func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
				resp, ferr := gen.NodeBatch(cmd.Context(), client, chunk)
				if ferr != nil {
					return nil, api.MapError(ferr)
				}
				return resp.NodeBatch, nil
			})
			if berr != nil {
				return berr
			}
			if len(unavailable) > 0 {
				fmt.Fprintf(f.IOStreams.ErrOut, "note: %d spec(s) listed but could not be read\n", len(unavailable))
			}

			details := make([]specDetailDTO, 0, len(batched))
			for _, bn := range batched {
				if bn == nil {
					continue
				}
				details = append(details, specDetailFromNode(nodeByIDFromBatch(bn), !abstractOnly))
			}
			// Bulk reads don't preserve order across chunks — sort for a
			// deterministic dump.
			sort.Slice(details, func(i, j int) bool { return details[i].Citation < details[j].Citation })

			return output.Write(f.IOStreams, f.JSON, details, func(w io.Writer) error {
				fmt.Fprintf(w, "%d spec(s) under %s\n", len(details), prefix)
				for _, d := range details {
					fmt.Fprintln(w, "\n────────────────────────────────────────")
					renderSpecDetail(w, memURN, d)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "dump every spec under this citation prefix (e.g. cor:cht)")
	cmd.Flags().BoolVar(&abstractOnly, "abstract-only", false, "print metadata + abstract, omit the body")
	cmd.Flags().BoolVar(&bodyOnly, "body-only", false, "print only the raw markdown body of a single spec (for a clean edit round-trip)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max specs to fetch as one page in --prefix mode (default: all)")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset for --prefix mode (implies a single page)")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// specDetailFromNode projects a fetched node into the stable detail DTO and
// computes its per-node lint findings. includeContent gates the body (false
// for --abstract-only).
func specDetailFromNode(n *gen.GetNodeByIdNodeByIdNode, includeContent bool) specDetailDTO {
	findings := lintNode(nodeFromGQL(n))
	dto := specDetailDTO{
		Citation:  n.Loc,
		MemoryID:  n.MemoryId,
		Name:      n.Name,
		NodeType:  n.NodeType,
		Tags:      n.Tags,
		Abstract:  n.Abstract,
		Data:      n.Data,
		Lint:      findings,
		UpdatedAt: n.UpdatedAt,
	}
	if includeContent {
		dto.Content = n.Content
	}
	for _, e := range n.OutgoingEdges {
		if e != nil && e.Target != nil {
			dto.Edges = append(dto.Edges, specEdgeDTO{Direction: "out", Label: e.Label, Loc: e.Target.Loc, MemoryID: e.Target.MemoryId})
		}
	}
	for _, e := range n.IncomingEdges {
		if e != nil && e.Source != nil {
			dto.Edges = append(dto.Edges, specEdgeDTO{Direction: "in", Label: e.Label, Loc: e.Source.Loc, MemoryID: e.Source.MemoryId})
		}
	}
	return dto
}

// nodeByIDFromBatch adapts a bulk NodeBatch node into the GetNodeById shape, so
// the detail/lint builders (specDetailFromNode, nodeFromGQL) run unchanged on
// either fetch path. Only the fields those builders read are carried over —
// scalars, data (for data.version), and both edge directions with target/source
// loc + memoryId; everything else stays zero.
func nodeByIDFromBatch(b *gen.NodeBatchNodeBatchNodeBatchResultNodesNode) *gen.GetNodeByIdNodeByIdNode {
	n := &gen.GetNodeByIdNodeByIdNode{
		Id:                 b.Id,
		MemoryId:           b.MemoryId,
		Loc:                b.Loc,
		Name:               b.Name,
		NodeType:           b.NodeType,
		Tags:               b.Tags,
		Abstract:           b.Abstract,
		AbstractOriginHash: b.AbstractOriginHash,
		Content:            b.Content,
		Data:               b.Data,
		UpdatedAt:          b.UpdatedAt,
	}
	for _, e := range b.OutgoingEdges {
		if e == nil || e.Target == nil {
			continue
		}
		n.OutgoingEdges = append(n.OutgoingEdges, &gen.GetNodeByIdNodeByIdNodeOutgoingEdgesEdge{
			Label:  e.Label,
			Target: &gen.GetNodeByIdNodeByIdNodeOutgoingEdgesEdgeTargetNode{Id: e.Target.Id, Loc: e.Target.Loc, MemoryId: e.Target.MemoryId},
		})
	}
	for _, e := range b.IncomingEdges {
		if e == nil || e.Source == nil {
			continue
		}
		n.IncomingEdges = append(n.IncomingEdges, &gen.GetNodeByIdNodeByIdNodeIncomingEdgesEdge{
			Label:  e.Label,
			Source: &gen.GetNodeByIdNodeByIdNodeIncomingEdgesEdgeSourceNode{Id: e.Source.Id, Loc: e.Source.Loc, MemoryId: e.Source.MemoryId},
		})
	}
	return n
}

// renderSpecDetail writes the human-readable single-spec view. Shared by the
// single-citation and --prefix paths so both render identically.
func renderSpecDetail(w io.Writer, memURN string, d specDetailDTO) {
	fmt.Fprintln(w, d.Name)
	fmt.Fprintln(w, specNodeRef(memURN, d.Citation))
	if len(d.Tags) > 0 {
		fmt.Fprintf(w, "Tags: %s\n", strings.Join(d.Tags, ", "))
	}
	if d.Abstract != nil && strings.TrimSpace(*d.Abstract) != "" {
		fmt.Fprintf(w, "\nAbstract:\n%s\n", strings.TrimSpace(*d.Abstract))
	}
	if d.Data != nil && len(*d.Data) > 0 && strings.TrimSpace(string(*d.Data)) != "null" {
		fmt.Fprintf(w, "\nData:\n%s\n", strings.TrimSpace(string(*d.Data)))
	}
	if len(d.Edges) > 0 {
		fmt.Fprintln(w, "\nEdges:")
		for _, e := range d.Edges {
			arrow := "→"
			if e.Direction == "in" {
				arrow = "←"
			}
			fmt.Fprintf(w, "  %s %s  %s\n", arrow, e.Loc, e.Label)
		}
	}
	if len(d.Lint) == 0 {
		fmt.Fprintln(w, "\nLint: ✓ ok")
	} else {
		fmt.Fprintf(w, "\nLint: %d finding(s)\n", len(d.Lint))
		for _, fnd := range d.Lint {
			fmt.Fprintf(w, "  [%s] %s: %s\n", fnd.Severity, fnd.Rule, fnd.Message)
		}
	}
	if d.Content != nil && strings.TrimSpace(*d.Content) != "" {
		fmt.Fprintf(w, "\n%s\n", *d.Content)
	}
}
