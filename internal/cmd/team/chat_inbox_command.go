package team

import (
	"os"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

// Deliberately unregistered until the generated R57 adapter and bound identity
// reader can be integrated against landed contracts.
func newCmdTeamChatInboxWithTransport(f *cmdutil.Factory, source inboxTransport, identity inboxIdentityReader) *cobra.Command {
	var ackFile string
	cmd := &cobra.Command{
		Use: "inbox", Short: "Display your worker's delivery batches and acknowledge complete output",
		Long: `Display delivery batches for this worktree's own live worker session.
Acknowledgment follows complete output; this does not mark chat read.
If acknowledgment is unconfirmed, retain the output. Retry only its proof list
with --ack-file PATH (JSON: {"batchIds":["opaque-proof"]}); use - for piped stdin.
Recovery acknowledges without reading or redisplaying batches. Proofs must
belong to the same live session. This command creates no recovery file.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var ids []string
			if cmd.Flags().Changed("ack-file") {
				if ackFile == "" {
					return exitcode.Newf(exitcode.Usage, "--ack-file requires a path or -")
				}
				reader := f.IOStreams.In
				if ackFile == "-" {
					if f.IOStreams.IsInputTerminal() {
						return exitcode.Newf(exitcode.Usage, "--ack-file - requires piped input")
					}
				} else {
					file, err := os.Open(ackFile)
					if err != nil {
						return exitcode.Newf(exitcode.Usage, "cannot open acknowledgment input")
					}
					defer func() { _ = file.Close() }()
					reader = file
				}
				var err error
				ids, err = decodeInboxAckFile(reader)
				if err != nil {
					return err
				}
			}
			return runInbox(cmd.Context(), source, identity, f.IOStreams, f.JSON, ids)
		},
	}
	cmd.Flags().StringVar(&ackFile, "ack-file", "", "Acknowledge batchIds from JSON without redisplaying (- for piped stdin)")
	return cmd
}
