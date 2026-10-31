package node

import (
	"fmt"
	"io"
	"math"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type provenanceSelectionDTO struct {
	Rev      *int `json:"rev"`
	Approved bool `json:"approved"`
	Signed   bool `json:"signed"`
}
type provenancePositionDTO struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}
type provenanceOccurrenceDTO struct {
	Order          int                    `json:"order"`
	Depth          int                    `json:"depth"`
	Parent         *int                   `json:"parent"`
	Kind           string                 `json:"kind"`
	NodeID         string                 `json:"nodeId"`
	RevNo          int                    `json:"revNo"`
	Hash           string                 `json:"hash"`
	ReplacesNodeID *string                `json:"replacesNodeId"`
	Position       *provenancePositionDTO `json:"position"`
	Selection      provenanceSelectionDTO `json:"selection"`
	Pruned         *bool                  `json:"pruned"`
}
type provenancePageDTO struct {
	NodeID      string                    `json:"nodeId"`
	RevNo       int                       `json:"revNo"`
	ContentHash string                    `json:"contentHash"`
	Version     int                       `json:"version"`
	Pruned      bool                      `json:"pruned"`
	Items       []provenanceOccurrenceDTO `json:"items"`
	Total       int                       `json:"total"`
	HasMore     bool                      `json:"hasMore"`
	Limit       int                       `json:"limit"`
	Offset      int                       `json:"offset"`
}

