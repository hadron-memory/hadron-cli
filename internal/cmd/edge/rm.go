package edge

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <edge-id>",
		Aliases: []string{"delete"},
		Short:   "Delete an edge (the link only; both nodes stay)",
		Long: `Delete one edge: the link between two nodes. Both nodes stay as they are;
only the link is removed. Find the edge id in the EDGE-ID column of
hadron edge list <node>, or in the output of edge create; an edge URN
(hrn:edge:...) works too.

The deletion is permanent: there is no undo and no edge history to restore it
from. To get the link back, create it again with edge create. You need write
access to the memory the edge belongs to (the one its starting node is in).
Links the platform maintains itself, such as those that anchor a comment, are
refused.

On a terminal it first asks "Delete edge <edge-id>? This cannot be undone.
(y/N)". Without a terminal (a script or an AI agent) pass --yes; otherwise it
deletes nothing and exits 2. On success it prints "✓ Deleted edge <edge-id>".`,
		Example: `  hadron edge list start-here -m hrn:mem:acme.com:kb
  hadron edge rm 019f0c2a5e7b7d3e9a1c4b6d8e0f2a35
  hadron edge rm 019f0c2a5e7b7d3e9a1c4b6d8e0f2a35 --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "edge "+args[0]); err != nil {
				return err
			}
			if _, err := gen.DeleteEdge(cmd.Context(), client, args[0]); err != nil {
				return api.MapError(err)
			}
			dto := map[string]string{"edge": args[0], "status": "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Deleted edge %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "delete without asking (required when not on a terminal)")
	return cmd
}
