package team

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

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
		Long: `Post to and read your team's group chat. Each team App has exactly one chat,
and the server decides message order, authors and @mentions.

Who a post comes from: with a worker session bound to this worktree (see
` + "`" + `hadron team session start` + "`" + `), a post is written by that worker. Without one,
the post is refused (exit 4) unless you pass --as-me, which posts as you.
A chat session (your desktop window or a Claude Code conversation) is a
different thing and writes nothing.

Which team: the chat of the App named by --app, else the configured App
context, else the App this worktree is bound to. ` + "`" + `read` + "`" + ` prints that App at the
top, even when nothing is new, so you can tell you are in the right team. ` + "`" + `post` + "`" + `
names the team in its receipt. When --as-me is given and the App came from a
context or binding you did not name, ` + "`" + `post` + "`" + ` warns on stderr before writing,
because a post made as yourself cannot be recalled and its mentions cannot be
unsent. With a worker session the server checks the App and refuses a mismatch.

Mention a teammate as @worker-name or @handle; for a name of several words use
its slug (@mary-jane).

Which command to use:
  read       look at messages (the everyday command)
  check      collect only the messages that tag you, into a private file
  inbox      show messages the system queued for your worker
  mark-read  move your read position forward without reading
  search     find old messages by text
  post       write a message`,
	}
	cmd.AddCommand(newCmdTeamChatPost(f))
	cmd.AddCommand(newCmdTeamChatRead(f))
	cmd.AddCommand(newCmdTeamChatMarkRead(f))
	cmd.AddCommand(newCmdTeamChatCheck(f))
	cmd.AddCommand(newCmdTeamChatInbox(f))
	cmd.AddCommand(newCmdTeamChatSearch(f))
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
type teamTagDTO struct {
	HeadID string `json:"headId"`
	Label  string `json:"label"`
}

type teamChatMessageDTO struct {
	TeamTags []teamTagDTO `json:"teamTags"`
	NodeID   string       `json:"nodeId"`
	Seq      int          `json:"seq"`
	Body     string       `json:"body"`
	At       string       `json:"at"`
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
	var tags []teamTagDTO
	if m.TeamTags != nil {
		tags = make([]teamTagDTO, 0, len(m.TeamTags))
		for _, tag := range m.TeamTags {
			tags = append(tags, teamTagDTO{HeadID: tag.HeadId, Label: tag.Label})
		}
	}
	return teamChatMessageDTO{
		TeamTags: tags,
		NodeID:   m.NodeId, Seq: m.Seq, Body: m.Body, At: m.At,
		Author: m.AuthorName, AuthorName: m.AuthorName,
		AuthorUserID: m.AuthorUserId, AuthorWorkerID: m.AuthorWorkerId,
		SessionID: m.SessionId, ReplyToSeq: m.ReplyToSeq, Mentions: mentions,
	}
}

func newCmdTeamChatPost(f *cmdutil.Factory) *cobra.Command {
	return newCmdTeamChatPostWithHostDelivery(f, preparePostHostDelivery(f))
}

