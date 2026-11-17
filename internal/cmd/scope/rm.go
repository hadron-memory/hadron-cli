package scope

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
	var (
		yes    bool
		byName bool
	)
	cmd := &cobra.Command{
		Use:     "rm <name|id>",
		Aliases: []string{"delete"},
		Short:   "Delete a scope",
		Long: `Delete a scope. Its name is free for reuse immediately.

Deleting a scope removes the lens, never the memories it listed.

A NAME resolves in an App's context (App > Agent > organization), taken from
the persistent --app flag or the active App, so the same name can be a
different scope in another App; an ID needs no context and ignores --app.
Look at the exact target first with ` + "`hadron scope get <name|id>`" + ` (same
--app), then delete it. Without --yes you are asked to confirm on a terminal;
a script or agent must pass --yes.`,
		Example: `  hadron scope get research --app hrn:app:acme.com:dev-team
  hadron scope rm research --app hrn:app:acme.com:dev-team --yes
  hadron scope rm 01a0a702033c7099b23cbd358cd0c269 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Resolve BEFORE prompting, so the confirmation names a scope that
			// actually exists rather than asking the user to approve deleting
			// something we then fail to find.
			id, err := resolveScopeID(cmd, f, client, args[0], byName)
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "scope "+args[0]); err != nil {
				return err
			}
			if _, err := gen.DeleteScope(cmd.Context(), client, id); err != nil {
				return api.MapError(err)
			}
			dto := map[string]string{"id": id, "status": "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ deleted scope %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&byName, "by-name", false, byNameUsage)
	return cmd
}
