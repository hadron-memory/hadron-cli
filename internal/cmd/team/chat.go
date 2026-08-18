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
// (sessionRef → the session's bound worker, the Worker envelope since #974;
// the driving session lands in the envelope, D16), and write-time mention
// extraction. The CLI composes no message node, allocates nothing, and never
// parses mentions.
func newCmdTeamChat(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat <command>",
		Short: "The team App's group chat",
		Long: `Post to and read the team App's group chat — ONE well-known chat per team
App, owned end-to-end by the server (placement, ordering, authorship,
mentions). With a worktree WORKER SESSION binding (` + "`session start`" + `),
posts are authored by the bound worker and carry the driving worker session;
without one, posts are authored by you. (A chat session — your Desktop window
or Claude Code session — is a different thing and authors nothing.)

The team App resolves from --app (or the configured App context), falling
back to the worktree binding. Mention teammates as @worker-name /
@handle — a multiword name by its slug (@mary-jane).

WHICH CHAT (#470). That resolution is ambient, so ` + "`read`" + ` opens with the
App it landed on and where the scope came from — including when there is
nothing new, which is the usual case and was previously indistinguishable
from reading another team. ` + "`post`" + ` names the team chat in its receipt, and warns
on stderr BEFORE writing when the App came from a context or binding you
did not name AND no session goes on the wire (no binding, or --as-me).
With a session the server checks it against the App and refuses a mismatch;
without one nothing does, and a post can neither be recalled nor its
mentions unfired — so that signal comes early rather than in the receipt.`,
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

// appScope is a resolved team App plus the branch of resolveTeamAppScope that
// answered. The SOURCE is the load-bearing half (#458): the fallback chain is
// silent, so the same bare command run in two worktrees bound to different
// teams prints different staff with visually identical output. There is no
// wrong-looking result to notice — unless the render says where the scope
// came from.
type appScope struct {
	Ref string
	// Source is a human phrase for the winning branch, e.g. "from --app".
	Source string
	// Ambient is true when nobody named this App on THIS invocation — it came
	// from a configured context or a worktree binding rather than --app.
	//
	// A separate field rather than a comparison against Source: a gate that
	// matched the phrase would silently stop firing the day someone reworded
	// it, and the thing it gates (#470's pre-flight before an unrecallable
	// write) fails silent by nature.
	Ambient bool
}

// resolveTeamApp resolves the team App an App-addressed team command works
// against (chat, worker, role, init). Callers that RENDER the scope want
// resolveTeamAppScope instead — this is the shorthand for the ones that only
// need the ref.
func resolveTeamApp(ctx context.Context, f *cmdutil.Factory, b *binding) (string, error) {
	scope, err := resolveTeamAppScope(ctx, f, b)
	return scope.Ref, err
}

// resolveTeamAppScope resolves the team App and names the branch that
// answered: --app / the configured App context wins; otherwise the binding
// answers — its recorded AppID (#399, the bound worker's App) directly, or a
// pre-#399 binding's team memory resolved to its App (the team memory IS the
// App's shared app-class memory, so Memory.appId is the team App).
func resolveTeamAppScope(ctx context.Context, f *cmdutil.Factory, b *binding) (appScope, error) {
	appRef, err := f.App()
	if err != nil {
		return appScope{}, err
	}
	if appRef != "" {
		// f.App() merges the two, and they are worth telling apart: --app is
		// something the reader just typed, a configured context is ambient and
		// as forgettable as a binding.
		if f.AppFlag != "" {
			return appScope{Ref: appRef, Source: "from --app"}, nil
		}
		return appScope{Ref: appRef, Source: "from the App context", Ambient: true}, nil
	}
	if b != nil && b.AppID != "" {
		return appScope{Ref: b.AppID, Source: "from the worktree binding", Ambient: true}, nil
	}
	if b != nil && b.TeamMemory != "" {
		client, err := f.GraphQLClient()
		if err != nil {
			return appScope{}, err
		}
		resp, err := gen.TeamMemoryApp(ctx, client, cmdutil.CanonicalMemoryRef(b.TeamMemory))
		if err != nil {
			return appScope{}, api.MapError(err)
		}
		if resp.Memory != nil && resp.Memory.AppId != nil && *resp.Memory.AppId != "" {
			return appScope{
				Ref:     *resp.Memory.AppId,
				Source:  "from the worktree binding's team memory " + b.TeamMemory,
				Ambient: true,
			}, nil
		}
		return appScope{}, exitcode.Newf(exitcode.Usage,
			"the bound team memory %s is not an App memory — pass --app <team-app>", b.TeamMemory)
	}
	return appScope{}, exitcode.Newf(exitcode.Usage,
		"no team App — pass --app <ref>, set an App context, or bind a worker session with `hadron team session start --as <worker>`")
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
	Author         *string  `json:"author"`
	AuthorName     *string  `json:"authorName"`
	AuthorUserID   *string  `json:"authorUserId"`
	AuthorWorkerID *string  `json:"authorWorkerId"`
	SessionID      *string  `json:"sessionId"`
	ReplyToSeq     *int     `json:"replyToSeq"`
	Mentions       []string `json:"mentions"`
}

// authorKind labels a message's author for the human transcript. The canonical
// envelope distinguishes a human post (authorUserId) from a worker post
// (authorWorkerId — the Worker envelope, #974) — genuinely better than the old
// single `author` string, and previously invisible outside --json (#406).
func (m teamChatMessageDTO) authorKind() string {
	switch {
	case m.AuthorWorkerID != nil && *m.AuthorWorkerID != "":
		return " (worker)"
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
		AuthorUserID: m.AuthorUserId, AuthorWorkerID: m.AuthorWorkerId,
		SessionID: m.SessionId, ReplyToSeq: m.ReplyToSeq, Mentions: mentions,
	}
}

func newCmdTeamChatPost(f *cmdutil.Factory) *cobra.Command {
	var body, bodyFile, replyTo string
	var asMe bool
	cmd := &cobra.Command{
		Use:   "post <body|-> [--reply-to <seq>]",
		Short: "Post to the team chat",
		Long: `Post one message to the team App's chat. With a worktree worker session
binding,
the message is authored by the bound WORKER through that session (the
server verifies the session is yours, active, and bound to a non-retired
worker of this App, and records it — a worker message always traces to the
human driving it); without a binding — or with --as-me — it is authored by
you.

Mentions (@worker-name / @handle; a multiword name by its slug, e.g.
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
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			appLabel := lazyAppLabel(ctx, f, scope)
			text, err := chat.ResolveBody(cmd, body, bodyFile, f.IOStreams.In)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// PRE-FLIGHT, before the write (#470). A receipt can only diagnose
			// this one: the message is live, and the mentions have already
			// notified people on a team the author may not have meant to
			// address. There is no removal surface here, so unlike a register
			// edit it cannot be taken back.
			//
			// Gated on ambient scope AND no session on the wire. The test is
			// sessionRef, not "is there a binding file" (PR #473 review): with
			// a real sessionRef the SERVER verifies the session belongs to
			// this App and refuses a mismatch, so an ambient scope that
			// disagrees cannot land silently. Without one — no binding at all,
			// or --as-me, which deliberately drops the session — nothing
			// checks the two against each other, and an ambient App context
			// overrides the binding's App, so the post lands irreversibly in
			// an App nobody named. The binding file's mere existence proves
			// none of that.
			//
			// stderr, so the --json stdout contract is untouched; and NOT
			// suppressed under --json, because an agent posting from an
			// ambient context is precisely the exposed caller.
			//
			// DO NOT "fix" this to match the reads. The rule that --json skips
			// the App identity read (#465, #468) governs DECORATION only — a
			// label nobody needs when a machine is parsing. A safety signal
			// before a write with no undo is not decoration, and the agent
			// path is the one that most needs it. Ruled explicitly, PR #473.
			if scope.Ambient && sessionRef == nil {
				fmt.Fprintf(f.IOStreams.ErrOut,
					"note: no --app and no worker session binding — posting to %s\n", appLabel())
			}
			resp, err := gen.CreateTeamChatMessage(ctx, client, scope.Ref, text, replyToSeq, sessionRef)
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
				// The old receipt named the author and the seq — the two facts
				// never in doubt — and not the team chat. Posting into the wrong
				// team returned that line with a ✓ on it, which does not merely
				// fail to warn: it reads as confirmation the post went where
				// it was meant to.
				if _, err := fmt.Fprintf(w, "✓ posted as %s (seq %d%s)\n", who, msg.Seq, reply); err != nil {
					return err
				}
				_, err := fmt.Fprintf(w, "  app: %s\n", appLabel())
				return err
			})
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "message body, or - to read from stdin")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the message body from a file (multi-line safe)")
	cmd.Flags().StringVar(&replyTo, "reply-to", "", "seq of the message this replies to; the server wires a reply edge")
	cmd.Flags().BoolVar(&asMe, "as-me", false, "post as yourself even when a worker session is bound")
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

--mentions-me keeps only messages mentioning the bound worker;
--mentions <ref> filters for any staff member or App member (a worker name
or id of this App, or a user handle/id — passed through to the server,
which resolves it only against the App's own staff and members). The
filter matches the mentions the SERVER extracted at write time — nothing
is re-parsed — and with a filter active, nextSince advances only past the
messages returned (skipped messages are re-scanned server-side next turn,
which is free and never re-delivers them). Mention tokens carry no
uniqueness guarantee (hadron-server#979): a token may match more than one
worker, and the filter simply returns every match.

--json names the author as BOTH ` + "`authorName`" + ` and ` + "`author`" + ` — the latter is an
alias for readers written against ` + "`hadron chat read`" + `, the retired academy
dialect, which calls the field ` + "`author`" + ` (#406). Prefer authorName. Unlike
that dialect this output also separates authorUserId from authorWorkerId, so
a human post and a worker post are tellable apart; the transcript marks
them "(human)" / "(worker)".`,
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
					return exitcode.Newf(exitcode.Usage, "--mentions-me needs the worktree's worker — `hadron team session start --as <name>` first (or use --mentions <ref>)")
				}
				mentionsRef = optStr(b.WorkerID)
				if mentionsRef == nil {
					return exitcode.Newf(exitcode.Usage, "the worker session binding carries no worker — re-run `hadron team session start --as <name>`")
				}
			case mentions != "":
				// Passed through raw: the server resolves a worker id or NAME
				// of this App, or a user handle/id, only against the App's own
				// staff and members (no existence oracle) — so no client-side
				// resolution, and no ambiguity error: mention tokens carry no
				// uniqueness (hadron-server#979).
				ref := strings.TrimSpace(mentions)
				mentionsRef = &ref
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			appRef, appLabel := scope.Ref, lazyAppLabel(ctx, f, scope)
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
				// UNCONDITIONALLY, and before the messages. Zero messages is the
				// normal steady state of `chat read --since <watermark>`, and
				// the render is otherwise a bare loop — so "nobody has posted
				// since seq N" and "you are reading a team you are not on" were
				// the same empty output. The failure hid inside the expected
				// case, which is what makes this the sharper half of #470.
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
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
	cmd.Flags().BoolVar(&mentionsMe, "mentions-me", false, "only messages mentioning the bound worker")
	cmd.Flags().StringVar(&mentions, "mentions", "", "only messages mentioning this staff/App member (worker name or id, or user handle)")
	return cmd
}