// Host delivery is injected only after the landed C1/S1 adapter is available.
func newCmdTeamChatPostWithHostDelivery(f *cmdutil.Factory, prepare postHostDeliveryPreparer) *cobra.Command {
	var hostConfig string
	var push bool
	var body, bodyFile, replyTo string
	var asMe bool
	cmd := &cobra.Command{
		Use:   "post <body|-> [--reply-to <seq>]",
		Short: "Post to the team chat",
		Long: `Post one message to the team App's chat (the team is chosen as in
` + "`" + `hadron team chat --help` + "`" + `). The body is the argument, or --body, or --body-file,
or - to read standard input; give exactly one. - is for pipes and is refused
at an interactive terminal (use --body-file there). On success it prints a
receipt naming the team and the message's seq.

Who it is from: with a worker session bound to this worktree, the post is
written by that worker, and the server checks the session is yours and live.
With --as-me it is written by you. With neither, the post is refused (exit 4)
and nothing is sent, because it would be credited to the person running the
agent. Fix that with ` + "`" + `hadron team session start` + "`" + ` (with no worker named it
resumes your worker), with ` + "`" + `--as <worker>` + "`" + ` to bind one, or with --as-me.

Mention a teammate as @worker-name or @handle (a name of several words by its
slug, e.g. @mary-jane); the server finds the mentions in the body.
--reply-to takes the seq of the message you answer, as ` + "`" + `team chat read` + "`" + `
shows it.

Two refusals are deliberate and exit 5: TEAM_CHAT_DUPLICATE (the same message
was just posted) and TEAM_CHAT_POST_RATE_LIMITED (too many posts). The server's
message names the earlier seq or when to try again. The command sends the post
once and never retries; with --json the refusal name is in error.serverCode.

Delivering to terminals (optional). Posting only writes to the chat; nobody's
terminal is typed into. --orca-host-config PATH adds a step after a confirmed
post: it checks this host's configuration first, then delivers any messages
now due to workers on this host, including older unrelated ones. Without the
flag no host configuration or key is read. The host uses its own key, separate
from any worker session, and must already be registered for the same App and
server; this command never registers it. If delivery is held or fails, the post
still stands and the exit stays 0: do not post again, run
` + "`" + `hadron team host run --config <host.yaml>` + "`" + ` instead. Held deliveries are
counted locally but not reported to the server, so no alarm is raised and
nobody is escalated to.

--push, which needs --orca-host-config (without it the command exits 2 before
posting), also sends the post to the terminals of the workers it mentions, and
only them. @all and @team reach no terminal. Nothing is typed over a draft the
worker is writing, and a held worker's read position does not move. The
report gives each mentioned worker one of:
  pushed         delivered and acknowledged
  typed          the text reached the terminal but the acknowledgement did
                 not land (reason ack-pending); it is still due and will not
                 be typed again
  held           delivery was postponed, e.g. the worker has a draft
  unconfirmed    the delivery failed before this worker's turn, so it is not
                 known whether it was typed (reason push-failed)
  not-pushed     not-on-host: the worker is not registered on this host, so
                 push from that worker's host; or no-due-batch: this host has
                 the worker but the server had nothing due for it
With --json the same states are under "push", and each worker also has "acked"
(how many batches' acknowledgements were accepted) and "batch" (the span of the
batch that decided the state, or none). A refusal's error.code is a number; its
name is in error.serverCode. Every state exits 0, so test the
state, not the exit code; a failed push never undoes the post.`,
		Example: `  hadron team chat post "@rufus schema is live, over to you"
  hadron team chat post --reply-to 42 "done"
  hadron team chat post --push --orca-host-config host.yaml "@rufus your turn"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if cmd.Flags().Changed("orca-host-config") && strings.TrimSpace(hostConfig) == "" {
				return exitcode.Newf(exitcode.Usage, "--orca-host-config requires a host configuration file")
			}
			if push && !cmd.Flags().Changed("orca-host-config") {
				return exitcode.Newf(exitcode.Usage, "--push requires --orca-host-config <host.yaml> (the host key that delivers); nothing posted")
			}
			// Exactly one body source: the positional, --body, or --body-file.
			if len(args) == 1 {
				if body != "" || bodyFile != "" {
					return exitcode.Newf(exitcode.Usage, "pass the body either positionally or via --body/--body-file, not both")
				}
				body = args[0]
			} else if body == "" && bodyFile == "" {
				return exitcode.Newf(exitcode.Usage, "no message body — pass it as the argument (or - for stdin), or via --body/--body-file")
			}
			// Refuse a terminal before the binding/scope lookups below, which
			// can cost round trips (#648); ResolveBody refuses the same way.
			if body == "-" && bodyFile == "" {
				if err := chat.RefuseBodyStdinFromTerminal(f.IOStreams.IsInputTerminal()); err != nil {
					return err
				}
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
			// cor:agt:020:03 (hadron-cli#842): a post from a context with no
			// worker binding is REFUSED, never sent as the human. Before #842
			// it went out with no sessionRef, and the server attributed it to
			// the person driving the agent, with nothing telling the agent so.
			// --as-me is the explicit way to post as yourself.
			if !asMe && (b == nil || b.SessionID == "") {
				return noBindingRefusal("this post", true)
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
			text, err := chat.ResolveBody(cmd, body, bodyFile, f.IOStreams.In, f.IOStreams.IsInputTerminal())
			if err != nil {
				return err
			}
			var hostPass postHostDeliveryPass
			if prepare != nil && cmd.Flags().Changed("orca-host-config") {
				server, err := f.Server()
				if err != nil {
					return err
				}
				hostPass, err = prepare(ctx, hostConfig, postHostTarget{AppRef: scope.Ref, Server: server})
				if err != nil {
					return err
				}
				if hostPass == nil {
					return exitcode.Newf(exitcode.Unavailable, "host delivery preflight returned no pass; nothing was posted")
				}
			}
			var pushPass postHostPushPass
			if push {
				var ok bool
				if pushPass, ok = hostPass.(postHostPushPass); !ok {
					return exitcode.Newf(exitcode.Unavailable, "host delivery cannot push; nothing was posted")
				}
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
			// With --push the report joins the ONE receipt document, so the
			// push runs first; the post is already live, so a stderr line
			// goes out before it and a lost receipt is never a silent post.
			var pushed []postPushResult
			if pushPass != nil {
				fmt.Fprintf(f.IOStreams.ErrOut, "note: posted seq %d; pushing to mentioned recipients\n", msg.Seq)
				pushed = reportPostHostPush(ctx, pushPass, f.IOStreams.ErrOut, msg.Seq, pushRecipients(msg.Mentions))
			}
			var receipt any = msg
			if push {
				receipt = struct {
					teamChatMessageDTO
					Push []postPushResult `json:"push"`
				}{msg, append([]postPushResult{}, pushed...)}
			}
			err = output.Write(f.IOStreams, f.JSON, receipt, func(w io.Writer) error {
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
				if _, err := fmt.Fprintf(w, "  app: %s\n", appLabel()); err != nil {
					return err
				}
				for _, r := range pushed {
					reason := ""
					if r.Reason != "" {
						reason = " (" + r.Reason + ")"
						if r.Reason == "ack-pending" {
							reason = fmt.Sprintf(" (ack-pending; acked %d)", r.Acked)
						}
					}
					if _, err := fmt.Fprintf(w, "  push %s: %s%s batch %s\n", r.Recipient, r.State, reason, r.Batch); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				if hostPass != nil {
					fmt.Fprintln(f.IOStreams.ErrOut, "note: the post succeeded but its receipt could not be written; check the chat before posting again")
				}
				return err
			}
			if hostPass != nil && !push {
				if flusher, ok := f.IOStreams.Out.(interface{ Flush() error }); ok {
					if err := flusher.Flush(); err != nil {
						fmt.Fprintln(f.IOStreams.ErrOut, "note: post succeeded but its receipt flush failed; no delivery attempted; check chat before posting again")
						return err
					}
				}
				reportPostHostDelivery(ctx, hostPass, f.IOStreams.ErrOut, msg.Seq)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "message body, or - to read from stdin")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the message body from a file (multi-line safe)")
	cmd.Flags().StringVar(&replyTo, "reply-to", "", "seq of the message this replies to; the server wires a reply edge")
	cmd.Flags().BoolVar(&asMe, "as-me", false, "post as yourself even when a worker session is bound")
	// One body source is enforced in RunE (a cobra flag group can't see the
	// positional form).
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	if prepare != nil {
		cmd.Flags().BoolVar(&push, "push", false, "also deliver the post to the terminals of the workers it mentions (requires --orca-host-config)")
		cmd.Flags().StringVar(&hostConfig, "orca-host-config", "", "attempt this host’s due deliveries after posting (including older unrelated batches)")
	}
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

// teamChatDefaultLimit is the page a bounded `chat read` asks for when --limit
// is unset (cli#967: hidden defaults stay at 20 or less). --all is unaffected:
// it walks every page in TeamChatPageSize chunks.
const teamChatDefaultLimit = 20

func newCmdTeamChatRead(f *cmdutil.Factory) *cobra.Command {
	var tagFlags cmdutil.ChatTagFlags
	var since, before, limit, thread int
	var all bool
	var mentionsMe bool
	var mentions string
	var wait, pollInterval time.Duration
	// returned is how many messages the latest ordinary read returned; the
	// --wait loop (chat_wait.go) polls until it is non-zero.
	var returned int
	cmd := &cobra.Command{
		Use:     "read [--since <seq>] [--before <seq>] [--limit <n>] [--all] [--mentions-me | --mentions <ref>]",
		Aliases: []string{"pull"},
		Short:   "Read one page of team chat, newest page by default",
		Long: `Read messages from the team chat, oldest first within the page. Each line
starts with the [seq] (the message's number), then its time in UTC to the
minute. With no options you get the newest page (20 messages). The team is
chosen as in ` + "`" + `hadron team chat --help` + "`" + `.

Choose the window:
  --since N      the page after seq N (0 = the oldest page)
  --before N     the page just before seq N, still printed oldest first
  --since N --before M   one bounded slice
  --all          every page forward to the end, from seq 0 or from --since;
                 not with --before or --limit
  --limit N      page size, 1 to 200 (default 20)
To go on, pass the nextSince shown on the page as --since, or its prevBefore
as --before. prevBefore is NULL when the page came back empty, and that is the
ONLY signal that you have reached the start of the history. Do not judge "is
there more" from a count: the server's total is scoped to the cursor, not to the
whole chat. With a filter, nextSince advances only past the messages returned
(the server re-scans the skipped ones next time, which is free and never
delivers them again).

Filter the messages:
  --mentions-me      only messages that mention your bound worker
  --mentions <ref>   only messages that mention a worker name or id of this
                     team, or a user handle or id. The server matches the
                     mentions it stored when each message was posted; nothing
                     is re-parsed, and a token may match several workers.
  --thread N         only the direct replies to message N (not N itself, not
                     replies to those replies). An unknown seq is exit 4; a
                     message with no replies gives an empty page. --since,
                     --before, --limit and --all then page through the replies.
  --tagged, --untagged, --team-head ID (repeatable)
                     only messages with a stored team head, with none, or with
                     one of these head IDs, used exactly as given (an ID,
                     not a name; commas are part of the ID; at most 20). Give
                     only one of the three, not with --thread. Where the
                     deployment stamps heads, text output shows their labels
                     and --json adds teamTags: [{headId,label}] ([] for none,
                     null where history lacks or withholds it); key on headId.
                     Refusals are exit 4 (TEAM_CHAT_TEAM_HEAD_INVALID), exit 8
                     (TEAM_CHAT_TEAM_METADATA_FORBIDDEN) or exit 2
                     (BAD_USER_INPUT); never an empty transcript.

What counts as read. An ordinary forward read of your own team's chat, with no
filter, from the point you already reached, records what it returned: it moves
the local watermark that ` + "`" + `team session log` + "`" + ` counts from and, when the binding
records this server, marks your worker read on the server up to the same seq.
Messages are marked only after they are printed, so a read that fails partway
marks nothing. An older server that cannot mark skips the mark; any other mark
failure prints a note on stderr and the read still succeeds. These reads mark
nothing and leave the watermark alone: the newest-page read with no cursor (the
server's count and fetch can race), --before, a window that starts ahead of
what you have read or past the end of the chat, any filter above, --thread, and
a read of another team. After one of them, run
` + "`" + `hadron team chat mark-read --through <seq>` + "`" + ` when you have really read up
to there. To read everything in order, use --since with the last seq you read.

Stale reads. Before printing, the command samples the newest message and
compares. If a forward or newest-page read looks shorter than the chat is (a
stale or incomplete answer from the server) it exits 5: the messages it got are
still shown but nothing is marked read, so retry from your original --since.
A post that arrives after the sample is not a discrepancy, and two equally stale
answers cannot be detected. Filtered and backward windows need not reach the
newest message. If the check itself fails, the read fails.
The read-state line (JSON: readState) shows the newest seq seen, the highest
allocated seq, and your server read position before this read; null means not
available, not zero.

Activity. On the team App your worktree is bound to, a read carries your worker
session without marking anything on the server: it counts as activity and keeps
your worker live, but does not acknowledge messages before they are shown. An
older server that rejects that argument is retried without the session, with a
note.

Waiting. Chat is not pushed to you. --wait <duration> (needs --since, not with
--before or --all) repeats the same read every --poll-interval (default 5s,
minimum 2s) until it returns a message, then prints it and exits 0. If the time
runs out it prints the last empty read and exits 13; nothing is wrong, so wait
again from the same --since. A poll that fails stops the wait with that error.

--json names the author as authorName, and also as author for readers written
for the retired ` + "`" + `hadron chat read` + "`" + `; prefer authorName. authorUserId and
authorWorkerId tell a human post from a worker post (text marks them "(human)"
or "(worker)").`,
		Example: `  hadron team chat read
  hadron team chat read --since 42
  hadron team chat read --all --since 0
  hadron team chat read --mentions-me --json
  hadron team chat read --thread 42 --since 0   # the direct replies to message 42
  hadron team chat read --since 42 --mentions-me --wait 2m   # poll for up to 2 minutes; exit 13 on timeout`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// FLAG VALIDATION FIRST — above the binding read and above
			// `f.GraphQLClient()` (@codex P2). These are wrong whatever your
			// credentials are, and a signed-out caller who typed `--limit 0`
			// was getting AuthRequired: an answer about their session for a
			// mistake in their arguments, which sends them to fix the wrong
			// thing. Same reasoning `alreadyBoundError` records in session.go
			// for the opposite move — a guard that can answer before a client
			// exists must.
			//
			// REFUSED, not clamped: every value here produces an EMPTY page,
			// and an empty page is this command's end-of-history signal, so a
			// permissive parse answers "you have reached the beginning" to a
			// caller with the whole chat still ahead of them. The contract's
			// one guarantee, broken by a typo.
			//
			// `--limit 0` is the sharpest, because the server does NOT reject
			// it: the SDL gives it a meaning (return only `total`), so it
			// succeeds, returns nothing, and looks exactly like the end. Same
			// shape as `asset ls`, which already refuses it.
			switch {
			case mentionsMe && mentions != "":
				return exitcode.Newf(exitcode.Usage, "pass --mentions-me or --mentions <ref>, not both")
			case all && cmd.Flags().Changed("before"):
				return exitcode.Newf(exitcode.Usage, "--all walks forward; use --before for one backward page")
			case all && cmd.Flags().Changed("limit"):
				return exitcode.Newf(exitcode.Usage, "--all walks every page; --limit selects one page")
			case cmd.Flags().Changed("limit") && limit == 0:
				return exitcode.Newf(exitcode.Usage,
					"--limit must be at least 1 (the server reads 0 as \"count only\", which returns an empty page and is indistinguishable from the end of the chat)")
			case cmd.Flags().Changed("thread") && thread < 1:
				return exitcode.Newf(exitcode.Usage, "--thread takes the seq of the message whose replies you want (seqs start at 1)")
			case limit < 0:
				return exitcode.Newf(exitcode.Usage, "--limit must not be negative")
			case limit > TeamChatPageSize:
				// Refused rather than silently capped. The server caps it
				// anyway, so a bounded read would quietly return 200 for a
				// request of 500 — not wrong, but it leaves the caller
				// believing they hold a page they do not.
				return exitcode.Newf(exitcode.Usage,
					"--limit must be at most %d (the server's page cap) — walk with --before to read more", TeamChatPageSize)
			case cmd.Flags().Changed("before") && before < 1:
				// seqs start at 1, so a cursor at or below it can only ever be
				// empty. `--before 1` IS legal and means "nothing older", the
				// honest end-of-history answer rather than a mistake.
				return exitcode.Newf(exitcode.Usage,
					"--before must be at least 1 — seq numbers start at 1, so a cursor below it can only return nothing")
			}
			if err := tagFlags.Validate(cmd); err != nil {
				return err
			}
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// The mutual-exclusion clause moved UP with the paging checks: it
			// is pure flag validation too and had the identical defect. What
			// stays here needs the binding.
			var mentionsRef *string
			switch {
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
			// A cursorless request must OMIT sinceSeq to receive the server's
			// newest page. An explicit --since 0 starts at the oldest page;
			// --all without --since does the same for a full forward walk.
			sinceGiven := cmd.Flags().Changed("since")
			threadGiven := cmd.Flags().Changed("thread")
			tailRead := !sinceGiven && !cmd.Flags().Changed("before") && !all
			pageSize := TeamChatPageSize
			if cmd.Flags().Changed("limit") {
				pageSize = limit
			} else if !all {
				pageSize = teamChatDefaultLimit
			}
			attribution := newChatReadAttribution(ctx, f, appRef, b)
			// cli#1677: the head probe stays before the page (its claim is
			// about messages seen BEFORE the page was asked for), but the
			// allocator-head and read-cursor reads are diagnostics that
			// neither decide staleness nor acknowledge anything, so they
			// overlap the head and page reads. The deferred wait keeps their
			// notes on every exit path; the explicit one below fills state.
			state, err := probeChatHead(ctx, f, client, appRef, attribution)
			if err != nil {
				return err
			}
			meta := startChatReadMetadata(ctx, f, client, appRef, b)
			defer meta.wait(f, nil)
			msgs := []teamChatMessageDTO{}
			lastPageCount := 0
			cursor := since
			for {
				size := pageSize
				var sinceArg *int
				if !tailRead && (sinceGiven || all) {
					sinceArg = &cursor
				}
				var beforeArg *int
				if cmd.Flags().Changed("before") {
					b := before
					beforeArg = &b
				}
				var resp *gen.TeamChatMessagesResponse
				var err error
				if len(tagFlags.Heads()) > 0 {
					resp, err = attribution.tagsMulti(ctx, client, appRef, sinceArg, mentionsRef, &size, beforeArg, tagFlags.Heads(), nil)
				} else if tagFlags.Active() {
					resp, err = attribution.tags(ctx, client, appRef, sinceArg, mentionsRef, &size, beforeArg, tagFlags.Head(), tagFlags.Untagged())
				} else if threadGiven {
					resp, err = attribution.thread(ctx, client, appRef, thread, sinceArg, mentionsRef, &size, beforeArg)
				} else {
					resp, err = attribution.page(ctx, client, appRef, sinceArg, mentionsRef, &size, beforeArg)
				}
				if err != nil {
					if tagFlags.Active() {
						return api.MapErrorKeepingCode(err)
					}
					return api.MapError(err)
				}
				if resp.TeamChatMessages == nil {
					return exitcode.Newf(exitcode.Unavailable, "team chat read returned no page; retry from the original cursor")
				}
				page := resp.TeamChatMessages.Items
				lastPageCount = 0
				pageMsgs := []teamChatMessageDTO{}
				for _, m := range page {
					if m == nil {
						continue
					}
					lastPageCount++
					pageMsgs = append(pageMsgs, teamChatMessageDTOFromFields(m.TeamChatMessageFields))
				}
				// A forward walk stops only on an EMPTY page: a page shorter
				// than asked for may be a server clamp or a mention filter's
				// window, not the end (cli#967). It also stops, without taking
				// the page, when the cursor did not advance (a server ignoring
				// sinceSeq) rather than looping on the same rows.
				if all && lastPageCount > 0 && pageMsgs[len(pageMsgs)-1].Seq <= cursor && len(msgs) > 0 {
					lastPageCount = 0
					break
				}
				msgs = append(msgs, pageMsgs...)
				if !all || lastPageCount == 0 {
					break
				}
				cursor = msgs[len(msgs)-1].Seq
			}
			meta.wait(f, state)
			// A full DEFAULT page cannot show that the chat ended: say so on
			// stderr (cli#967); an explicit --limit is the caller's own page.
			if !all && !cmd.Flags().Changed("limit") && lastPageCount >= pageSize {
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "Showing a full page of %d messages; more may exist (--limit up to %d, --before/--since to walk, --all to read every page).\n", pageSize, TeamChatPageSize)
			}
			returned = len(msgs)
			next := since
			for _, m := range msgs {
				if m.Seq > next {
					next = m.Seq
				}
			}
			highest := 0
			for _, m := range msgs {
				if m.Seq > highest {
					highest = m.Seq
				}
			}
			// A --before bound above the sampled head is also a newest-page
			// request (the portable tail idiom used by older CLI versions).
			beforeGiven := cmd.Flags().Changed("before")
			if state.Head != nil {
				head := *state.Head
				newest := tailRead || (beforeGiven && before > head && head > since)
				state.SuspectedStale = chatPageBehindHead(head, highest, cursor, lastPageCount, pageSize, newest, sinceGiven || all, beforeGiven && !newest, mentionsRef != nil || threadGiven || tagFlags.Active())
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
			// A cursorless newest page is never a read-state checkpoint. Even
			// an apparently complete tail cannot prove coverage: the server
			// counts and fetches separately, and posts can land between them.
			// Explicit --since 0/N is the forward path that can prove a prefix.
			contiguous := !tailRead && ((b == nil) ||
				(b.ChatSeenSeq == nil && since == 0) ||
				(b.ChatSeenSeq != nil && since <= *b.ChatSeenSeq))
			// …and only ever TO a seq the server actually returned, with one
			// addition: an explicit forward read from the very beginning that
			// returns nothing proves the chat empty, which is read-through-0.
			verified, ok := 0, false
			for _, m := range msgs {
				if !ok || m.Seq > verified {
					verified, ok = m.Seq, true
				}
			}
			if !ok && !tailRead && since == 0 {
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
			unfiltered := mentionsRef == nil && !threadGiven && !tagFlags.Active()
			// recordWatermark reports whether this read COUNTS as having read
			// through `verified` — the same evidence decides the server-side
			// mark below (#1353), so the two claims cannot disagree.
			recordWatermark := func() bool {
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
				// --limit with an explicit --since is still a forward prefix.
				// --limit alone is a cursorless tail, excluded by contiguous.
				if state.SuspectedStale || b == nil || b.SessionID == "" || !ok || !unfiltered || !contiguous || cmd.Flags().Changed("before") ||
					!bindingServerMatches(f, b) {
					return false
				}
				if !isBindingsApp(ctx, f, scope.Ref, b.AppID) {
					return false
				}
				// Unconditionally: it writes only when the watermark moves, and
				// its answer — is this worktree still bound to b.SessionID? —
				// gates the server-side mark below.
				return recordChatWatermark(ctx, b.SessionID, verified)
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
			// remained. Backward emptiness remains the signal; forward emptiness is also
			// checked against the independent head because a stale page can forge it.
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
				PrevBefore *int              `json:"prevBefore"`
				ReadState  *chatReadStateDTO `json:"readState"`
			}{msgs, next, prevBefore, state}
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
				cursorLabel := "unavailable or inapplicable"
				if state.ReadCursor != nil {
					cursorLabel = fmt.Sprintf("#%d", *state.ReadCursor)
				}
				headLabel, allocatedLabel := "unavailable", "unavailable"
				if state.Head != nil {
					headLabel = fmt.Sprintf("#%d", *state.Head)
				}
				if state.AllocatedHead != nil {
					allocatedLabel = fmt.Sprintf("#%d", *state.AllocatedHead)
				}
				if _, err := fmt.Fprintf(w, "read state before this read: cursor %s; channel head %s (allocated %s)\n", cursorLabel, headLabel, allocatedLabel); err != nil {
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
					stamp := ""
					if at, parseErr := time.Parse(time.RFC3339, m.At); parseErr == nil {
						stamp = at.UTC().Format("2006-01-02T15:04Z") + " "
					}
					if _, err := fmt.Fprintf(w, "[%d] %s%s%s%s: %s\n", m.Seq, stamp, who, m.authorKind(), reply+teamTagText(m.TeamTags), m.Body); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if state.SuspectedStale {
				return exitcode.Newf(exitcode.Conflict, "suspected stale team-chat read: page ends at #%d, but the independent pre-read channel head was #%d; nothing marked read — retry from the original cursor", highest, *state.Head)
			}
			// The bound worker's SERVER read state (hadron-server#1353) moves
			// only here: after the messages were delivered, and only for a read
			// the watermark rule above counts. The read itself carries no
			// session, so the server never marks a page before this command has
			// shown it — a later page failing, or the render failing, would
			// otherwise bury messages the reader never saw (PR #732, @codex
			// P1). A failed mark errs the loss-safe way: a duplicate nudge.
			finish := func() {
				if recordWatermark() && verified > 0 {
					markDeliveredRead(ctx, f, client, scope.Ref, b, verified)
				}
			}
			// --wait holds this read's output in a buffer, so the render above
			// "succeeded" before anything reached the real stream: it takes the
			// bookkeeping and runs it only once the output was delivered.
			if d, ok := ctx.Value(chatReadDeferKey{}).(*func()); ok {
				*d = finish
				return nil
			}
			finish()
			return nil
		},
	}
	cmd.Flags().IntVar(&since, "since", 0, "one forward page after this seq (0 = oldest page; add --all for every page)")
	// "the newest of those" describes WHICH messages, not what order they
	// print in — the page is still rendered oldest-first like every other read
	// (@copilot: the first wording said "newest first", which reads as
	// ordering and contradicts the paragraph above it).
	cmd.Flags().IntVar(&before, "before", 0, "one page of the messages just before this seq — the newest of those, still printed oldest-first")
	cmd.Flags().IntVar(&limit, "limit", teamChatDefaultLimit, "messages in this page (1 to 200, default 20; --all walks every page)")
	cmd.Flags().BoolVar(&all, "all", false, "walk every page forward (from seq 0, or --since)")
	cmd.Flags().BoolVar(&mentionsMe, "mentions-me", false, "only messages mentioning the bound worker")
	cmd.Flags().StringVar(&mentions, "mentions", "", "only messages mentioning this staff/App member (worker name or id, or user handle)")
	cmd.Flags().IntVar(&thread, "thread", 0, "only the direct replies to this seq (the message itself is not included); an unknown seq is exit 4, a message with no replies is an empty page")
	cmd.Flags().DurationVar(&wait, "wait", 0, "poll until a new message arrives or this long has passed (needs --since); exit 13 on timeout")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", chatWaitDefaultInterval, "with --wait: time between polls (at least 2s)")
	tagFlags.Bind(cmd)
	cmd.RunE = chatWaitRunE(f, cmd.RunE, &wait, &pollInterval, &returned)
	return cmd
}

// Metadata may be null or empty: neither makes a text claim about absence.
func teamTagText(tags []teamTagDTO) string {
	if len(tags) == 0 {
		return ""
	}
	labels := make([]string, 0, len(tags))
	for _, tag := range tags {
		labels = append(labels, output.SafeCell(tag.Label))
	}
	return " (teams: " + strings.Join(labels, ", ") + ")"
}
