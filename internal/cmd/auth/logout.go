package auth

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLogout(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored credential for the current server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			server, err := f.Server()
			if err != nil {
				return err
			}
			// An active impersonation session must not survive logout: it is
			// filed under its own key, so purging only the credential key
			// would leave Factory.Token() still resolving it — "logged out"
			// while still authenticated as the target (PR #345 review).
			impRemoved, err := store.Purge(auth.ImpersonationHostKey(server))
			if err != nil {
				return err
			}
			// Delete from EVERY backend, not just the one Resolve() picks now:
			// a token written to the plaintext file on a headless box would
			// otherwise survive a logout that resolves to the keychain, and
			// vice-versa (#116).
			removed, err := store.Purge(auth.Host(server))
			if err != nil {
				return err
			}
			removed = removed || impRemoved
			if !removed {
				dto := map[string]string{"server": server, "status": "no_stored_credential"}
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					_, err := fmt.Fprintf(w, "no stored credential for %s\n", server)
					return err
				})
			}
			dto := map[string]string{"server": server, "status": "logged_out"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Logged out of %s\n", server)
				return err
			})
		},
	}
}
