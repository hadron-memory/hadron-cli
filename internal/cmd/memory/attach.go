package memory

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdAttach(f *cmdutil.Factory) *cobra.Command {
	var app string
	var agent string
	cmd := &cobra.Command{
		Use:   "attach <memory-id-or-urn> --app <app-id-or-urn> --agent <agent-id-or-urn>",
		Short: "Attach a free-standing personal/private memory to an App",
		Long: `Attach an existing free-standing personal or private memory to an App.

The memory must belong to you, the Agent must be installed in the App, and you
must be an App member. The memory keeps its URN, class, and owner. Memory, App,
and Agent references accept IDs, bare URNs, or prefixed URNs.`,
		Example: `  hadron memory attach acme.com::my-notes --app acme.com::coach --agent acme.com::agent
  hadron memory attach hrn:memory:acme.com::private-notes --app app-id --agent agent-id --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if app == "" || agent == "" {
				return exitcode.Newf(exitcode.Usage, "memory attach requires --app and --agent")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memoryRef := cmdutil.CanonicalMemoryRef(args[0])
			resp, err := gen.AttachMemoryToApp(cmd.Context(), client, memoryRef, app, agent)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.AttachMemoryToApp == nil {
				return exitcode.Newf(exitcode.Error, "server returned no memory")
			}
			m := dtoFromMemory(resp.AttachMemoryToApp)
			return output.Write(f.IOStreams, f.JSON, m, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ attached", m.URN, "to", app, "via", agent)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "App ID or URN (required)")
	cmd.Flags().StringVar(&agent, "agent", "", "Agent ID or URN installed in the App (required)")
	return cmd
}
