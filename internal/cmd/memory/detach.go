package memory

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

// detachDTO is the stable --json shape: the refs as sent and the server's answer.
type detachDTO struct {
	Memory   string `json:"memory"`
	App      string `json:"app"`
	Detached bool   `json:"detached"`
}

func newCmdDetach(f *cmdutil.Factory) *cobra.Command {
	var app string
	var yes bool
	cmd := &cobra.Command{
		Use:   "detach <memoryRef> --app <app-id-or-urn>",
		Short: "Detach a memory from an App",
		Long: `Detach a memory from an App: remove the attachment that memory attach made.
The memory itself is unchanged: it keeps its name, URN, class, owner and
content, and any attachment to another App stays. Detaching an attachment
that is not there succeeds and changes nothing.

The server decides who may detach: whoever attached it (even after they can
no longer read the memory), the memory's owner, and whoever manages the App.
Anyone else is refused, and the CLI prints the server's code and message as it
answers; a memory you cannot read is reported as not found. Only a person can
detach, not an App key, a run or a worker session.

It asks before it detaches on a terminal, and needs --yes otherwise. To undo
it, attach it again with memory attach. Memory and App references accept an
ID or a URN, with or without the hrn: prefix.`,
		Example: `  hadron memory detach hrn:mem:acme.com:handbook --app hrn:app:acme.com:coach
  hadron memory detach hrn:mem:acme.com:handbook --app acme.com:coach --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if app == "" {
				return exitcode.Newf(exitcode.Usage, "memory detach requires --app")
			}
			appRef, err := cmdutil.CanonicalAppRef("--app", app)
			if err != nil {
				return err
			}
			memoryRef := cmdutil.CanonicalMemoryRef(args[0])
			if err := cmdutil.Confirm(f.IOStreams, yes,
				"Detach "+output.SafeCell(memoryRef)+" from "+output.SafeCell(appRef)+"?"); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// One mutation, no client-side authority check (cor:api:240:01): the
			// server answers with the refusal table's code and message.
			resp, err := gen.DetachMemoryFromApp(cmd.Context(), client, memoryRef, appRef)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || !resp.DetachMemoryFromApp {
				return exitcode.Newf(exitcode.Error, "the server did not confirm the detach; nothing is claimed")
			}
			dto := detachDTO{Memory: memoryRef, App: appRef, Detached: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ detached %s from %s\n", output.SafeCell(memoryRef), output.SafeCell(appRef))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "App ID or URN (required)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}
