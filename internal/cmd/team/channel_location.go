package team

import (
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type teamChannelLocationDTO struct {
	App       string  `json:"app"`
	AppSource string  `json:"appSource"`
	ChannelID string  `json:"channelId"`
	PortalURL *string `json:"portalUrl"`
}

func newCmdTeamChannel(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use: "channel", Short: "Show the default team channel's server-built URL",
		Long: `Find the default team channel without knowing its id or address.

The team App resolves from --app, the configured App context, or the worktree
binding, just as other team commands do. Output names the App and scope source.
The server-built URL opens the channel root node's own page and channel view;
this does not select an App-level conversation or create a new portal route.

One default-channel metadata read is followed by one authorized root-node read
for identity and URL only. That node read records a read and may decrypt
internally; no transcript is fetched, no cursor is advanced, and no binding
is started or changed. No URL is composed by the client. If the server supplies
no link, or the optional root read fails or is concealed, portalUrl is null in
JSON and text says no channel URL is available. The default-channel lookup
itself must succeed.`,
		Example: "  hadron team channel\n  hadron team channel --app acme.com:team --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamChannelLocation(ctx, client, scope.Ref)
			if err != nil {
				return api.MapError(err)
			}
			if resp.App == nil {
				return exitcode.Newf(exitcode.NotFound, "no team App is readable here")
			}
			if resp.App.DefaultChannel == nil {
				return exitcode.Newf(exitcode.NotFound, "the App has no default team channel")
			}
			d := teamChannelLocationDTO{App: resp.App.Urn, AppSource: scope.Source, ChannelID: resp.App.DefaultChannel.Id}
			if ref := resp.App.DefaultChannel.ChatRootNodeId; ref != "" {
				pointer, pointerErr := gen.NodePortal(ctx, client, ref)
				if pointerErr == nil && pointer != nil && pointer.Node != nil {
					d.PortalURL = pointer.Node.PortalUrl
				}
			}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s (%s)\nchannel: %s\n", d.App, d.AppSource, d.ChannelID); err != nil {
					return err
				}
				if d.PortalURL != nil {
					_, err := fmt.Fprintf(w, "URL: %s\n", *d.PortalURL)
					return err
				}
				_, err := fmt.Fprintln(w, "no channel URL is available")
				return err
			})
		},
	}
}
