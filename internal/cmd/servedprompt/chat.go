package servedprompt

import (
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// NewChat reads records only, never the Chat object or its conversation.
func NewChat(f *cmdutil.Factory) *cobra.Command {
	var limit, offset, provenanceLimit, provenanceOffset int
	cmd := &cobra.Command{
		Use:   "served-prompts <chat-id>",
		Short: "Read a chatbot chat's recorded prompt history",
		Long: `Read one page of the prompt records served to a chatbot chat, newest first
in server order (served time, then ordinal). Pass the chatbot chat-id supplied
by the application, not a team-chat Channel id or a message-parent node URN.
This command is independent of chat config, the worktree binding and --app.
Sign in as whoever manages the App named by the chat's first record: only that
App's records are returned. For a chat recorded under no App, only a platform
admin outside impersonation can read the records, under the current server rule.
The chat's end user has no read right merely by being its user.

This reads records only, never the conversation, source text, current pointers
or live edges, and changes nothing. Recorded variables may contain sensitive
text. Text prints each record's revision, hash, pointer, recorded variables and
one provenance page. --json returns {servedPrompts: {items, total, limit, offset}}.
Unknown or unreadable chats, no records and serving off all return an empty
page (items: []), exit 0: empty does not distinguish these cases. Actual provider
errors retain their mapped exit codes; no typed refusal is invented for an
empty page. This command does not create records.

--limit (default 50, 0..200) and --offset page RECORDS; 0 reads their count only.
Use the returned total to choose another offset. Independent --provenance-limit
(default 50, 0..200) and --provenance-offset page each record's source ids,
revision numbers, hashes and pruning flags, never source bodies. servedRevSeq
is the compiled node's served revision; provenanceRevSeq is the compile behind
it. Pruned revisions remain recorded. Separate page requests do not promise
one database snapshot. Requires a server containing hadron-server#2865
(6b3f1e15); an older provider refuses the query as schema skew.`,
		Example: `  # Replace the id with the chatbot chat's id from the application.
  hadron chat served-prompts 01234567-89ab-4cde-8f01-23456789abcd --json
  hadron chat served-prompts 01234567-89ab-4cde-8f01-23456789abcd --limit 0
  hadron chat served-prompts 01234567-89ab-4cde-8f01-23456789abcd --limit 20 --offset 20`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return exitcode.Newf(exitcode.Usage, "pass an explicit chatbot chat id, not a team-chat Channel or node URN")
			}
			if limit < 0 || limit > 200 || provenanceLimit < 0 || provenanceLimit > 200 || offset < 0 || offset > math.MaxInt32 || provenanceOffset < 0 || provenanceOffset > math.MaxInt32 {
				return exitcode.Newf(exitcode.Usage, "limits must be 0..200 and offsets 0..2147483647")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			response, err := gen.ChatServedPrompts(cmd.Context(), client, id, limit, offset, provenanceLimit, provenanceOffset)
			if err != nil {
				return api.MapError(err)
			}
			if response == nil || response.ChatServedPrompts == nil {
				return exitcode.Newf(exitcode.Error, "server returned no chat served-prompt history page")
			}
			p := response.ChatServedPrompts
			if p.Items == nil || p.Total < 0 || len(p.Items) > limit || len(p.Items) > max(0, p.Total-offset) {
				return exitcode.Newf(exitcode.Error, "server returned an invalid served-prompt history page")
			}
			result := &historyDTO{Items: []recordDTO{}, Total: p.Total, Limit: limit, Offset: offset}
			for _, item := range p.Items {
				if item == nil {
					return exitcode.Newf(exitcode.Error, "server returned a null served-prompt record")
				}
				record, err := recordFrom(&item.ServedPromptRecord, provenanceLimit, provenanceOffset)
				if err != nil {
					return err
				}
				result.Items = append(result.Items, *record)
			}
			return output.Write(f.IOStreams, f.JSON, historyEnvelopeDTO{ServedPrompts: result}, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "served prompts: %d of %d (offset %d)\n", len(result.Items), result.Total, result.Offset); err != nil {
					return err
				}
				if len(result.Items) == 0 {
					_, err := fmt.Fprintln(w, "No records on this page (empty may also mean unknown, unreadable or serving off).")
					return err
				}
				for i := range result.Items {
					if err := writeRecord(w, &result.Items[i]); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "records per page (0 counts only, maximum 200)")
	cmd.Flags().IntVar(&offset, "offset", 0, "records to skip")
	cmd.Flags().IntVar(&provenanceLimit, "provenance-limit", 50, "provenance entries per record (0 counts only, maximum 200)")
	cmd.Flags().IntVar(&provenanceOffset, "provenance-offset", 0, "provenance entries to skip in each record")
	return cmd
}
