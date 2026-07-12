package connection

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

func newCmdRevoke(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "revoke <grant-id>",
		Aliases: []string{"rm"},
		Short:   "Revoke a connection grant (owner-only)",
		Long: `Revoke a connection grant you created (owner-only). The App immediately loses
the delegated access.`,
		Example: `  hadron connection grant revoke cg_123 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, fmt.Sprintf("connection grant %s", args[0])); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RevokeConnectionGrant(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			// The server throws NOT_FOUND rather than returning false today, but
			// honor a false defensively instead of reporting a revoke that didn't
			// happen.
			if resp == nil || !resp.RevokeConnectionGrant {
				return exitcode.Newf(exitcode.NotFound, "connection grant %q not found", args[0])
			}
			dto := map[string]string{"id": args[0], "status": "revoked"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ revoked connection grant %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
