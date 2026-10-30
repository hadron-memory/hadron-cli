package grant

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
		Use:     "revoke <id>",
		Aliases: []string{"rm"},
		Short:   "Revoke an action grant (org ADMIN)",
		Long: `Revoke an action grant by its id from hadron grant list. Needs ADMIN or
OWNER in the grant's org, acting as yourself (an App key, an agent run or an
impersonation session is refused; see hadron grant create). It takes effect at
the grantee's next action; their role and membership stay as they are. An
unknown or already-revoked grant exits 4.

On a terminal it asks for confirmation; in a script or agent pass --yes, or it
refuses with exit 2. --yes skips only the prompt, not the server's checks.`,
		Example: `  hadron grant list --org acme.com
  hadron grant revoke 01a18cf640787571911bd9470869f557
  hadron grant revoke 01a18cf640787571911bd9470869f557 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, fmt.Sprintf("grant %s", args[0])); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RevokePrincipalGrant(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			// The server throws NOT_FOUND rather than returning false today,
			// but honor a false defensively (webhook rm precedent; PR-214
			// review) instead of reporting a revoke that didn't happen.
			if resp == nil || !resp.RevokePrincipalGrant {
				return exitcode.Newf(exitcode.NotFound, "grant %q not found", args[0])
			}
			dto := map[string]string{"id": args[0], "status": "revoked"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ revoked grant %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
