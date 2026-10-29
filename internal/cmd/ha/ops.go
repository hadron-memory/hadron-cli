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
		Long: `List what an AI agent may do on one instance: each "op" is one action, with
its description. Use the RUN-TOOL-NAME value, of the form ha__<slug>__<op>, to
name that action in a node's data.tools.

<id> is the instance ID from "hadron ha list". The list is every op the server
knows, narrowed to the instance's allowlist. Listing an op does not by itself
let a run use it: the policy chain must also allow tool.<run-tool-name>. No
request is sent to the Home Assistant host. A disabled instance is refused by
the server (HA_INSTANCE_DISABLED) and that message is shown as it came.`,
		Example: `  hadron ha ops <instance-id>
  hadron ha ops <instance-id> --json`,
		Args: cobra.ExactArgs(1),
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
					table.Row(output.SafeCell(o.Op), output.SafeCell(o.RunToolName), output.SafeCell(o.Description))
				}
				return table.Flush()
			})
		},
	}
}
