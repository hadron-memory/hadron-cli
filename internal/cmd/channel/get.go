package channel

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
	cmd := &cobra.Command{
		Use:   "get <id|address>",
		Short: "Show a Channel",
		Long: `Show a Channel by its id or its address.

Both forms are resolved BY THE SERVER — this command does not inspect the ref,
so an address in any spelling the node resolver accepts works here too.

Best-effort chat-root node URL: one authorized node read copies the server's
portal URL verbatim. Missing, concealed or failed pointer reads yield null in
JSON and no URL line in text. This is not a verified Team chat tab destination.
The authorized root read records a node read and may decrypt internally even
though this selection requests only identity and URL. No transcript is fetched,
and channel list is unchanged.`,
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
			d := channelGetDTO{channelDTO: dtoFrom(resp.Channel.ChannelFields, resp.Channel.ChannelMemory)}
			// The pointer is optional enrichment. Channel facts remain usable
			// when its separately authorized node read is unavailable.
			pointer, pointerErr := gen.NodePortal(cmd.Context(), client, d.ChatRootNodeID)
			if pointerErr == nil && pointer != nil && pointer.Node != nil {
				d.PortalURL = pointer.Node.PortalUrl
			}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				return writeChannelText(w, d.channelDTO, d.PortalURL)
			})
		},
	}
	return cmd
}

// Get-only enrichment: never add the field to list or mutation projections.
type channelGetDTO struct {
	channelDTO
	PortalURL *string `json:"portalUrl"`
}
