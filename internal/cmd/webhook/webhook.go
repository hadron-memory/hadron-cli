package webhook

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdWebhook builds the `webhook` command group.
func NewCmdWebhook(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhook <command>",
		Short: "Manage inbound webhook triggers for headless runs",
		Long: `Manage inbound webhook triggers (spec-040, D-2026-05-02).

A POST to the webhook's URL fires an entry node under an App's identity. The URL
path and platform token are shown ONCE at create and rotate — store them then;
the secret is never queryable again.`,
	}
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdRotate(f))
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdRm(f))
	return cmd
}
