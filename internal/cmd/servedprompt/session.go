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

type historyDTO struct {
	Items  []recordDTO `json:"items"`
	Total  int         `json:"total"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}
type historyEnvelopeDTO struct {
	ServedPrompts *historyDTO `json:"servedPrompts"`
}

// NewSession reads an explicitly named session, never the worktree's binding.
func NewSession(f *cmdutil.Factory) *cobra.Command {
	var limit, offset, provenanceLimit, provenanceOffset int
	cmd := &cobra.Command{
		Use:   "served-prompts <session-id>",
		Short: "Read one worker session's recorded prompt history",
		Long: `Read one page of the boot-prompt records served to a worker session, newest
first in server order (served time, then ordinal). Find a session-id with
` + "`team session list`" + `; pass its id, not a worker name or URN. Sign in first.
No binding or ambient App selection is used: the explicit id chooses the session
and the server decides access. This reads no source text, current pointers or
live edges, and changes nothing. Recorded variables may contain sensitive text.

Text prints each record's revision, hash, pointer, recorded variables and one
provenance page. --json returns {servedPrompts: {items, total, limit, offset}}.
A missing or unreadable session is unavailable ({servedPrompts: null}); an empty
page has items: [] and may mean no records, unreadable records or serving off.
Neither result proves which. Actual provider errors retain their typed exit
codes; sign in/check access if refused. This command does not create records.

--limit (default 50, 0..200) and --offset page RECORDS; 0 reads their count only.
Use the returned total to choose another offset. Each record's provenance has
independent --provenance-limit (default 50, 0..200) and --provenance-offset.
Provenance contains source node ids, revision numbers, hashes and pruning flags,
never source bodies. servedRevSeq is the compiled node's served revision number;
provenanceRevSeq is the compile behind it. Pruned revisions remain recorded.
Pages are separate requests and do not promise one database snapshot.`,
		Example: `  # Replace the id with one from team session list; JSON is one history page.
  hadron team session served-prompts 01234567-89ab-4cde-8f01-23456789abcd --json
  hadron team session served-prompts 01234567-89ab-4cde-8f01-23456789abcd --limit 0
  hadron team session served-prompts 01234567-89ab-4cde-8f01-23456789abcd --limit 20 --offset 20`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return exitcode.Newf(exitcode.Usage, "pass an explicit session id from team session list")
			}
			if limit < 0 || limit > 200 || provenanceLimit < 0 || provenanceLimit > 200 || offset < 0 || offset > math.MaxInt32 || provenanceOffset < 0 || provenanceOffset > math.MaxInt32 {
				return exitcode.Newf(exitcode.Usage, "limits must be 0..200 and offsets 0..2147483647")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			response, err := gen.SessionServedPrompts(cmd.Context(), client, id, limit, offset, provenanceLimit, provenanceOffset)
			if err != nil {
				return api.MapError(err)
			}
			var result *historyDTO
			if response == nil {
				return exitcode.Newf(exitcode.Error, "server returned no session history response")
			}
			if response.Session != nil {
				p := response.Session.ServedPrompts
				if p == nil || p.Items == nil || p.Total < 0 || len(p.Items) > limit || len(p.Items) > max(0, p.Total-offset) {
					return exitcode.Newf(exitcode.Error, "server returned an invalid served-prompt history page")
				}
				result = &historyDTO{Items: []recordDTO{}, Total: p.Total, Limit: limit, Offset: offset}
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
			}
			return output.Write(f.IOStreams, f.JSON, historyEnvelopeDTO{ServedPrompts: result}, func(w io.Writer) error {
				if result == nil {
					_, err := fmt.Fprintln(w, "served prompts: unavailable (session missing or unreadable)")
					return err
				}
				if _, err := fmt.Fprintf(w, "served prompts: %d of %d (offset %d)\n", len(result.Items), result.Total, result.Offset); err != nil {
					return err
				}
				if len(result.Items) == 0 {
					_, err := fmt.Fprintln(w, "No records on this page (empty may also mean unreadable or serving off).")
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
