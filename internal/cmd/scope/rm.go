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
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <name|id>",
		Aliases: []string{"delete"},
		Short:   "Delete a scope",
		Long: `Delete a scope. Its name is free for reuse immediately.

Deleting a scope removes the lens, never the memories it listed.`,
		Example: `  hadron scope rm research --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			appRef, err := f.App()
			if err != nil {
				return err
			}
			// Resolve BEFORE prompting, so the confirmation names a scope that
			// actually exists rather than asking the user to approve deleting
			// something we then fail to find.
			id, err := resolveScopeID(cmd, client, args[0], appRef)
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
	return cmd
}
