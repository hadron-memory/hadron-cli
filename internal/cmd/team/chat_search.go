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
		Long: `Find messages in the team chat's history whose text contains <query>, newest
first. The match is a literal, case-insensitive piece of the message text, not
a pattern. Results are references only, the seq and the message URN, with no
text: read a hit with ` + "`" + `team chat read --since <seq-1> --limit 1` + "`" + `.

A search stops at a full page or at the server's scan limit, whichever comes
first. The output says "complete" only when the search reached the start of the
chat's history. Otherwise it prints a cursor: pass it as --cursor, with the
SAME query, to continue. An empty page that is not complete does not mean "no
matches"; the scan limit was reached before any match, so continue. Only "no
matches" together with "complete" means the chat has none.

The server sets the limits (longest query, default and largest --limit, how
far one call scans); this command passes --limit and --cursor through
unchanged. Refusals keep the server's name in the --json error as serverCode:
TEAM_CHAT_SEARCH_QUERY_LENGTH (query too long) and
TEAM_CHAT_SEARCH_CURSOR_INVALID (cursor not valid for this query) exit 2;
TEAM_CHAT_SEARCH_ENCRYPTED exits 5, because a chat in an encrypted memory is
refused, never shown as an empty result.

Searching does not change your read position or mark anything read. The team
is chosen as in ` + "`" + `hadron team chat --help` + "`" + `. To search another channel, use
` + "`" + `hadron channel search` + "`" + `.`,
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
			if cmd.Flags().Changed("cursor") {
				// Even an explicit empty one: the server validates it, the CLI
				// does not quietly turn it into a first-page search.
				cursorPtr = &cursor
			}
			resp, err := gen.TeamChatSearch(ctx, client, appRef, args[0], limitPtr, cursorPtr)
			if err != nil {
				return chatsearch.MapError(err)
			}
			page := chatsearch.FromGen(resp.TeamChatSearch.ChatSearchPageFields)
			return chatsearch.Render(f, page, func(seq int) string {
				// A search run with --app or --server read THAT team on THAT
				// server; the hint must too, or it opens the ambient one.
				return fmt.Sprintf("hadron team chat read%s --since %d --limit 1", chatsearch.ContextFlags(f, true), seq-1)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum matches (the server defaults it and clamps it to its page maximum)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "continue a search from the cursor of an incomplete page (same query)")
	return cmd
}
