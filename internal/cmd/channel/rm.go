package channel

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
		Use:     "rm <id|address>",
		Aliases: []string{"delete"},
		Short:   "Delete a Channel",
		Long: `Delete a Channel by its id or its address.

This removes the chat room. Requires write access to its host memory.`,
		Example: `  hadron channel rm hrn:node:acme.com:team-shared:chats:standup --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "channel "+args[0]); err != nil {
				return err
			}
			// The mutation returns a BOOLEAN, and false means nothing was
			// deleted — an unknown ref, or one already gone. Discarding it
			// reports success with exit 0, so automation treats a failed
			// delete as a done one (@codex, #598). `schedule rm` and
			// `webhook rm` check theirs; `org rm` does not, and that is the
			// one I copied.
			resp, err := gen.DeleteChannel(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || !resp.DeleteChannel {
				return exitcode.Newf(exitcode.NotFound, "no Channel %q was deleted — it is unreadable or already gone", args[0])
			}
			dto := map[string]string{"ref": args[0], "status": "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ deleted channel %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
