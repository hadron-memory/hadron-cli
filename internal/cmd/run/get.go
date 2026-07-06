package run

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show a run in full (status, budgets, policy, failure)",
		Long: `Show one run in full — status, trigger, budgets, policy, and the failure
payload when present. This is "what ran last night, why, and what it cost".`,
		Example: `  hadron run get run_123 --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AppRun(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.AppRun == nil {
				return exitcode.Newf(exitcode.NotFound, "run %q not found", args[0])
			}
			dto := dtoFromFields(resp.AppRun.AppRunFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeRunDetail(w, dto)
			})
		},
	}
}
