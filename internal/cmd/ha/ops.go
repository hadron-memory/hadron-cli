package ha

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdOps(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "ops <id>",
		Short: "List the ops a registered Home Assistant instance exposes",
		Long: `List the ops one instance exposes: the server's static op catalog filtered by
the row's allowlist. The runToolName (ha__<slug>__<op>) is what you declare in
a node's data.tools; it is the server's string, never composed here. No request
reaches the Home Assistant host. A disabled instance is refused by the server
(HA_INSTANCE_DISABLED) and that message is shown unchanged.`,
		Example: `  hadron ha ops 01h... --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.HomeAssistantInstanceOps(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			// Null = absent or not visible, distinct from a real empty op set.
			if resp == nil || resp.HomeAssistantInstanceOps == nil {
				return exitcode.Newf(exitcode.NotFound, "Home Assistant instance %q not found or not visible to you", args[0])
			}
			ops := make([]opDTO, 0, len(resp.HomeAssistantInstanceOps))
			for _, o := range resp.HomeAssistantInstanceOps {
				if o == nil {
					continue
				}
				ops = append(ops, opDTO{Op: o.Op, RunToolName: o.RunToolName, Description: o.Description, InputSchema: o.InputSchema})
			}
			return output.Write(f.IOStreams, f.JSON, ops, func(w io.Writer) error {
				table := output.NewTable(w, "OP", "RUN-TOOL-NAME", "DESCRIPTION")
				for _, o := range ops {
					table.Row(o.Op, o.RunToolName, o.Description)
				}
				return table.Flush()
			})
		},
	}
}
