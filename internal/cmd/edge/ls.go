package edge

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// edgeListDTO is one row in `edge ls` output.
type edgeListDTO struct {
	ID        string `json:"id"`
	Direction string `json:"direction"` // outgoing | incoming
	Label     string `json:"label"`
	Priority  int    `json:"priority"`
	OtherID   string `json:"otherNodeId"`
	OtherLoc  string `json:"otherNodeLoc"`
}

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "ls <node-urn>",
		Aliases: []string{"list"},
		Short:   "List a node's edges (both directions)",
		Example: `  hadron edge ls hadronmemory.com:dev:start-here`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := resolveNodeURN(cmd, client, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.GetNodeById(cmd.Context(), client, id)
			if err != nil {
				return api.MapError(err)
			}
			if resp.NodeById == nil {
				return exitcode.Newf(exitcode.NotFound, "node %q not found", args[0])
			}

			edges := []edgeListDTO{}
			for _, e := range resp.NodeById.OutgoingEdges {
				edges = append(edges, edgeListDTO{
					ID: e.Id, Direction: "outgoing", Label: e.Label, Priority: e.Priority,
					OtherID: e.Target.Id, OtherLoc: e.Target.Loc,
				})
			}
			for _, e := range resp.NodeById.IncomingEdges {
				edges = append(edges, edgeListDTO{
					ID: e.Id, Direction: "incoming", Label: e.Label, Priority: e.Priority,
					OtherID: e.Source.Id, OtherLoc: e.Source.Loc,
				})
			}

			return output.Write(f.IOStreams, f.JSON, edges, func(w io.Writer) error {
				t := output.NewTable(w, "DIR", "LABEL", "NODE", "EDGE-ID")
				for _, e := range edges {
					arrow := "→"
					if e.Direction == "incoming" {
						arrow = "←"
					}
					t.Row(arrow, e.Label, e.OtherLoc, e.ID)
				}
				return t.Flush()
			})
		},
	}
}
