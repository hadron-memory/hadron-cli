package node

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
	var yes, hard bool
	var memory string
	cmd := &cobra.Command{
		Use:     "delete <node-urn> | <loc> -m <memory>",
		Aliases: []string{"rm"},
		Short:   "Delete a node",
		Long: `Delete a node. By default this is a soft delete — the node disappears from
reads but is recoverable from version history.

--hard removes the row entirely, cascading its edges and version history
(#391). This bypasses version-history recovery and is irreversible, so it
prompts with a distinct warning (still gated by --yes non-interactively).`,
		Example: `  hadron node rm acme.com::kb::findings:flaky-ci --yes
  hadron node rm findings:flaky-ci -m acme.com::kb --yes
  hadron node rm acme.com::kb::data:stale --hard --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			node, err := fetchNode(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			// A hard delete bypasses version-history recovery, so name that in
			// the prompt — the default ConfirmDeletion line understates it.
			what := "node " + args[0]
			if hard {
				what += " (hard — permanently removes the row, its edges, and version history)"
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, what); err != nil {
				return err
			}
			var hardArg *bool
			if hard {
				hardArg = &hard
			}
			// #542: deleteNode takes a single nodeRef. fetchNode already resolved
			// the target (URN or <loc> -m <memory>) to a concrete row, so pass its
			// immutable PK.
			if _, err := gen.DeleteNode(cmd.Context(), client, node.Id, hardArg); err != nil {
				return api.MapError(err)
			}
			status, verb := "deleted", "Deleted"
			if hard {
				status, verb = "hard-deleted", "Hard-deleted"
			}
			dto := map[string]string{"loc": node.Loc, "memoryId": node.MemoryId, "status": status}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ %s node %s\n", verb, args[0])
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve a bare <loc> against")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&hard, "hard", false, "permanently remove the row (cascades edges + version history; irreversible)")
	return cmd
}
