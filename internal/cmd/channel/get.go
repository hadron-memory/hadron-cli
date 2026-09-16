package channel

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id|address>",
		Short: "Show a Channel",
		Long: `Show a Channel by its id or its address.

Both forms are resolved BY THE SERVER — this command does not inspect the ref,
so an address in any spelling the node resolver accepts works here too.`,
		Example: `  hadron channel get hrn:node:acme.com:team-shared:chats:team
  hadron channel get 01a0a702033c7099b23cbd358cd0c269`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GetChannel(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.Channel == nil {
				// The server answers "does not exist", "names nothing" and
				// "you may not read its host memory" with the SAME null, on
				// purpose — it is never an existence oracle. Assert only what
				// is known.
				return exitcode.Newf(exitcode.NotFound, "no Channel %q is readable here", args[0])
			}
			return writeChannel(f, dtoFrom(resp.Channel.ChannelFields, resp.Channel.ChannelMemory))
		},
	}
	return cmd
}
