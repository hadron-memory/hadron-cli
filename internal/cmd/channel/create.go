package channel

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var (
		memoryRef   string
		loc         string
		description string
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a Channel in a memory",
		Long: `Create a Channel hosted in a memory at a reserved address.

--memory and --loc together ARE the Channel's address: the server composes them
into the chat root's node URN, which is what every other command accepts back.
The output prints it — copy that rather than composing one yourself.

A name is not unique: two Channels can share a name and a loc and differ only
in their host memory.`,
		Example: `  hadron channel create standup -m hrn:mem:acme.com:team-shared --loc chats:standup`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Local validation precedes the client, which needs credentials:
			// otherwise a missing flag reports AuthRequired.
			if memoryRef == "" {
				return exitcode.Newf(exitcode.Usage,
					"a Channel is hosted in a memory — pass -m/--memory <ref>")
			}
			if loc == "" {
				return exitcode.Newf(exitcode.Usage,
					"a Channel needs its address within the memory — pass --loc (e.g. chats:standup)")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			input := gen.CreateChannelInput{Name: args[0], MemoryRef: memoryRef, Loc: loc}
			if description != "" {
				input.Description = &description
			}
			resp, err := gen.CreateChannel(cmd.Context(), client, &input)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.CreateChannel == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server returned no Channel for %q", args[0])
			}
			return writeChannel(f, dtoFrom(resp.CreateChannel.ChannelFields, resp.CreateChannel.ChannelMemory))
		},
	}
	cmd.Flags().StringVarP(&memoryRef, "memory", "m", "", "host memory (ID or URN)")
	cmd.Flags().StringVar(&loc, "loc", "", "the Channel's address within the memory (e.g. chats:standup)")
	cmd.Flags().StringVar(&description, "description", "", "what this Channel is for")
	return cmd
}
