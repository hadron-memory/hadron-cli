package app

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdUninstall(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "uninstall <app-ref>",
		Aliases: []string{"rm"},
		Short:   "Uninstall an App",
		Example: `  hadron app uninstall hrn:app:acme.com:support --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := cmdutil.CanonicalAppRef("<app-ref>", args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "App "+appRef); err != nil {
				return err
			}
			if _, err := gen.DeleteApp(cmd.Context(), client, appRef); err != nil {
				return api.MapError(err)
			}
			dto := map[string]string{"app": appRef, "status": "uninstalled"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Uninstalled App %s\n", appRef)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
