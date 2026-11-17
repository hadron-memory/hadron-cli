package app

import (
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type installMemoryDTO struct {
	Memory *installMemoryRowDTO `json:"memory"`
}
type installMemoryRowDTO struct {
	ID   string `json:"id"`
	URN  string `json:"urn"`
	Name string `json:"name"`
}

func newCmdAgentCustomize(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use: "customize <app> <agent>", Short: "Find an App's customisation memory for an installed Agent",
		Long: `Read the App's install memory: the separate memory where this App keeps
its customisation of an Agent. Both references are ids or fully-qualified
URNs. This command only reads; it creates no memory and writes no node.

JSON is {memory:{id,urn,name}} or {memory:null}. Null means not available:
no customisation exists yet, the target is missing, or it is not shown to
you. It is not proof that nothing exists. To create a customisation, use
node add --install <app>/<agent> --loc <loc> --name <name>; only an App
manager may write, while the App is live and the Agent installed.`,
		Example: "  hadron app agent customize hrn:app:acme.com:eng-team hrn:agent:acme.com:iris --json",
		Args:    cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := cmdutil.InstallRef(args[0], "app")
			if err != nil {
				return err
			}
			agentRef, err := cmdutil.InstallRef(args[1], "agent")
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.InstallMemory(cmd.Context(), client, appRef, agentRef)
			if err != nil {
				return api.MapError(err)
			}
			dto := installMemoryDTO{}
			if r.InstallMemory != nil {
				m := r.InstallMemory
				dto.Memory = &installMemoryRowDTO{m.Id, m.Urn, m.Name}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.Memory == nil {
					_, err := fmt.Fprintln(w, "install memory not available (not customised yet, missing or not shown to you); this read created nothing")
					return err
				}
				_, err := fmt.Fprintf(w, "%s — %s\n", output.SafeCell(dto.Memory.URN), output.SafeCell(dto.Memory.Name))
				return err
			})
		},
	}
}
