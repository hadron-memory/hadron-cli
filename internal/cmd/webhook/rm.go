package webhook

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
		Short:   "Delete a webhook",
		Example: `  hadron webhook rm wh_123 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, fmt.Sprintf("webhook %s", args[0])); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.DeleteAgentWebhook(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if !resp.DeleteAgentWebhook {
				return exitcode.Newf(exitcode.NotFound, "webhook %q not found", args[0])
			}
			dto := map[string]string{"id": args[0], "status": "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ deleted webhook %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
