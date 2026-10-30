package org

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
		Use:     "rm <org>",
		Aliases: []string{"delete"},
		Short:   "Delete an organization (platform admin only)",
		Long: `Delete an organization. Only a platform admin can do this; the org's own
OWNER is refused (exit 8). There is no undo.

What happens: the org stops resolving (another rm of it exits 4), its pending
invitations are revoked, and its org secrets are deleted for good. Its
memberships, memories, Apps, agents and grants are left as they are, and its
root stays reserved. <org> is its id, URN or root; check it with hadron org get
first.

On a terminal it asks for confirmation; in a script or agent pass --yes, or it
refuses with exit 2.`,
		Example: `  hadron org get acme.com
  hadron org rm acme.com
  hadron org rm hrn:org:acme.com --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "organization "+args[0]); err != nil {
				return err
			}
			if _, err := gen.DeleteOrganization(cmd.Context(), client, args[0]); err != nil {
				return api.MapError(err)
			}
			dto := map[string]string{"id": args[0], "status": "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ deleted organization %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
