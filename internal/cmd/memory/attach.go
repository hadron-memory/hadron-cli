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
		Use:   "attach <memoryRef> --app <app-id-or-urn> [--agent <agent-id-or-urn>]",
		Short: "Attach a memory you can read to an App",
		Long: `Attach any memory you can read to an App, so the App's key and its runs can
read it (a personal or private memory only when you also own the App; see below).
Use it for notes or a knowledge memory you want an App to see, instead of copying
them into a new memory.

The memory itself is unchanged: it keeps its name, URN, class, owner and App.
It may be attached to more than one App, and an attached memory does not count
toward the one-per-class limit on memories you create in an App. Any class
except system can be attached. The attachment is read-only: the App's key and
runs can read the memory but not write it, App members gain nothing from it,
and the key never reads more than you can read yourself. A personal or private
memory reaches the App's key only when the memory's owner is the App's owner
(an App you own yourself); in an organization's App it is attached but does
nothing for the key. An attachment stops working when you stop reading the
memory, and works again if you read it again. Only a person can attach, and
only to an App you are a member of. Attaching the same memory again changes
nothing. To remove an attachment, use memory detach.

Memory and App references accept an ID or a URN, with or without the hrn:
prefix. --agent is ignored by current servers, which attach the memory to the
App as a whole; an older server still needs it.`,
		Example: `  hadron memory attach hrn:mem:acme.com:handbook --app hrn:app:acme.com:coach
  hadron memory attach hrn:mem:acme.com:team-wiki --app acme.com:coach --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if app == "" {
				return exitcode.Newf(exitcode.Usage, "memory attach requires --app")
			}
			appRef, err := cmdutil.CanonicalAppRef("--app", app)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memoryRef := cmdutil.CanonicalMemoryRef(args[0])
			// No client-side rule decides what may be attached (cor:api:240:01): the
			// server judges the class, the read and the membership.
			var agentRef *string
			if agent != "" {
				agentRef = &agent
			}
			resp, err := gen.AttachMemoryToApp(cmd.Context(), client, memoryRef, appRef, agentRef)
			if err != nil {
				// A server that predates attaching without an agent needs --agent: say so when the
				// schema-skew line is what came back and none was given.
				if agent == "" && api.IsSchemaSkew(err) {
					return api.WithCause(exitcode.Newf(exitcode.Usage, "%v; if the server predates attaching without an agent, pass --agent <agent>", api.MapError(err)), api.MapError(err))
				}
				// Keeps the server's code for the --json error's serverCode.
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || resp.AttachMemoryToApp == nil {
				return exitcode.Newf(exitcode.Error, "server returned no memory")
			}
			m := dtoFromMemory(resp.AttachMemoryToApp)
			return output.Write(f.IOStreams, f.JSON, m, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ attached", output.SafeCell(m.URN), "to", appRef)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "App ID or URN (required)")
	cmd.Flags().StringVar(&agent, "agent", "", "Agent ID or URN: ignored by current servers, needed only by an older one")
	return cmd
}
