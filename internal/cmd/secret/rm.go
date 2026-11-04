package secret

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
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <id|urn>",
		Aliases: []string{"delete"},
		Short:   "Delete a secret",
		Long: `Delete a secret permanently; the value cannot be recovered. Name it by the
id from hadron secret list, or by URN: hrn:secret:<org or user handle>:<name>.
You can delete your own user secrets, and an org's if you are its ADMIN or
OWNER. An unknown or already-deleted secret exits 4.

Nothing checks whether a secret is still in use. Runs that need it fail from
then on, or pick up a secret of the same name from the org instead of the user.

On a terminal it asks for confirmation; in a script or agent pass --yes, or it
refuses with exit 2.`,
		Example: `  hadron secret list --scope user
  hadron secret rm hrn:secret:alice:github-token
  hadron secret rm 01a18cf643247d0fa261f320ec74d878 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "secret "+ref); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.DeleteSecret(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || !resp.DeleteSecret {
				return exitcode.Newf(exitcode.NotFound, "secret %q not found", ref)
			}
			dto := map[string]string{"id": ref, "status": "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ deleted secret %s\n", ref)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
