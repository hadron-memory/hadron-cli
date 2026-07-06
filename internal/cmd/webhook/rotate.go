package webhook

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdRotate(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rotate <id>",
		Short: "Rotate a webhook's secret (prints the shown-once replacement)",
		Long: `Rotate a webhook's secret. The old URL path and token stop working immediately;
the new ones are printed ONCE — store them now. Prompts on a TTY; a
non-interactive caller must pass --yes.`,
		Example: `  hadron webhook rotate wh_123 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.Confirm(f.IOStreams, yes, "Rotate this webhook's secret? The current URL stops working immediately."); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RotateAgentWebhook(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.RotateAgentWebhook == nil || resp.RotateAgentWebhook.Webhook == nil {
				return exitcode.Newf(exitcode.Error, "server returned incomplete webhook credentials")
			}
			dto := credsFromFields(resp.RotateAgentWebhook.AgentWebhookCredentialFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeCredentials(w, "rotated", dto)
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
