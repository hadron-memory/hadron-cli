package run

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

func newCmdCancel(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a live run (the kill switch)",
		Long: `Cancel a live run — transitions it to CANCELLED. Prompts on a
TTY; a non-interactive caller must pass --yes.

Cancelling is idempotent: a run that had already ended is left as it is, the
command says "run <id> is already <STATUS>; nothing to cancel", and the exit
code stays 0 (the run is not live, which is what a caller asked for).`,
		Example: `  hadron run cancel run_123 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Cancel run %s?", args[0])); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CancelAppRun(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.CancelAppRun == nil {
				return exitcode.Newf(exitcode.Error, "server returned no run")
			}
			dto := dtoFromFields(resp.CancelAppRun.AppRunFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				// The server's cancel is idempotent: a run that had already ended
				// comes back unchanged. Claim a cancellation only when the run
				// is CANCELLED; otherwise name the state it is really in (#3080).
				if dto.Status != "CANCELLED" {
					_, err := fmt.Fprintf(w, "run %s is already %s; nothing to cancel\n", output.SafeCell(dto.ID), output.SafeCell(dto.Status))
					return err
				}
				_, err := fmt.Fprintf(w, "✓ cancelled run %s (%s)\n", output.SafeCell(dto.ID), output.SafeCell(dto.Status))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
