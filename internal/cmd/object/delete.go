package object

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

func newCmdDelete(f *cmdutil.Factory) *cobra.Command {
	var yes, hard bool
	cmd := &cobra.Command{
		Use:     "delete <ref>",
		Aliases: []string{"rm"},
		Short:   "Delete an object",
		Long: `Delete one record by its id or node URN. Asks for confirmation on a terminal;
pass --yes in scripts.

By default the record is hidden from every read but kept: bring it back with
hadron node restore <id or URN>. --hard removes it permanently, and it cannot
be restored.`,
		Example: `  hadron object delete 01a189a8175c7145b6c3680628ef0101 --yes
  hadron node restore 01a189a8175c7145b6c3680628ef0101
  hadron object rm hrn:node:acme.com:market:competitor:letta --hard --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			what := "object " + args[0]
			if hard {
				what += " (hard — permanently removes the row)"
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, what); err != nil {
				return err
			}
			var hardArg *bool
			if hard {
				hardArg = &hard
			}
			resp, err := gen.DeleteObject(cmd.Context(), client, cmdutil.CanonicalNodeRef(args[0]), hardArg)
			if err != nil {
				return api.MapError(err)
			}
			// deleteObject returns true on success; a false result means nothing was
			// removed (no such object / already gone) — don't report a phantom
			// success. Unlike node rm, object delete doesn't pre-resolve the ref, so
			// this is a real outcome to surface.
			if resp == nil || !resp.DeleteObject {
				return exitcode.Newf(exitcode.NotFound, "object %q not found or already deleted", args[0])
			}
			status, verb := "deleted", "Deleted"
			if hard {
				status, verb = "hard-deleted", "Hard-deleted"
			}
			dto := deleteDTO{Ref: args[0], Status: status}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, werr := fmt.Fprintf(w, "✓ %s object %s\n", verb, args[0])
				return werr
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&hard, "hard", false, "delete permanently (cannot be restored)")
	return cmd
}

// deleteDTO is the stable --json shape for an object delete.
type deleteDTO struct {
	Ref    string `json:"ref"`
	Status string `json:"status"`
}
