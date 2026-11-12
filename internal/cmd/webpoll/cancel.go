package webpoll

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func itoa(n int) string { return strconv.Itoa(n) }

func newCmdCancel(f *cmdutil.Factory) *cobra.Command {
	var (
		org string
		yes bool
	)
	cmd := &cobra.Command{
		Use:   "cancel <job-id> --org <ref>",
		Short: "Stop a page-monitor watch (org ADMIN)",
		Long: `Stop one page-monitor watch of an organization (org ADMIN/OWNER). A stopped
watch is not resumed: a new run has to start another one.

Stopping a watch that was already stopped succeeds and says so. A job id from
another organization, or none at all, is refused the same way (NOT_FOUND). The
server makes every authorization decision; the CLI checks nothing ahead of it.`,
		Example: `  hadron web-poll cancel job_123 --org acme.com --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Stop watch %s? It cannot be resumed.", jobID)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CancelWebPoll(cmd.Context(), client, org, jobID)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || resp.CancelWebPoll == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no answer for the stop; check with web-poll list before retrying")
			}
			p := resp.CancelWebPoll
			dto := cancelDTO{JobID: p.JobId, Cancelled: p.Cancelled, AlreadyCancelled: p.AlreadyCancelled}
			if !dto.Cancelled && !dto.AlreadyCancelled {
				// Neither a stop nor an already-stopped watch: not a success.
				if werr := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					_, err := fmt.Fprintf(w, "watch %s was not stopped\n", output.SafeCell(dto.JobID))
					return err
				}); werr != nil {
					return werr
				}
				return exitcode.Silent(exitcode.Error)
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.AlreadyCancelled {
					_, err := fmt.Fprintf(w, "watch %s was already stopped\n", output.SafeCell(dto.JobID))
					return err
				}
				_, err := fmt.Fprintf(w, "✓ stopped watch %s\n", output.SafeCell(dto.JobID))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization the watch belongs to (ID or URN; required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	_ = cmd.MarkFlagRequired("org")
	return cmd
}
