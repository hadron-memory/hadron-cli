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

// rmDTO is the stable --json shape for a delete. Recursive is a real boolean,
// omitted when false so an ordinary delete's shape is unchanged.
type rmDTO struct {
	Loc       string `json:"loc"`
	MemoryID  string `json:"memoryId"`
	Status    string `json:"status"`
	Recursive bool   `json:"recursive,omitempty"`
}

func newCmdRm(f *cmdutil.Factory) *cobra.Command {
	var yes, hard, recursive, cascade bool
	var memory string
	cmd := &cobra.Command{
		Use:     "delete <node-urn> | <loc> -m <memory>",
		Aliases: []string{"rm"},
		Short:   "Delete a node",
		Long: `Delete a node. By default this is a soft delete: the node disappears from
reads but can be brought back. node recovery list shows deleted nodes, and
node restore <id|urn> restores one, with its links.

--hard removes the node entirely, with its edges and version history. It
cannot be restored and cannot be undone, so it
prompts with a distinct warning (still gated by --yes non-interactively).

--recursive/-r deletes the node PLUS every descendant under its loc prefix.
Without it, deleting a node that has descendants is refused (so a subtree is
never silently orphaned) — the error names the descendant count and points you
at --recursive.

A hard delete that would remove an edge linking to a node in another memory is
refused (that memory may not be yours). --cascade-cross-memory-edges, with
--hard, removes those edges too.`,
		Example: `  hadron node rm hrn:node:acme.com:kb:findings:flaky-ci --yes
  hadron node rm findings:flaky-ci -m hrn:mem:acme.com:kb --yes
  hadron node rm hrn:node:acme.com:kb:data:stale --hard --yes
  hadron node rm hrn:node:acme.com:kb:findings --recursive --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The server reads it only on a hard delete, so without --hard it
			// would be silently ignored.
			if cascade && !hard {
				return exitcode.Newf(exitcode.Usage, "--cascade-cross-memory-edges applies only to a hard delete: add --hard. Nothing was deleted")
			}
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
				if cascade {
					what += ", including edges that link to other memories"
				}
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, what); err != nil {
				return err
			}
			var hardArg, recursiveArg, cascadeArg *bool
			if hard {
				hardArg = &hard
			}
			if recursive {
				recursiveArg = &recursive
			}
			if cascade {
				cascadeArg = &cascade
			}
			// #542: deleteNode takes a single nodeRef. fetchNode already resolved
			// the target (URN or <loc> -m <memory>) to a concrete row, so pass its
			// immutable PK.
			if _, err := gen.DeleteNode(cmd.Context(), client, node.Id, hardArg, recursiveArg, cascadeArg); err != nil {
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
				// cli#1341: a hard delete of a node with edges to other memories
				// refuses with CROSS_MEMORY_EDGES and names the GraphQL argument;
				// name the flag instead.
				if api.HasErrorCode(err, "CROSS_MEMORY_EDGES") {
					if n := api.ExtensionCount(err, "CROSS_MEMORY_EDGES"); n >= 0 {
						return exitcode.Newf(exitcode.Usage,
							"%q has %d edge(s) linking it to other memories; pass --cascade-cross-memory-edges with --hard to delete them too", node.Loc, n)
					}
					return exitcode.Newf(exitcode.Usage,
						"%q has edges linking it to other memories; pass --cascade-cross-memory-edges with --hard to delete them too", node.Loc)
				}
				return api.MapError(err)
			}
			status, verb := "deleted", "Deleted"
			if hard {
				status, verb = "hard-deleted", "Hard-deleted"
			}
			// Explicit DTO (not a map) so --json stays a stable, correctly-typed
			// contract: recursive is a real boolean, omitted when false.
			dto := rmDTO{Loc: node.Loc, MemoryID: node.MemoryId, Status: status, Recursive: recursive}
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
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve a bare <loc> against")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&hard, "hard", false, "permanently remove the row (cascades edges + version history; irreversible)")
	cmd.Flags().BoolVar(&cascade, "cascade-cross-memory-edges", false, "with --hard: also remove edges linking to nodes in other memories (otherwise refused)")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "also delete every descendant under the node's loc (required to delete a node that has children)")
	return cmd
}
