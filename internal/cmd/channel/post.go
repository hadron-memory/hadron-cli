package channel

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdPost(f *cmdutil.Factory) *cobra.Command {
	var (
		replyTo int
		session string
		asMe    bool
	)
	cmd := &cobra.Command{
		Use:   "post <id|address> <body|->",
		Short: "Post a message into a Channel",
		Long: `Post a message into a Channel. Pass - to read the body from stdin.

AUTHORSHIP IS EXPLICIT. --session <id> posts as the Worker bound to that
session; --as-me posts as you. One of the two is required, because the server
treats the session as OPTIONAL and omitting it silently records the human as
the author with no error — the wrong authorship, indistinguishable from success.
Refusing here is the only place that can be caught.`,
		Example: `  hadron channel post <address> "shipped #594" --session 01a0a78c…
  hadron channel post <address> - --as-me < note.md`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Authorship arity, checked before anything else: this is the
			// quiet failure the team-chat tools are known for.
			if session == "" && !asMe {
				return exitcode.Newf(exitcode.Usage,
					"say who is posting: --session <id> to post as that session's Worker, or --as-me to post as yourself (the server records the human silently if neither is given)")
			}
			if session != "" && asMe {
				return exitcode.Newf(exitcode.Usage, "--session and --as-me are mutually exclusive")
			}

			body := args[1]
			if body == "-" {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("reading body from stdin: %w", err)
				}
				body = string(b)
			}
			if strings.TrimSpace(body) == "" {
				return exitcode.Newf(exitcode.Usage, "the message body is empty")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var replyPtr *int
			var sessionPtr *string
			if cmd.Flags().Changed("reply-to") {
				replyPtr = &replyTo
			}
			if session != "" {
				sessionPtr = &session
			}
			resp, err := gen.CreateChannelMessage(cmd.Context(), client, args[0], body, replyPtr, sessionPtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.CreateChannelMessage == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server returned no message")
			}
			m := resp.CreateChannelMessage
			dto := messageDTO{
				Seq: m.Seq, At: m.At, Body: m.Body,
				AuthorName: m.AuthorName, AuthorWorkerID: m.AuthorWorkerId, AuthorUserID: m.AuthorUserId,
				AuthorAppID: m.AuthorAppId, SessionID: m.SessionId,
				ReplyToSeq: m.ReplyToSeq, Mentions: m.Mentions, NodeID: m.NodeId,
			}
			if dto.Mentions == nil {
				dto.Mentions = []string{}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				author := "you"
				if dto.AuthorName != nil && *dto.AuthorName != "" {
					author = *dto.AuthorName
				}
				// Echo the recorded author back. The whole hazard here is
				// posting under the wrong name, so the confirmation states
				// who the server actually recorded rather than who was asked
				// for.
				_, err := fmt.Fprintf(w, "✓ posted #%d as %s\n", dto.Seq, author)
				return err
			})
		},
	}
	cmd.Flags().IntVar(&replyTo, "reply-to", 0, "seq of the message this replies to")
	cmd.Flags().StringVar(&session, "session", "", "post as the Worker bound to this session id")
	cmd.Flags().BoolVar(&asMe, "as-me", false, "post as yourself rather than as a Worker")
	return cmd
}
