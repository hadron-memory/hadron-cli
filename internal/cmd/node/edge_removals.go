package node

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// edgeRemovalDTO is the stable --json shape for one hard-delete edge-removal
// audit entry (#1160). EdgeLoc and TargetLoc are nullable BY CONCEALMENT, not
// by absence: the server nulls them when the caller cannot read the memory the
// deleted target lived in, so null is honest data and must survive to the JSON
// output — never an empty string.
type edgeRemovalDTO struct {
	ID        string  `json:"id"`
	CreatedAt string  `json:"createdAt"`
	EdgeLoc   *string `json:"edgeLoc"`
	EdgeName  *string `json:"edgeName"`
	Reason    string  `json:"reason"`
	RemovedBy *string `json:"removedBy"`
	TargetID  string  `json:"targetId"`
	TargetLoc *string `json:"targetLoc"`
}

// edgeRemovalsDefaultLimit is the page `node edge-removals` asks for when
// --limit is unset, matching the server's own default (cli#967: hidden
// defaults stay at 20 or less, and the CLI names the page it asked for).
const edgeRemovalsDefaultLimit = 20

func newCmdEdgeRemovals(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var limit int
	cmd := &cobra.Command{
		Use:   "edge-removals <node-urn> | <loc> -m <memory> | <node-id>",
		Short: "List the edges a hard delete removed from this node",
		Long: `List the audit entries a HARD delete left on this node when it removed
edges the node held to the deleted target (#2517): one entry per removed
edge, newest first. A soft delete keeps its edges and records nothing.

An entry names the removed edge (its loc and name), the deleted target (its
stable id and loc), why, who deleted it, and when. The target's loc — and the
removed edge's loc, which is derived from it — are withheld (null) when you
cannot read the memory the target lived in.`,
		Example: `  hadron node edge-removals hrn:node:hadronmemory.com:dev:start-here
  hadron node edge-removals start-here -m hrn:mem:hadronmemory.com:dev --limit 5 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 {
				return exitcode.Newf(exitcode.Usage, "limit must be non-negative")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ref, err := revisionNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			// Like `node revision list`: the query has no offset or total,
			// so the history cannot be paged to its end (cli#967). An unset
			// --limit sends the explicit default rather than the server's
			// own, and a full default page is reported as "first N; more may
			// exist" — a page of exactly N does not prove older entries
			// exist.
			explicitLimit := cmd.Flags().Changed("limit")
			if !explicitLimit {
				limit = edgeRemovalsDefaultLimit
			}
			limitArg := &limit
			resp, err := gen.NodeEdgeRemovals(cmd.Context(), client, ref, limitArg)
			if err != nil {
				return api.MapError(err)
			}
			dtos := []edgeRemovalDTO{}
			for _, r := range resp.NodeEdgeRemovals {
				dtos = append(dtos, edgeRemovalDTO{
					ID:        r.Id,
					CreatedAt: r.CreatedAt,
					EdgeLoc:   r.EdgeLoc,
					EdgeName:  r.EdgeName,
					Reason:    r.Reason,
					RemovedBy: r.RemovedBy,
					TargetID:  r.TargetId,
					TargetLoc: r.TargetLoc,
				})
			}
			if !explicitLimit && len(dtos) >= limit {
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "Showing the first %d edge-removal entries; more may exist. Pass --limit N for a larger page.\n", limit)
			}
			return output.Write(f.IOStreams, f.JSON, dtos, func(w io.Writer) error {
				t := output.NewTable(w, "WHEN", "EDGE", "EDGE-NAME", "TARGET-ID", "TARGET-LOC", "REASON", "REMOVED-BY")
				for _, d := range dtos {
					t.Row(
						output.SafeCell(d.CreatedAt),
						output.SafeCell(withheldCell(d.EdgeLoc)),
						output.SafeCell(output.Dash(d.EdgeName)),
						output.SafeCell(d.TargetID),
						output.SafeCell(withheldCell(d.TargetLoc)),
						output.SafeCell(d.Reason),
						output.SafeCell(output.Dash(d.RemovedBy)),
					)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve a bare <loc> against")
	cmd.Flags().IntVar(&limit, "limit", 0, "max entries to return (default 20; a full default page says more may exist)")
	return cmd
}

// withheldCell renders a loc the server withheld — null because the caller
// cannot read the memory the deleted target lived in — as an explicit marker
// rather than a blank that reads as missing data. Distinct from Dash: a nil
// here is concealment (the entry exists, the address does not), not absence.
func withheldCell(s *string) string {
	if s == nil {
		return "(withheld)"
	}
	return *s
}
