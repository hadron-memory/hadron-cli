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
	ID         string `json:"id"`
	Direction  string `json:"direction"` // outgoing | incoming
	Name       string `json:"name"`
	Loc        string `json:"loc"`
	IsRunnable bool   `json:"isRunnable"`
	Priority   int    `json:"priority"`
	OtherID    string `json:"otherNodeId"`
	OtherLoc   string `json:"otherNodeLoc"`
}

func edgeListRow(id, dir string, name *string, loc string, isRunnable *bool, priority int, otherID, otherLoc string) edgeListDTO {
	n := ""
	if name != nil {
		n = *name
	}
	run := false
	if isRunnable != nil {
		run = *isRunnable
	}
	return edgeListDTO{
		ID: id, Direction: dir, Name: n, Loc: loc, IsRunnable: run, Priority: priority,
		OtherID: otherID, OtherLoc: otherLoc,
	}
}

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var memory string
	cmd := &cobra.Command{
		Use:     "ls <node-urn> | <loc> -m <memory>",
		Aliases: []string{"list"},
		Short:   "List a node's edges (both directions)",
		Example: `  hadron edge ls hadronmemory.com::dev::start-here
  hadron edge ls start-here -m hadronmemory.com::dev`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.GetNode(cmd.Context(), client, id)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Node == nil {
				return exitcode.Newf(exitcode.NotFound, "node %q not found", args[0])
			}

			edges := []edgeListDTO{}
			for _, e := range resp.Node.OutgoingEdges {
				edges = append(edges, edgeListRow(e.Id, "outgoing", e.Name, e.Loc, e.IsRunnable, e.Priority, e.Target.Id, e.Target.Loc))
			}
			for _, e := range resp.Node.IncomingEdges {
				edges = append(edges, edgeListRow(e.Id, "incoming", e.Name, e.Loc, e.IsRunnable, e.Priority, e.Source.Id, e.Source.Loc))
			}

			return output.Write(f.IOStreams, f.JSON, edges, func(w io.Writer) error {
				t := output.NewTable(w, "DIR", "REL", "NODE", "EDGE-ID")
				for _, e := range edges {
					arrow := "→"
					if e.Direction == "incoming" {
						arrow = "←"
					}
					rel := e.Name
					if rel == "" {
						rel = e.Loc
					}
					t.Row(arrow, rel, e.OtherLoc, e.ID)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve a bare <loc> against")
	return cmd
}
