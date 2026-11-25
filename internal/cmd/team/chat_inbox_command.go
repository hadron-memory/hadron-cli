package team

import (
	"os"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

// Transport seam used by the production self adapter and lifecycle tests.
func newCmdTeamChatInboxWithTransport(f *cmdutil.Factory, source inboxTransport, identity inboxIdentityReader) *cobra.Command {
	var ackFile string
	cmd := &cobra.Command{
		Use: "inbox", Short: "Display your worker's delivery batches and acknowledge complete output",
		Long: `Show the delivery batches queued for this worktree's own live worker session,
one page at a time. A batch is a group of messages the system has set aside for
your worker to see. After the page has been printed in full, the command
acknowledges those batches, which stops them being shown again. That records
delivery only; it does not move your chat read position (use
` + "`" + `team chat mark-read` + "`" + ` or ` + "`" + `team chat read` + "`" + ` for that).

Paging: pass the page's pageInfo.endCursor as --after to get the next page.

If the run fails before the acknowledgement, the same batch may show again the
next time. If the acknowledgement is unconfirmed in text mode, simply run the
command again; unacknowledged batches may show again.

Recovering an acknowledgement without reading the batches again: run with
--json and keep that output privately, because each batch has an id that is
the proof of delivery. If the acknowledgement was lost, write those ids to a
file as {"batchIds":["<id>", ...]} (the ids from batches[].id) and run again
with --ack-file PATH (- for piped standard input). That acknowledges them
without printing any batch. The ids must belong to the same live session. This
command does not create the file for you.`,
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
