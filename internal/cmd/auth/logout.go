package auth

import (
	"errors"
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
			err = f.TokenStore().Delete(auth.Host(server))
			if errors.Is(err, store.ErrNotFound) {
				dto := map[string]string{"server": server, "status": "no_stored_credential"}
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					_, err := fmt.Fprintf(w, "no stored credential for %s\n", server)
					return err
				})
			}
			if err != nil {
				return err
			}
			dto := map[string]string{"server": server, "status": "logged_out"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Logged out of %s\n", server)
				return err
			})
		},
	}
}
