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
		Long: `Cancel a live run — transitions it to CANCELLED (cor:agt:010:02). Prompts on a
TTY; a non-interactive caller must pass --yes.`,
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
				_, err := fmt.Fprintf(w, "✓ cancelled run %s (%s)\n", dto.ID, dto.Status)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
