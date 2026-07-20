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

func newCmdRm(f *cmdutil.Factory) *cobra.Command {
	var yes, hard, recursive bool
	var memory string
	cmd := &cobra.Command{
		Use:     "delete <node-urn> | <loc> -m <memory>",
		Aliases: []string{"rm"},
		Short:   "Delete a node",
		Long: `Delete a node. By default this is a soft delete — the node disappears from
reads but is recoverable from version history.

--hard removes the row entirely, cascading its edges and version history
(#391). This bypasses version-history recovery and is irreversible, so it
prompts with a distinct warning (still gated by --yes non-interactively).

--recursive/-r deletes the node PLUS every descendant under its loc prefix.
Without it, deleting a node that has descendants is refused (so a subtree is
never silently orphaned) — the error names the descendant count and points you
at --recursive.`,
		Example: `  hadron node rm acme.com::kb::findings:flaky-ci --yes
  hadron node rm findings:flaky-ci -m acme.com::kb --yes
  hadron node rm acme.com::kb::data:stale --hard --yes
  hadron node rm acme.com::kb::findings --recursive --yes`,
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
			// Name the subtree and the hard-delete blast radius in the prompt —
			// a recursive hard delete is the most destructive form, and the
			// default ConfirmDeletion line understates both.
			what := "node " + args[0]
			if recursive {
				what += " and its entire subtree (all descendant nodes)"
			}
			if hard {
				if recursive {
					what += " — HARD: permanently removes every node in the subtree, their edges, and version history"
				} else {
					what += " (hard — permanently removes the row, its edges, and version history)"
				}
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, what); err != nil {
				return err
			}
			var hardArg, recursiveArg *bool
			if hard {
				hardArg = &hard
			}
			if recursive {
				recursiveArg = &recursive
			}
			// #542: deleteNode takes a single nodeRef. fetchNode already resolved
			// the target (URN or <loc> -m <memory>) to a concrete row, so pass its
			// immutable PK.
			if _, err := gen.DeleteNode(cmd.Context(), client, node.Id, hardArg, recursiveArg); err != nil {
				// #239 / server #661: a non-recursive delete of a node with
				// descendants refuses with NODE_HAS_DESCENDANTS. Re-message it in
				// CLI terms (loc + count + the --recursive flag) rather than
				// surfacing the raw "pass recursive: true" GraphQL wording.
				if api.HasErrorCode(err, "NODE_HAS_DESCENDANTS") {
					hint := "--recursive"
					if hard {
						hint = "--recursive --hard"
					}
					if n := api.DescendantCount(err); n >= 0 {
						return exitcode.Newf(exitcode.Usage,
							"%q has %d descendant(s); pass %s to delete the whole subtree", node.Loc, n, hint)
					}
					return exitcode.Newf(exitcode.Usage,
						"%q has descendants; pass %s to delete the whole subtree", node.Loc, hint)
				}
				return api.MapError(err)
			}
			status, verb := "deleted", "Deleted"
			if hard {
				status, verb = "hard-deleted", "Hard-deleted"
			}
			dto := map[string]string{"loc": node.Loc, "memoryId": node.MemoryId, "status": status}
			if recursive {
				dto["recursive"] = "true"
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				suffix := ""
				if recursive {
					suffix = " and its subtree"
				}
				_, err := fmt.Fprintf(w, "✓ %s node %s%s\n", verb, args[0], suffix)
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve a bare <loc> against")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&hard, "hard", false, "permanently remove the row (cascades edges + version history; irreversible)")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "also delete every descendant under the node's loc (required to delete a node that has children)")
	return cmd
}
