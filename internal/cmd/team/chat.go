package team

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmd/chat"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// defaultMessagesLoc is where the team group chat lives inside the team App
// memory. One chat per team memory by convention; --messages-loc overrides.
const defaultMessagesLoc = "chat:messages"

// The team chat commands are a persona-aware layer over the chat package's
// message-node dialect (chat.PostMessage / chat.CollectMessages — ONE
// implementation, so CLI- and hadron-client-channel-posted messages can't
// drift apart, D10). What team adds: coordinates and identity come from the
// worktree binding (memory = the team memory, handle = the persona name,
// role = personaRole, identity = the model), and every persona post carries
// sessionId (D16) — attribution of an agent message is the DRIVING human,
// resolved via Session.userId, not the persona's creator.
func newCmdTeamChat(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat <command>",
		Short: "The team group chat, as the persona this worktree is driving",
		Long: `Post to and read the team group chat — message nodes in the team App
memory, the same dialect ` + "`hadron chat`" + ` speaks and the hadron-client push
channel delivers into running Claude Code sessions (Codex and humans poll
via CLI).

The worktree binding supplies everything: the chat lives in the bound team
memory (` + "`session start -m`" + `) under ` + defaultMessagesLoc + `, and messages
post as the bound persona (handle = the persona name, with the session's
model as identity) carrying the session id — so a message always traces to
the human driving the persona. For a chat outside a session binding, use
` + "`hadron chat`" + ` directly.`,
	}
	cmd.AddCommand(newCmdTeamChatPost(f))
	cmd.AddCommand(newCmdTeamChatRead(f))
	return cmd
}

// teamChatCoords resolves the chat coordinates from flags and the binding.
// The binding may be nil when -m names the memory explicitly.
func teamChatCoords(b *binding, memoryFlag, messagesLocFlag string) (chat.Coords, error) {
	mem := ""
	if b != nil {
		mem = b.TeamMemory
	}
	if memoryFlag != "" {
		mem = cmdutil.CanonicalMemoryRef(memoryFlag)
	}
	if mem == "" {
		return chat.Coords{}, exitcode.Newf(exitcode.Usage,
			"no team memory — pass -m <team-memory>, or record it with `session start -m` (see `hadron team init`)")
	}
	loc := strings.TrimSuffix(messagesLocFlag, ":")
	if loc == "" {
		loc = defaultMessagesLoc
	}
	return chat.Coords{Memory: mem, MessagesLoc: loc}, nil
}

// handleRE is the chat dialect's handle charset (mirrors the channel's
// MENTION_RE capture group).
var handleRE = regexp.MustCompile(`[^a-z0-9_-]+`)

// handleFromPersona folds a persona display name ("Ana María") onto the chat
// handle charset: lowercased, spaces to dashes, anything else dropped.
func handleFromPersona(name string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(name))
	h = strings.ReplaceAll(h, " ", "-")
	h = handleRE.ReplaceAllString(h, "")
	h = strings.Trim(h, "-")
	if h == "" {
		return "", exitcode.Newf(exitcode.Error, "persona name %q yields no usable chat handle", name)
	}
	return h, nil
}

