package team

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/chat"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// The team chat commands are THIN wrappers over the platform operations
// (hadron-server#939/#941, spec cor:agt:020:04): `createTeamChatMessage` and
// `teamChatMessages`. The server owns everything that used to live here —
// placement (chats:team in the Team Agent's shared app memory, bootstrapped
// on the first post), atomic seq allocation (#919), author derivation
// (sessionRef → the session's bound persona; the driving session lands in the
// envelope, D16), and write-time mention extraction. The CLI composes no
// message node, allocates nothing, and never parses mentions.
func newCmdTeamChat(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat <command>",
		Short: "The team App's group chat",
		Long: `Post to and read the team App's group chat — ONE well-known chat per team
App, owned end-to-end by the server (placement, ordering, authorship,
mentions). With a worktree session binding (` + "`session start`" + `), posts are
authored by the bound persona and carry the driving session; without one,
posts are authored by you.

The team App resolves from --app (or the configured App context), falling
back to the binding's team memory. Mention teammates as @persona-name /
@handle — a multiword name by its slug (@mary-jane).`,
	}
	cmd.AddCommand(newCmdTeamChatPost(f))
	cmd.AddCommand(newCmdTeamChatRead(f))
	return cmd
}

// readBindingOrNilWithApp reads the worktree binding, treating "not inside a
// git worktree" as simply NO binding whenever an App context is available —
// a human posting with --app (or a configured App) needs no worktree at all
// (PR #382 review). Without an App context the binding error stands: it is
// the actionable message.
func readBindingOrNilWithApp(ctx context.Context, f *cmdutil.Factory) (*binding, error) {
	b, _, err := readBinding(ctx)
	if err != nil {
		if appRef, appErr := f.App(); appErr == nil && appRef != "" {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// resolveTeamApp resolves the team App an App-addressed team command works
// against (the chat operations, and `team roster`): --app / the configured App
// context wins; otherwise the binding's team memory is resolved to its App
// (the team memory IS the App's shared app-class memory, so Memory.appId is
// the team App).
func resolveTeamApp(ctx context.Context, f *cmdutil.Factory, b *binding) (string, error) {
	appRef, err := f.App()
	if err != nil {
		return "", err
	}
	if appRef != "" {
		return appRef, nil
	}
	if b != nil && b.TeamMemory != "" {
		client, err := f.GraphQLClient()
		if err != nil {
			return "", err
		}
		resp, err := gen.TeamMemoryApp(ctx, client, cmdutil.CanonicalMemoryRef(b.TeamMemory))
		if err != nil {
			return "", api.MapError(err)
		}
		if resp.Memory != nil && resp.Memory.AppId != nil && *resp.Memory.AppId != "" {
			return *resp.Memory.AppId, nil
		}
		return "", exitcode.Newf(exitcode.Usage,
			"the bound team memory %s is not an App memory — pass --app <team-app>", b.TeamMemory)
	}
	return "", exitcode.Newf(exitcode.Usage,
		"no team App — pass --app <ref>, set an App context, or bind a session with `hadron team session start -m <team-memory>`")
}

// teamChatMessageDTO is the stable --json shape of one message: the server
// envelope, plus ONE CLI-only field — `author`, an alias of the server's
// `authorName` (see below). It is no longer verbatim, so do not treat every
// field here as a server field when touching codegen or the schema.
type teamChatMessageDTO struct {
	NodeID string `json:"nodeId"`
	Seq    int    `json:"seq"`
	Body   string `json:"body"`
	At     string `json:"at"`
	// Author is an ALIAS of AuthorName, carried for the retired academy
	// dialect (#406). `hadron chat read` names this field `author`, and reads
	// "accept the retired dialect forever" per the #369 decision record — so a
	// jq filter or agent that learned `.author` there reported every canonical
	// message as unauthored. A wrong field name yielding a plausible wrong
	// answer rather than an error is the dangerous shape; one duplicated
	// string makes both dialects' readers correct.
	Author        *string  `json:"author"`
	AuthorName    *string  `json:"authorName"`
	AuthorUserID  *string  `json:"authorUserId"`
	AuthorAgentID *string  `json:"authorAgentId"`
	SessionID     *string  `json:"sessionId"`
	ReplyToSeq    *int     `json:"replyToSeq"`
	Mentions      []string `json:"mentions"`
}

// authorKind labels a message's author for the human transcript. The canonical
// envelope distinguishes a human post (authorUserId) from a persona post
// (authorAgentId) — genuinely better than the old single `author` string, and
// previously invisible outside --json (#406).
func (m teamChatMessageDTO) authorKind() string {
	switch {
	case m.AuthorAgentID != nil && *m.AuthorAgentID != "":
		return " (persona)"
	case m.AuthorUserID != nil && *m.AuthorUserID != "":
		return " (human)"
	default:
		return ""
	}
}

func teamChatMessageDTOFromFields(m gen.TeamChatMessageFields) teamChatMessageDTO {
	mentions := m.Mentions
	if mentions == nil {
		mentions = []string{}
	}
	return teamChatMessageDTO{
		NodeID: m.NodeId, Seq: m.Seq, Body: m.Body, At: m.At,
		Author: m.AuthorName, AuthorName: m.AuthorName,
		AuthorUserID: m.AuthorUserId, AuthorAgentID: m.AuthorAgentId,
		SessionID: m.SessionId, ReplyToSeq: m.ReplyToSeq, Mentions: mentions,
	}
}

func newCmdTeamChatPost(f *cmdutil.Factory) *cobra.Command {
	var body, bodyFile, replyTo string
	var asMe bool
	cmd := &cobra.Command{
		Use:   "post <body|-> [--reply-to <seq>]",
		Short: "Post to the team chat",
		Long: `Post one message to the team App's chat. With a worktree session binding,
the message is authored by the bound PERSONA through that session (the
server verifies the session is yours, active, and of this App, and records
it — a persona message always traces to the human driving it); without a
binding — or with --as-me — it is authored by you.

Mentions (@persona-name / @handle; a multiword name by its slug, e.g.
@mary-jane) are extracted server-side into the message. The body is the
positional argument (- reads stdin); --body/--body-file are accepted too,
matching ` + "`hadron chat post`" + `. Exactly one source.

--reply-to takes the seq of the message being answered, as shown by
` + "`team chat read`" + `.`,
		Example: `  hadron team chat post "@rufus schema is live, over to you"
  hadron team chat post --reply-to 42 "done"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			// Exactly one body source: the positional, --body, or --body-file.
			if len(args) == 1 {
				if body != "" || bodyFile != "" {
					return exitcode.Newf(exitcode.Usage, "pass the body either positionally or via --body/--body-file, not both")
				}
				body = args[0]
			} else if body == "" && bodyFile == "" {
				return exitcode.Newf(exitcode.Usage, "no message body — pass it as the argument (or - for stdin), or via --body/--body-file")
			}
			var replyToSeq *int
			if s := strings.TrimSpace(replyTo); s != "" {
				seq, err := strconv.Atoi(s)
				if err != nil {
					return exitcode.Newf(exitcode.Usage, "--reply-to takes the seq shown by `team chat read`, got %q", replyTo)
				}
				replyToSeq = &seq
			}
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			var sessionRef *string
			if b != nil && !asMe {
				if err := checkBindingServer(f, b); err != nil {
					return err
				}
				sessionRef = optStr(b.SessionID)
			}
			appRef, err := resolveTeamApp(ctx, f, b)
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
			resp, err := gen.CreateTeamChatMessage(ctx, client, appRef, text, replyToSeq, sessionRef)
			if err != nil {
				return api.MapError(err)
			}
			msg := teamChatMessageDTOFromFields(resp.CreateTeamChatMessage.TeamChatMessageFields)
			return output.Write(f.IOStreams, f.JSON, msg, func(w io.Writer) error {
				who := "you"
				if msg.AuthorName != nil {
					who = *msg.AuthorName
				}
				reply := ""
				if msg.ReplyToSeq != nil {
					reply = fmt.Sprintf(", reply to %d", *msg.ReplyToSeq)
				}
				fmt.Fprintf(w, "✓ posted as %s (seq %d%s)\n", who, msg.Seq, reply)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "message body, or - to read from stdin")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the message body from a file (multi-line safe)")
	cmd.Flags().StringVar(&replyTo, "reply-to", "", "seq of the message this replies to; the server wires a reply edge")
	cmd.Flags().BoolVar(&asMe, "as-me", false, "post as yourself even when a persona session is bound")
	// One body source is enforced in RunE (a cobra flag group can't see the
	// positional form).
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	return cmd
}

// teamChatPageSize is the server's list cap; reads page with the seq
// watermark as the cursor (each page's last seq is the next sinceSeq).
const teamChatPageSize = 200

func newCmdTeamChatRead(f *cmdutil.Factory) *cobra.Command {
	var since int
	var mentionsMe bool
	var mentions string
	cmd := &cobra.Command{
		Use:     "read [--since <seq>] [--mentions-me | --mentions <ref>]",
		Aliases: []string{"pull"},
		Short:   "Read the team chat since a seq",
		Long: `Read team-chat messages, oldest first by the server-assigned seq. Pass
--since <seq> for only newer messages; the response's nextSince is the seq
to pass next turn.

--mentions-me keeps only messages mentioning the bound persona;
--mentions <ref> filters for any roster member (a persona/agent ref or a
user handle). The filter matches the mentions the SERVER extracted at
write time — nothing is re-parsed — and with a filter active, nextSince
advances only past the messages returned (skipped messages are re-scanned
server-side next turn, which is free and never re-delivers them).

--json names the author as BOTH ` + "`authorName`" + ` and ` + "`author`" + ` — the latter is an
alias for readers written against ` + "`hadron chat read`" + `, the retired academy
dialect, which calls the field ` + "`author`" + ` (#406). Prefer authorName. Unlike
that dialect this output also separates authorUserId from authorAgentId, so
a human post and a persona post are tellable apart; the transcript marks
them "(human)" / "(persona)".`,
		Example: `  hadron team chat read --since 42
  hadron team chat read --mentions-me --json`,
		Args: cobra.NoArgs,
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
			var mentionsRef *string
			switch {
			case mentionsMe && mentions != "":
				return exitcode.Newf(exitcode.Usage, "pass --mentions-me or --mentions <ref>, not both")
			case mentionsMe:
				if b == nil {
					return exitcode.Newf(exitcode.Usage, "--mentions-me needs the worktree's persona — `hadron team session start --as <name>` first (or use --mentions <ref>)")
				}
				mentionsRef = optStr(b.AgentID)
				if mentionsRef == nil {
					mentionsRef = optStr(b.AgentURN)
				}
				if mentionsRef == nil {
					return exitcode.Newf(exitcode.Usage, "the session binding carries no persona agent — re-run `hadron team session start --as <name>`")
				}
			case mentions != "":
				ref := strings.TrimSpace(mentions)
				// A bare, non-URN value may be a persona NAME — resolve it
				// against the roster so `--mentions Iris` works. Anything the
				// roster doesn't know passes through raw: the server accepts
				// agent/user ids, URNs, and bare user HANDLES (but not persona
				// names, which is why the roster pass runs client-side).
				if !strings.Contains(ref, ":") {
					if p, pErr := resolvePersona(ctx, client, nil, ref); pErr == nil {
						ref = p.Id
					}
				}
				mentionsRef = &ref
			}
			appRef, err := resolveTeamApp(ctx, f, b)
			if err != nil {
				return err
			}
			// Page to exhaustion with the watermark as the cursor: items are
			// seq-ascending and sinceSeq is strictly-greater, so each page's
			// last seq is the next page's cursor — no offset drift.
			msgs := []teamChatMessageDTO{}
			cursor := since
			for {
				limit := teamChatPageSize
				resp, err := gen.TeamChatMessages(ctx, client, appRef, &cursor, mentionsRef, &limit, nil)
				if err != nil {
					return api.MapError(err)
				}
				page := resp.TeamChatMessages.Items
				for _, m := range page {
					if m == nil {
						continue
					}
					msgs = append(msgs, teamChatMessageDTOFromFields(m.TeamChatMessageFields))
				}
				if len(page) < teamChatPageSize {
					break
				}
				cursor = msgs[len(msgs)-1].Seq
			}
			next := since
			for _, m := range msgs {
				if m.Seq > next {
					next = m.Seq
				}
			}
			result := struct {
				Messages  []teamChatMessageDTO `json:"messages"`
				NextSince int                  `json:"nextSince"`
			}{msgs, next}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				for _, m := range result.Messages {
					who := "?"
					if m.AuthorName != nil {
						who = *m.AuthorName
					}
					reply := ""
					if m.ReplyToSeq != nil {
						reply = fmt.Sprintf(" (reply to %d)", *m.ReplyToSeq)
					}
					fmt.Fprintf(w, "[%d] %s%s%s: %s\n", m.Seq, who, m.authorKind(), reply, m.Body)
				}
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&since, "since", 0, "only messages with seq greater than this (0 = whole history)")
	cmd.Flags().BoolVar(&mentionsMe, "mentions-me", false, "only messages mentioning the bound persona")
	cmd.Flags().StringVar(&mentions, "mentions", "", "only messages mentioning this roster member (persona/agent ref or user handle)")
	return cmd
}
