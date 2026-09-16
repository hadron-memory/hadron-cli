package channel

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// messageDTO is the stable --json shape of one Channel message.
//
// AuthorAppID is projected and never omitted: spec 049 item J MARKS a post that
// crossed Apps, and a transcript that drops the mark misrepresents who was
// speaking where.
type messageDTO struct {
	Seq            int      `json:"seq"`
	At             string   `json:"at"`
	Body           string   `json:"body"`
	AuthorName     *string  `json:"authorName"`
	AuthorWorkerID *string  `json:"authorWorkerId"`
	AuthorUserID   *string  `json:"authorUserId"`
	AuthorAppID    *string  `json:"authorAppId"`
	SessionID      *string  `json:"sessionId"`
	ReplyToSeq     *int     `json:"replyToSeq"`
	Mentions       []string `json:"mentions"`
	NodeID         string   `json:"nodeId"`
}

// readResultDTO carries the watermark alongside the messages.
//
// NextSince is what a caller passes as --since next time. Emitting it removes
// the one arithmetic every polling consumer would otherwise reinvent, and gets
// it wrong at the boundary: sinceSeq is STRICTLY GREATER, so the watermark is
// the last seq seen, not one past it.
type readResultDTO struct {
	Messages  []messageDTO `json:"messages"`
	Total     int          `json:"total"`
	NextSince *int         `json:"nextSince"`
}

func newCmdRead(f *cmdutil.Factory) *cobra.Command {
	var (
		since    int
		before   int
		limit    int
		offset   int
		mentions string
	)
	cmd := &cobra.Command{
		Use:   "read <id|address>",
		Short: "Read a Channel's messages",
		Long: `Read a Channel's messages, oldest first.

--since is a WATERMARK and is strictly greater: pass the last seq you saw, not
one past it. The output reports the next watermark so you do not have to
compute it.

--before pages BACKWARD and is the stable way to walk history. --offset is a
position in a list other people are appending to, so under concurrent posting
it skips or repeats; the server IGNORES --offset when --before is given, and so
does this command's help.`,
		Example: `  hadron channel read hrn:node:acme.com:team-shared:chats:team --limit 20
  hadron channel read <id> --since 631
  hadron channel read <id> --before 100   # the page before seq 100`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("before") && cmd.Flags().Changed("offset") {
				// Refuse rather than let the server silently drop one: a
				// caller who passed both believes both applied.
				return exitcode.Newf(exitcode.Usage,
					"--before and --offset cannot be combined — the server ignores --offset when --before is given; use --before alone to page backward")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var sincePtr, beforePtr, limitPtr, offsetPtr *int
			var mentionsPtr *string
			if cmd.Flags().Changed("since") {
				sincePtr = &since
			}
			if cmd.Flags().Changed("before") {
				beforePtr = &before
			}
			if limit > 0 {
				limitPtr = &limit
			}
			if offset > 0 {
				offsetPtr = &offset
			}
			if mentions != "" {
				mentionsPtr = &mentions
			}
			resp, err := gen.ChannelMessages(cmd.Context(), client, args[0], sincePtr, beforePtr, limitPtr, offsetPtr, mentionsPtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.ChannelMessages == nil {
				return exitcode.Newf(exitcode.NotFound, "no Channel %q is readable here", args[0])
			}
			result := readResultDTO{Messages: []messageDTO{}, Total: resp.ChannelMessages.Total}
			for _, m := range resp.ChannelMessages.Items {
				if m == nil {
					continue
				}
				d := messageDTO{
					Seq: m.Seq, At: m.At, Body: m.Body,
					AuthorName: m.AuthorName, AuthorWorkerID: m.AuthorWorkerId, AuthorUserID: m.AuthorUserId,
					AuthorAppID: m.AuthorAppId, SessionID: m.SessionId,
					ReplyToSeq: m.ReplyToSeq, Mentions: m.Mentions, NodeID: m.NodeId,
				}
				if d.Mentions == nil {
					d.Mentions = []string{}
				}
				result.Messages = append(result.Messages, d)
			}
			if n := len(result.Messages); n > 0 {
				// The LAST seq seen — sinceSeq is strictly greater, so this is
				// the watermark, not one past it.
				last := result.Messages[n-1].Seq
				result.NextSince = &last
			}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				for _, m := range result.Messages {
					author := "(unknown)"
					if m.AuthorName != nil && *m.AuthorName != "" {
						author = *m.AuthorName
					}
					crossApp := ""
					if m.AuthorAppID != nil && *m.AuthorAppID != "" {
						// Item J: a cross-App post is marked. Dropping the
						// mark misrepresents who was speaking where.
						crossApp = " (cross-App)"
					}
					reply := ""
					if m.ReplyToSeq != nil {
						reply = fmt.Sprintf(" ↩%d", *m.ReplyToSeq)
					}
					if _, err := fmt.Fprintf(w, "#%d [%s]%s %s%s\n%s\n\n", m.Seq, author, crossApp, m.At, reply, m.Body); err != nil {
						return err
					}
				}
				if result.NextSince != nil {
					_, err := fmt.Fprintf(w, "%d message(s); next: --since %d\n", len(result.Messages), *result.NextSince)
					return err
				}
				_, err := io.WriteString(w, "no messages\n")
				return err
			})
		},
	}
	cmd.Flags().IntVar(&since, "since", 0, "only messages with a seq strictly greater than this watermark")
	cmd.Flags().IntVar(&before, "before", 0, "page BACKWARD: only messages with a seq strictly less than this")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum messages (0 = server default)")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset (ignored by the server when --before is given)")
	cmd.Flags().StringVar(&mentions, "mentions", "", "only messages mentioning this worker or user (name, handle or id)")
	return cmd
}