func newCmdTeamChatPost(f *cmdutil.Factory) *cobra.Command {
	var body, bodyFile, replyTo, memory, messagesLoc string
	cmd := &cobra.Command{
		Use:   "post (--body <text|-> | --body-file <path>) [--reply-to <seq-or-loc>]",
		Short: "Post to the team chat as the bound persona",
		Long: `Post one message to the team group chat as the persona this worktree is
driving. The message carries the session id (attribution = the human
driving the persona) and the session's model as identity; mentions
(@handle) are extracted automatically.

--reply-to takes the seq of the message being answered (as shown by
` + "`team chat read`" + `) — or a message loc, as ` + "`hadron chat post`" + ` accepts.`,
		Example: `  hadron team chat post --body "@rufus schema is live, over to you"
  hadron team chat post --reply-to 42 --body "done"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, _, err := readBinding(ctx)
			if err != nil {
				return err
			}
			if b == nil {
				return exitcode.Newf(exitcode.NotFound, "no persona bound to this worktree — `hadron team session start --as <name>` first (or post with `hadron chat post`)")
			}
			if err := checkBindingServer(f, b); err != nil {
				return err
			}
			c, err := teamChatCoords(b, memory, messagesLoc)
			if err != nil {
				return err
			}
			handle, err := handleFromPersona(b.PersonaName)
			if err != nil {
				return err
			}
			text, err := chat.ResolveBody(cmd, body, bodyFile, f.IOStreams.In)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			target, err := resolveReplyTarget(ctx, client, c, replyTo)
			if err != nil {
				return err
			}
			identity := b.Model
			if identity == "" {
				identity = b.Tool
			}
			res, err := chat.PostMessage(ctx, client, chat.PostInput{
				Coords:   c,
				Handle:   handle,
				Identity: identity,
				Role:     b.PersonaRole,
				Body:     text,
				ReplyTo:  target,
				Extra:    map[string]any{"sessionId": b.SessionID},
			})
			if err != nil {
				return err
			}
			result := struct {
				Loc       string `json:"loc"`
				Seq       *int   `json:"seq"`
				Author    string `json:"author"`
				SessionID string `json:"sessionId"`
				ReplyTo   string `json:"replyTo,omitempty"`
			}{res.Loc, res.Seq, handle, b.SessionID, target}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				fmt.Fprintf(w, "✓ posted as %s%s (seq %s)\n", handle, roleSuffix(optStr(b.PersonaRole)), seqString(res.Seq))
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "message body, or - to read from stdin")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the message body from a file (multi-line safe)")
	cmd.Flags().StringVar(&replyTo, "reply-to", "", "seq (or loc) of the message this replies to; adds a reply edge")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "team App memory override (defaults to the binding's)")
	cmd.Flags().StringVar(&messagesLoc, "messages-loc", "", "message-parent loc override (default "+defaultMessagesLoc+")")
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	cmd.MarkFlagsOneRequired("body", "body-file")
	return cmd
}

// resolveReplyTarget turns a --reply-to value into the loc the reply edge
// targets. A bare number is a seq (the citation primitive readers see) and
// resolves through the message list; anything else passes through as a loc.
func resolveReplyTarget(ctx context.Context, client graphql.Client, c chat.Coords, replyTo string) (string, error) {
	replyTo = strings.TrimSpace(replyTo)
	if replyTo == "" {
		return "", nil
	}
	seq, err := strconv.Atoi(replyTo)
	if err != nil {
		return replyTo, nil
	}
	msgs, err := chat.CollectMessages(ctx, client, c, 0)
	if err != nil {
		return "", err
	}
	for _, m := range msgs {
		if m.Seq != nil && *m.Seq == seq {
			return m.Loc, nil
		}
	}
	return "", exitcode.Newf(exitcode.NotFound, "no message with seq %d in this chat — `team chat read` shows the seqs", seq)
}

func newCmdTeamChatRead(f *cmdutil.Factory) *cobra.Command {
	var memory, messagesLoc string
	var since int
	var mentionsMe bool
	cmd := &cobra.Command{
		Use:     "read [--since <seq>] [--mentions-me]",
		Aliases: []string{"pull"},
		Short:   "Read the team chat since a seq",
		Long: `Read team-chat messages, newest-tracking by the server-assigned seq. Pass
--since <seq> for only newer messages; the response's nextSince is the seq
to pass next turn — it advances past everything read, INCLUDING messages
--mentions-me filtered out, so a mentions-only reader never re-reads.

--mentions-me keeps only messages mentioning the bound persona's @handle
(stored mentions, else recomputed from the body — hand-created messages
carry none).`,
		Example: `  hadron team chat read --since 42
  hadron team chat read --mentions-me --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, _, err := readBinding(ctx)
			if err != nil && memory == "" {
				return err
			}
			c, err := teamChatCoords(b, memory, messagesLoc)
			if err != nil {
				return err
			}
			var handle string
			if mentionsMe {
				if b == nil {
					return exitcode.Newf(exitcode.Usage, "--mentions-me needs the worktree's persona — `hadron team session start --as <name>` first")
				}
				if handle, err = handleFromPersona(b.PersonaName); err != nil {
					return err
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			msgs, err := chat.CollectMessages(ctx, client, c, since)
			if err != nil {
				return err
			}
			// nextSince from EVERYTHING read, before any mentions filter.
			next := since
			for _, m := range msgs {
				if m.Seq != nil && *m.Seq > next {
					next = *m.Seq
				}
			}
			if mentionsMe {
				kept := []chat.Message{}
				for _, m := range msgs {
					if mentionsHandle(m, handle) {
						kept = append(kept, m)
					}
				}
				msgs = kept
			}
			if msgs == nil {
				msgs = []chat.Message{}
			}
			result := struct {
				Messages  []chat.Message `json:"messages"`
				NextSince int            `json:"nextSince"`
			}{msgs, next}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				for _, m := range result.Messages {
					who := m.Author
					if m.Role != "" {
						who = fmt.Sprintf("%s (%s)", m.Author, m.Role)
					}
					fmt.Fprintf(w, "[%s] %s: %s\n", seqString(m.Seq), who, m.Body)
				}
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&since, "since", 0, "only messages with seq greater than this (0 = whole history)")
	cmd.Flags().BoolVar(&mentionsMe, "mentions-me", false, "only messages mentioning the bound persona's @handle")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "team App memory override (defaults to the binding's)")
	cmd.Flags().StringVar(&messagesLoc, "messages-loc", "", "message-parent loc override (default "+defaultMessagesLoc+")")
	return cmd
}

// mentionsHandle reports whether a message mentions the handle — stored
// mentions when present, else recomputed from the body.
func mentionsHandle(m chat.Message, handle string) bool {
	ms := m.Mentions
	if len(ms) == 0 {
		ms = chat.Mentions(m.Body)
	}
	for _, h := range ms {
		if strings.EqualFold(h, handle) {
			return true
		}
	}
	return false
}

func seqString(seq *int) string {
	if seq == nil {
		return "?"
	}
	return fmt.Sprintf("%d", *seq)
}
