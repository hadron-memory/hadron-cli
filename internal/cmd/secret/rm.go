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
		Use:     "rm <id>",
		Aliases: []string{"delete"},
		Short:   "Delete a secret",
		Long: `Hard-delete a secret by id. Secret URNs will become accepted after the
server ships resolveSecretRef; until then pass the cuid id.`,
		Example: `  hadron secret rm sec_123 --yes`,
		Args:    cobra.ExactArgs(1),
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
