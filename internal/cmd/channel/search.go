package channel

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/chatsearch"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// newCmdSearch searches one Channel's history (hadron-cli#1327 over
// Query.channelMessageSearch, the Channel-addressed twin of teamChatSearch).
func newCmdSearch(f *cmdutil.Factory) *cobra.Command {
	var limit int
	var cursor string
	cmd := &cobra.Command{
		Use:   "search <id|address> <query>",
		Short: "Search a Channel's history for a text",
		Long: `Search one Channel's history for messages containing <query>, newest first.
The match is a literal, case-insensitive substring of the message text. The
Channel is its id or its address (as for "channel read"). Results are
REFERENCES only (the seq and the message URN), with no body or snippet: read a
hit with "channel read <channel> --since <seq-1> --limit 1".

A call stops at a full page or at the server's scan bound. "complete" means the
search reached the start of the history; otherwise the footer prints the
cursor to pass as --cursor, with the SAME query. An empty page that is not
complete is not "no matches": continue. Only "no matches" with "complete" means
the Channel holds none.

The server owns the numbers (query length, default and maximum --limit, scan
bound); --limit and --cursor are passed through, never clamped. Typed refusals
keep the server's code in the --json envelope (serverCode):
TEAM_CHAT_SEARCH_QUERY_LENGTH and TEAM_CHAT_SEARCH_CURSOR_INVALID exit 2,
TEAM_CHAT_SEARCH_ENCRYPTED exit 5 (an encrypted chat is refused, never shown as
empty). A Channel you may not read, or that does not exist, is exit 4.

Searching changes no read position and marks nothing read. For your own team
chat use "hadron team chat search".`,
		Example: `  hadron channel search hrn:node:acme.com:team-shared:chats:team "release freeze"
  hadron channel search <id> deploy --limit 50 --json
  hadron channel search <id> deploy --cursor <cursor from the last page>`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var limitPtr *int
			if cmd.Flags().Changed("limit") {
				limitPtr = &limit
			}
			var cursorPtr *string
			if cmd.Flags().Changed("cursor") {
				cursorPtr = &cursor
			}
			resp, err := gen.ChannelMessageSearch(cmd.Context(), client, args[0], args[1], limitPtr, cursorPtr)
			if err != nil {
				return chatsearch.MapError(err)
			}
			page := chatsearch.FromGen(resp.ChannelMessageSearch.ChatSearchPageFields)
			return chatsearch.Render(f, page, func(seq int) string {
				return fmt.Sprintf("hadron channel read%s %s --since %d --limit 1", chatsearch.ContextFlags(f, false), cmdutil.ShellQuote(args[0]), seq-1)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum matches (the server defaults it and clamps it to its page maximum)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "continue a search from the cursor of an incomplete page (same query)")
	return cmd
}
