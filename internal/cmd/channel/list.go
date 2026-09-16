package channel

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type channelListItem = gen.ChannelsChannelsChannelsPageItemsChannel

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	var (
		appRef    string
		memoryRef string
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Channels you can read",
		Long: `List Channels.

Narrow by --owner-app or --memory. The ADDRESS column is what "channel get",
"channel update" and "channel rm" accept back — copy it rather than composing
one. Where a Channel's host memory predates the flat URN grammar the server
advertises no address; use its ID, which always works.

Two Channels can share a name AND a loc and differ only in their host memory,
so the MEMORY column is what tells them apart.`,
		Example: `  hadron channel list
  hadron channel list --memory hrn:mem:acme.com:team-shared`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Local flag handling before the client, which needs credentials.
			var filter *gen.ChannelFilter
			if appRef != "" || memoryRef != "" {
				filter = &gen.ChannelFilter{}
				if appRef != "" {
					filter.AppRef = &appRef
				}
				if memoryRef != "" {
					filter.MemoryRef = &memoryRef
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Paged to exhaustion — the contract is "every Channel you can read".
			items, err := api.CollectAll(func(limit, offset int) ([]*channelListItem, int, error) {
				off := offset
				resp, err := gen.Channels(cmd.Context(), client, filter, &limit, &off, nil)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.Channels == nil {
					return nil, 0, nil
				}
				return resp.Channels.Items, resp.Channels.Total, nil
			})
			if err != nil {
				return err
			}
			channels := make([]channelDTO, 0, len(items))
			missingAddress := 0
			for _, c := range items {
				if c == nil {
					continue
				}
				d := dtoFrom(c.ChannelFields, c.ChannelMemory)
				if d.ChatRootURN == nil || *d.ChatRootURN == "" {
					missingAddress++
				}
				channels = append(channels, d)
			}
			return output.Write(f.IOStreams, f.JSON, channels, func(w io.Writer) error {
				t := output.NewTable(w, "NAME", "MEMORY", "ADDRESS", "ID")
				for _, c := range channels {
					address := "—"
					if c.ChatRootURN != nil && *c.ChatRootURN != "" {
						address = *c.ChatRootURN
					}
					t.Row(c.Name, c.MemoryURN, address, c.ID)
				}
				if err := t.Flush(); err != nil {
					return err
				}
				if note := noAddressNote(missingAddress); note != "" {
					if _, err := io.WriteString(w, note+"\n"); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	// NOT --app: that is a persistent flag meaning the App CONTEXT, and a local
	// flag of the same name would shadow it for this command only.
	cmd.Flags().StringVar(&appRef, "owner-app", "", "only Channels of this App (ID or URN)")
	cmd.Flags().StringVarP(&memoryRef, "memory", "m", "", "only Channels hosted in this memory (ID or URN)")
	return cmd
}