func newCmdProvenance(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var revision, limit, offset int
	var all bool
	cmd := &cobra.Command{
		Use:   "provenance <compiled-node-ref> --revision <n>",
		Short: "Show which sources a compiled boot prompt was built from (maintainers)",
		Long: `For maintainers of Agent boot prompts. hadron agent compile builds an Agent's
boot prompt (its standing instructions) from several source notes, and saves
the result as a new revision of a compiled node. This command shows what went
into one such revision: each source note it included, in order and nesting
depth, with the source revision and content hash that were used. It shows no
source text, and changes nothing.

Name the compiled node by its id or URN (a bare loc needs -m), and give
--revision <n>, the revision number that agent compile printed (node get
--json shows the current one). The answer is what was recorded when that
revision was compiled, not the sources the node links to now.

The table lists each source and its status.
--json includes the full recorded occurrence: its parent, hash, replacement,
position and selection settings.

Reading needs read access to the compiled node's history. A node that is
missing, deleted, not compiled, or not readable by you exits 4. Missing or
damaged provenance is a server error, never an empty list. (The served-prompt
reads differ: an unavailable served record is a successful null, exit 0.)

"pruned" on the compiled revision says whether its text has been removed. On
a source, pruned is true or false only when you can read that source's
current version and any kept snapshot; null means you cannot see it, not that
it was kept or removed.

Paging: 20 sources per page by default (--limit, at most 100); --offset skips
some; --limit 0 prints counts only. --all reads every page and prints only
once all have arrived; it excludes --offset and --limit 0. If any page fails,
or the compiled revision changes between pages, the whole read fails rather
than returning part of it. Pages are not one snapshot: a source's
availability can change between them.`,
		Example: "  hadron node provenance hrn:node:acme.com:iris-system:boot:compiled --revision 7 --json\n  hadron node provenance boot:compiled -m hrn:mem:acme.com:iris-system --revision 7 --all\n  hadron node provenance hrn:node:acme.com:iris-system:boot:compiled --revision 7 --limit 0",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if revision < 1 || revision > math.MaxInt32 {
				return exitcode.Newf(exitcode.Usage, "--revision must be between 1 and 2147483647")
			}
			if limit < 0 || limit > 100 {
				return exitcode.Newf(exitcode.Usage, "--limit must be between 0 (counts only) and 100")
			}
			if offset < 0 || offset > math.MaxInt32 {
				return exitcode.Newf(exitcode.Usage, "--offset must be between 0 and 2147483647")
			}
			if all && (limit == 0 || cmd.Flags().Changed("offset")) {
				return exitcode.Newf(exitcode.Usage, "--all excludes --offset and --limit 0")
			}
			ref, err := cmdutil.BatchNodeRef(memory, args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var dto provenancePageDTO
			next := offset
			lastOrder := -1
			for {
				r, err := gen.CompiledPromptProvenance(cmd.Context(), client, ref, revision, limit, next)
				if err != nil {
					return api.MapError(err)
				}
				if r.CompiledPromptProvenance == nil {
					return exitcode.Newf(exitcode.NotFound, "compiled revision provenance is not available (missing, deleted, noncompiled or not readable by you)")
				}
				p := r.CompiledPromptProvenance
				if p.Items == nil || p.Total < 0 || len(p.Items) > p.Total || p.RevNo != revision || len(p.Items) > limit || p.NodeId == "" || p.ContentHash == "" || (limit > 0 && p.HasMore && len(p.Items) == 0) {
					return exitcode.Newf(exitcode.Error, "server returned an invalid provenance page")
				}
				if next == offset {
					dto = provenancePageDTO{NodeID: p.NodeId, RevNo: p.RevNo, ContentHash: p.ContentHash, Version: p.Version, Pruned: p.Pruned, Items: []provenanceOccurrenceDTO{}, Total: p.Total, Limit: limit, Offset: offset}
				} else if dto.NodeID != p.NodeId || dto.RevNo != p.RevNo || dto.ContentHash != p.ContentHash || dto.Version != p.Version || dto.Total != p.Total || dto.Pruned != p.Pruned {
					return exitcode.Newf(exitcode.Conflict, "provenance identity, count or compiled pruning state changed while paging; no partial record returned")
				}
				for _, item := range p.Items {
					if item == nil || item.Selection == nil || item.Order <= lastOrder {
						return exitcode.Newf(exitcode.Error, "server returned an invalid provenance occurrence")
					}
					lastOrder = item.Order
					row := provenanceOccurrenceDTO{Order: item.Order, Depth: item.Depth, Parent: item.Parent, Kind: item.Kind, NodeID: item.NodeId, RevNo: item.RevNo, Hash: item.Hash, ReplacesNodeID: item.ReplacesNodeId, Selection: provenanceSelectionDTO{item.Selection.Rev, item.Selection.Approved, item.Selection.Signed}, Pruned: item.Pruned}
					if item.Position != nil {
						row.Position = &provenancePositionDTO{item.Position.Line, item.Position.Column}
					}
					dto.Items = append(dto.Items, row)
				}
				dto.HasMore = p.HasMore
				if !all || !p.HasMore {
					break
				}
				if len(p.Items) == 0 || next > math.MaxInt32-len(p.Items) || next+len(p.Items) >= p.Total {
					return exitcode.Newf(exitcode.Error, "server provenance pagination did not advance consistently; no partial record returned")
				}
				next += len(p.Items)
			}
			if all && len(dto.Items) != dto.Total {
				return exitcode.Newf(exitcode.Error, "server returned incomplete provenance; no partial record returned")
			}
			if !all && limit > 0 && dto.HasMore {
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "showing %d of %d occurrences; next: --offset %d (or --all)\n", len(dto.Items), dto.Total, offset+len(dto.Items))
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				state := "retained"
				if dto.Pruned {
					state = "pruned"
				}
				if _, err := fmt.Fprintf(w, "compiled node: %s revision %d\ncontent hash: %s\ncompiled revision text: %s\noccurrences: %d of %d (offset %d)\n", output.SafeCell(dto.NodeID), dto.RevNo, output.SafeCell(dto.ContentHash), state, len(dto.Items), dto.Total, dto.Offset); err != nil {
					return err
				}
				t := output.NewTable(w)
				t.Row("ORDER", "DEPTH", "KIND", "SOURCE NODE", "REVISION", "SOURCE TEXT")
				for _, item := range dto.Items {
					status := "not visible"
					if item.Pruned != nil {
						status = "retained"
						if *item.Pruned {
							status = "pruned"
						}
					}
					t.Row(fmt.Sprint(item.Order), fmt.Sprint(item.Depth), output.SafeCell(item.Kind), output.SafeCell(item.NodeID), fmt.Sprint(item.RevNo), status)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory for a bare compiled-node loc")
	cmd.Flags().IntVar(&revision, "revision", 0, "exact compiled node revision (required)")
	cmd.Flags().IntVar(&limit, "limit", 20, "occurrences per page, 0 for counts only (0-100)")
	cmd.Flags().IntVar(&offset, "offset", 0, "occurrences to skip (excludes --all)")
	cmd.Flags().BoolVar(&all, "all", false, "read every occurrence page; fail without partial output if any page fails")
	_ = cmd.MarkFlagRequired("revision")
	return cmd
}
