package team

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/chatsearch"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// newCmdTeamChatSearch searches the team chat's history (hadron-cli#1327 over
// Query.teamChatSearch, cor:agt:020:04). It is a pure read: it records no
// watermark and marks nothing read, so it never follows itself with a
// mark-read.
func newCmdTeamChatSearch(f *cmdutil.Factory) *cobra.Command {
	var limit int
	var cursor string
	cmd := &cobra.Command{
		Use:   "search <query> [--limit <n>] [--cursor <c>]",
		Short: "Search the team chat's history for a text",
		Long: `Search the team chat's history for messages containing <query>, newest
first. The match is a literal, case-insensitive substring of the message text.
Results are REFERENCES only (the seq and the message URN): there is no body or
snippet, so read a hit with ` + "`team chat read --since <seq-1> --limit 1`" + `.

A call stops at a full page or at the server's scan bound, whichever comes
first. "complete" means the search reached the start of the history. Otherwise
the footer prints the cursor: pass it as --cursor, with the SAME query, to
continue. An empty page that is not complete is not "no matches"; the scan
bound fired before any match, so continue. Only "no matches" with "complete"
means the chat holds none.

The server owns the numbers (query length, default and maximum --limit, scan
bound); this command passes --limit and --cursor through and never clamps
them. Typed refusals keep the server's code in the --json envelope
(serverCode): TEAM_CHAT_SEARCH_QUERY_LENGTH and TEAM_CHAT_SEARCH_CURSOR_INVALID
exit 2, TEAM_CHAT_SEARCH_ENCRYPTED exit 5 — a chat in an encrypted memory is
refused, never shown as an empty result.

Searching changes no read position and marks nothing read. The team App
resolves like the other team chat commands (--app, the App context, or the
worktree binding). To search another channel, use ` + "`hadron channel search`" + `.`,
		Example: `  hadron team chat search "release freeze"
  hadron team chat search deploy --limit 50 --json
  hadron team chat search deploy --cursor <cursor from the last page>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			appRef, err := resolveTeamApp(ctx, f, b)
			if err != nil {
				return err
			}
			if appRef == "" {
				return exitcode.Newf(exitcode.Usage, "no team App: pass --app, set an App context, or run inside a bound worktree")
			}
			var limitPtr *int
			if cmd.Flags().Changed("limit") {
				// Passed as typed, even 0 or negative: the server refuses
				// below 1 with BAD_USER_INPUT, and that refusal is the answer.
				limitPtr = &limit
			}
			var cursorPtr *string
			if cursor != "" {
				cursorPtr = &cursor
			}
			resp, err := gen.TeamChatSearch(ctx, client, appRef, args[0], limitPtr, cursorPtr)
			if err != nil {
				return chatsearch.MapError(err)
			}
			page := chatsearch.FromGen(resp.TeamChatSearch.ChatSearchPageFields)
			return chatsearch.Render(f, page, func(seq int) string {
				// A search run with --app read THAT team; the hint must read it
				// too, or it opens whichever team the worktree is bound to.
				app := ""
				if f.AppFlag != "" {
					app = " --app " + f.AppFlag
				}
				return fmt.Sprintf("hadron team chat read%s --since %d --limit 1", app, seq-1)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum matches (the server defaults it and clamps it to its page maximum)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "continue a search from the cursor of an incomplete page (same query)")
	return cmd
}
