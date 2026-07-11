package grant

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdRevoke(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "revoke <id>",
		Aliases: []string{"rm"},
		Short:   "Revoke an action grant (org ADMIN)",
		Long: `Revoke an action grant (org ADMIN, interactive-only). Takes effect at the
next gate check; the grantee keeps their role and membership.`,
		Example: `  hadron grant revoke pg_123 --yes`,
		Args:    cobra.ExactArgs(1),
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
			_ = resp.RevokePrincipalGrant // true on success; server errors map above
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
