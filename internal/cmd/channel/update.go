package channel

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var (
		name        string
		description string
	)
	cmd := &cobra.Command{
		Use:   "update <id|address>",
		Short: "Rename or re-describe a Channel",
		Long: `Rename or re-describe a Channel. Requires write access to its host memory.

An omitted flag preserves the current value — nothing is cleared by accident.
A Channel's ADDRESS is not changed by a rename: the address is its memory and
loc, so renaming it leaves every existing reference working.`,
		Example: `  hadron channel update hrn:node:acme.com:team-shared:chats:standup --name daily`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Built and validated before the client: a no-op update should
			// report the local usage error, not AuthRequired.
			input := gen.UpdateChannelInput{}
			touched := false
			if cmd.Flags().Changed("name") {
				input.Name = &name
				touched = true
			}
			if cmd.Flags().Changed("description") {
				input.Description = &description
				touched = true
			}
			if !touched {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass --name or --description")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UpdateChannel(cmd.Context(), client, args[0], &input)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.UpdateChannel == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server returned no Channel for %q", args[0])
			}
			return writeChannel(f, dtoFrom(resp.UpdateChannel.ChannelFields, resp.UpdateChannel.ChannelMemory))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "rename the Channel")
	cmd.Flags().StringVar(&description, "description", "", "what this Channel is for")
	return cmd
}
