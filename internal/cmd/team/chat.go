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

// TeamChatPageSize is the server's list cap and the default page size; an
// unbounded read pages with the seq watermark as the cursor (each page's last
// seq is the next sinceSeq).
//
// EXPORTED for the command tests, which need a FULL page to drive the
// exhaustive loop past its first iteration — the loop stops on a short page, so
// a test hard-coding 200 would silently stop exercising exhaustion the day this
// changes.
const TeamChatPageSize = 200

func newCmdTeamChatRead(f *cmdutil.Factory) *cobra.Command {
	var since, before, limit int
	var mentionsMe bool
	var mentions string
	cmd := &cobra.Command{
		Use:     "read [--since <seq>] [--before <seq>] [--limit <n>] [--mentions-me | --mentions <ref>]",
		Aliases: []string{"pull"},
		Short:   "Read the team chat, forwards from a seq or backwards from one",
		Long: `Read team-chat messages, oldest first by the server-assigned seq. Pass
--since <seq> for only newer messages; the response's nextSince is the seq
to pass next turn.

TWO CURSORS, IN OPPOSITE DIRECTIONS. --since walks FORWARD and, on its own,
reads to the end of the chat. --before <seq> walks BACK: it returns the page
immediately before that seq — the newest messages preceding it, still
oldest-first — and the response's prevBefore is what to pass next. That is how
you walk a long history without asking for all of it at once, which on a chat
with real history is the difference between a read you can hold and one you
cannot.

EITHER --before OR --limit MAKES THE READ ONE PAGE. With neither, the command
pages to exhaustion from --since, exactly as it always has. --limit sets how
big that page is (default 200, the server's cap). They compose, so
` + "`--since 300 --before 340`" + ` reads a bounded slice in the middle.

prevBefore is NULL when the page came back EMPTY, and that is the ONLY
end-of-history signal — walk back until you get one. Do not try to compute
"is there more" from a count: the server's total is scoped to the cursor,
not to the chat (hadron-server#1121), so on the second page back it reports
fewer messages than exist and a reader trusting it stops early.

A --before read never advances the watermark below, and cannot: it returns
the newest messages before a cursor, so everything between --since and that
page is unread by construction. The gap is in the MIDDLE, where a
start-of-read check cannot see it.

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

An UNFILTERED read of the worktree's OWN team App records a WATERMARK on the
binding (#474), which is what lets ` + "`session log`" + ` tell you how much has
landed since. Nothing to pass: it is the seq this command just returned.
Both qualifiers matter — a filtered read skips the messages in between (see
nextSince above), and another team's seq is not this binding's cursor — so
those reads deliberately leave the watermark where it was, as does a read
whose output could not be written. "Another team" is decided on canonical App
ids, so naming your own team by URN still counts as reading it. The watermark is NOT nextSince: it advances
only on a read CONTIGUOUS with what the binding already holds, and only to a
seq the server actually returned — so a --since ahead of the watermark (or
past the end of the chat) reads a window rather than a prefix and records
nothing. Reading a chat that is EMPTY still counts as
having read it.

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
			// BOUNDED OR EXHAUSTIVE, and exactly one flag decides which
			// (#548). Either cursor-bound makes this a SINGLE PAGE:
			//
			//   --before  the page immediately BEFORE a seq — the backward
			//             mirror, and the only way to walk history without
			//             asking for all of it at once;
			//   --limit   how big a page is.
			//
			// Neither: the historical behaviour, unchanged — page forward to
			// exhaustion from --since. That is what makes this additive rather
			// than a break, and it is why `--limit` bounds the read instead of
			// merely sizing a page nobody sees: an exhaustive read has no page
			// size a caller could observe, so a `--limit` that only tuned it
			// would be a flag with no effect. A flag whose effect depends on
			// another flag being present is the shape this repo has already
			// filed twice; each of these means something on its own.
			// REFUSED BEFORE THE QUERY, not clamped after it (@codex P2,
			// @copilot). Every one of these produces an EMPTY page, and an
			// empty page is this command's end-of-history signal — so a
			// permissive parse would answer "you have reached the beginning"
			// to a caller with the whole chat still ahead of them. The
			// contract's one guarantee, broken by a typo.
			//
			// `--limit 0` is the sharpest: the SDL gives it a MEANING (return
			// only `total`), so it is not a nonsense value the server rejects
			// — it succeeds, returns nothing, and looks exactly like the end.
			// Same shape as `asset ls`, which already refuses it.
			switch {
			case cmd.Flags().Changed("limit") && limit == 0:
				return exitcode.Newf(exitcode.Usage,
					"--limit must be at least 1 (the server reads 0 as \"count only\", which returns an empty page and is indistinguishable from the end of the chat)")
			case limit < 0:
				return exitcode.Newf(exitcode.Usage, "--limit must not be negative")
			case limit > TeamChatPageSize:
				// Refused rather than silently capped. The server caps it
				// anyway, so a bounded read would quietly return 200 for a
				// request of 500 — which is not wrong, but leaves the caller
				// believing they hold a page they do not.
				return exitcode.Newf(exitcode.Usage,
					"--limit must be at most %d (the server's page cap) — walk with --before to read more", TeamChatPageSize)
			case cmd.Flags().Changed("before") && before < 1:
				// seqs start at 1, so a cursor at or below it can only ever be
				// empty. `--before 1` IS legal and means "nothing older", which
				// is the honest end-of-history answer rather than a mistake.
				return exitcode.Newf(exitcode.Usage,
					"--before must be at least 1 — seq numbers start at 1, so a cursor below it can only return nothing")
			}
			bounded := cmd.Flags().Changed("before") || cmd.Flags().Changed("limit")
			pageSize := TeamChatPageSize
			if cmd.Flags().Changed("limit") {
				pageSize = limit
			}
			msgs := []teamChatMessageDTO{}
			cursor := since
			for {
				size := pageSize
				var beforeArg *int
				if cmd.Flags().Changed("before") {
					b := before
					beforeArg = &b
				}
				resp, err := gen.TeamChatMessages(ctx, client, appRef, &cursor, mentionsRef, &size, nil, beforeArg)
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
				// A bounded read is ONE page by definition; looping here would
				// be the caller's job done wrongly, since only they know which
				// direction they are walking.
				if bounded || len(page) < pageSize {
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
			// The watermark the binding records is NOT `next`. `next` is a PAGING
			// cursor: it answers "where do I resume", falls back to whatever the
			// caller passed, and is the right value to hand back on the wire. The
			// watermark answers "how far have I actually SEEN", which is a claim
			// about coverage, and the two come apart in both directions.
			//
			// It advances only when the read was CONTIGUOUS with what the binding
			// already claims — starting at or before the existing watermark, or at
			// zero when there is none. An arbitrary `--since` is a window, not a
			// prefix: `--since 100` from a watermark of 90 renders 101 onward and
			// never shows 91–100, so recording 101 would bury exactly ten messages
			// while reporting the reader as caught up (PR #493 review, P1).
			//
			// This subsumes the unverified-cursor case: `--since 999999` on a
			// hundred-message chat is not contiguous either, so it cannot mark the
			// team's next year of messages read on a typo.
			contiguous := (b == nil) ||
				(b.ChatSeenSeq == nil && since == 0) ||
				(b.ChatSeenSeq != nil && since <= *b.ChatSeenSeq)
			// …and only ever TO a seq the server actually returned, with one
			// addition: asking from the very beginning and being handed nothing
			// means the chat is genuinely empty, which is read-through-0 rather
			// than never-read.
			verified, ok := 0, false
			for _, m := range msgs {
				if !ok || m.Seq > verified {
					verified, ok = m.Seq, true
				}
			}
			if !ok && since == 0 {
				ok = true
			}
			// Two more conditions, both from the same review, both P1:
			//
			// 1. UNFILTERED reads only. --mentions-me/--mentions returns only
			//    matching messages, so its highest seq says nothing about the ones
			//    in between — storing it as the ALL-messages watermark skips them
			//    permanently, and the reader is then told they are caught up on
			//    messages they were never shown.
			//
			// 2. The resolved App must BE the binding's. Reading App B from a
			//    worktree bound to App A would write B's cursor into A's binding.
			//    isBindingsApp compares canonical ids rather than raw refs — the
			//    same App named as a URN is still the same App, and treating it
			//    as another team would pin the watermark forever. It is paired
			//    with bindingServerMatches, because an App id is unique within a
			//    deployment and not across them: a clone or a restore carries the
			//    id over, so `--server <other>` can satisfy the id check while
			//    holding an unrelated chat. `chat post` and the session mutations
			//    already REFUSE on that mismatch; a read is legitimate, so only
			//    the bookkeeping is skipped.
			unfiltered := mentionsRef == nil
			recordWatermark := func() {
				// Best-effort and deliberately silent: a read that succeeded must
				// not fail because the bookkeeping did, and a reader with no
				// binding (--app only) is an ordinary case, not an error.
				//
				// ORDER MATTERS: every local, free predicate is checked before
				// isBindingsApp, which may cost a round trip. A read with no
				// binding must stay as cheap as it was.
				// A --before READ NEVER ADVANCES IT (#548), and this is the
				// same rule as `--since 100` from a watermark of 90 rather than
				// a new one: a backward page is the NEWEST messages before the
				// cursor, so everything between `since` and that page is
				// unread by construction. The start being contiguous is not
				// enough — the hole is in the MIDDLE, which `contiguous` cannot
				// see because it only looks at where the read began.
				//
				// "A window is not a prefix" (review:a-claim-must-not-outrun-
				// its-evidence, finding 8), and this is the first surface that
				// can produce a window whose start looks perfectly contiguous.
				//
				// --limit alone is NOT excluded, and that asymmetry is the
				// point: a bounded FORWARD read from the watermark is a genuine
				// prefix — seqs 1..30 of a chat, with nothing skipped — so
				// recording 30 claims exactly what was seen.
				if b == nil || !ok || !unfiltered || !contiguous || cmd.Flags().Changed("before") ||
					!bindingServerMatches(f, b) {
					return
				}
				if !isBindingsApp(ctx, f, scope.Ref, b.AppID) {
					return
				}
				if b.ChatSeenSeq == nil || verified > *b.ChatSeenSeq {
					recordChatWatermark(ctx, b.SessionID, verified)
				}
			}
			// prevBefore is nextSince's mirror: the cursor for the page BEFORE
			// this one, i.e. the lowest seq returned. Pass it as --before to
			// keep walking back.
			//
			// NULL when the page was empty, and that is the ONLY end-of-history
			// signal this command offers — deliberately. The obvious
			// alternative, comparing against `total`, is wrong on exactly the
			// pages where it matters: total is cursor-scoped under beforeSeq
			// (hadron-server#1121, measured by @Gil), so a reader adopting it
			// would conclude it had reached the beginning while messages
			// remained. An empty page cannot be scoped wrong.
			var prevBefore *int
			for i := range msgs {
				if prevBefore == nil || msgs[i].Seq < *prevBefore {
					s := msgs[i].Seq
					prevBefore = &s
				}
			}
			result := struct {
				Messages  []teamChatMessageDTO `json:"messages"`
				NextSince int                  `json:"nextSince"`
				// ADDED, never replacing nextSince: the two answer opposite
				// questions and a reader walking forward must not have to learn
				// a new key.
				PrevBefore *int `json:"prevBefore"`
			}{msgs, next, prevBefore}
			// AFTER the render, and only if it succeeded (PR #493 review). The
			// watermark claims the reader has seen these messages; a closed pipe
			// or a full disk means they have not, and marking them read would
			// bury them. `next` still goes out as the paging cursor regardless —
			// that one is about the wire, not about what was read.
			if err := output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
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
					// Checked, like the header above it: the watermark that follows a
					// successful render claims the reader SAW these messages, and a
					// pipe that closes partway through means they saw only some
					// (PR #493 review).
					if _, err := fmt.Fprintf(w, "[%d] %s%s%s: %s\n", m.Seq, who, m.authorKind(), reply, m.Body); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
			recordWatermark()
			return nil
		},
	}
	cmd.Flags().IntVar(&since, "since", 0, "only messages with seq greater than this (0 = whole history)")
	// "the newest of those" describes WHICH messages, not what order they
	// print in — the page is still rendered oldest-first like every other read
	// (@copilot: the first wording said "newest first", which reads as
	// ordering and contradicts the paragraph above it).
	cmd.Flags().IntVar(&before, "before", 0, "one page of the messages just before this seq — the newest of those, still printed oldest-first")
	cmd.Flags().IntVar(&limit, "limit", TeamChatPageSize, "messages per page; giving it makes the read ONE page instead of the whole history")
	cmd.Flags().BoolVar(&mentionsMe, "mentions-me", false, "only messages mentioning the bound worker")
	cmd.Flags().StringVar(&mentions, "mentions", "", "only messages mentioning this staff/App member (worker name or id, or user handle)")
	return cmd
}
