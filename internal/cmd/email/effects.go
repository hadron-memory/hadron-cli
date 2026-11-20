package email

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#198 Slice C: the mailbox effects. Each command is exactly one
// request and is never retried. The CLI decides nothing about who may do what
// (ownership, grants, impersonation are the server's); refusals pass through.
// Reply goes to the sender only (the server takes no recipient). The body is read
// from a file or stdin and is never echoed. An idempotency key exists on draft
// save and draft send only, is passed unchanged and is never invented.

// quotePromptID renders an id for a confirmation prompt: made visible (control and escape
// sequences neutralised), never raw. The id SENT is the one the caller gave.
func quotePromptID(id string) string { return output.SafeCell(strconv.Quote(id)) }

// requireConfirmable refuses, before anything is read, an effect that has no --yes
// and no terminal to ask on (the same refusal Confirm would make later).
func requireConfirmable(f *cmdutil.Factory, yes bool) error {
	if !yes && !f.IOStreams.IsInputTerminal() {
		return exitcode.Newf(exitcode.Usage, "refusing to proceed without --yes in non-interactive mode")
	}
	return nil
}

// maxBodyBytes bounds a body read from a file or stdin.
const maxBodyBytes = 1 << 20

// idArg trims and requires a positional id before any request.
func idArg(what, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", exitcode.Newf(exitcode.Usage, "give a nonempty %s (check for an unset variable)", what)
	}
	return v, nil
}

// readBody reads the message body from --body-file (a path, or - for stdin). It
// is required and nonempty, and refuses an interactive stdin.
func readBody(f *cmdutil.Factory, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", exitcode.Newf(exitcode.Usage, "give the message body with --body-file <path> (or - for stdin)")
	}
	var r io.Reader
	if path == "-" {
		if f.IOStreams.IsInputTerminal() {
			return "", exitcode.Newf(exitcode.Usage, "--body-file - reads stdin, which is a terminal here: pipe the body in or name a file")
		}
		r = f.IOStreams.In
	} else {
		file, err := os.Open(path)
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage, "cannot read --body-file")
		}
		defer func() { _ = file.Close() }()
		r = file
	}
	b, err := io.ReadAll(io.LimitReader(r, maxBodyBytes+1))
	if err != nil {
		return "", exitcode.Newf(exitcode.Usage, "cannot read the body")
	}
	if len(b) > maxBodyBytes {
		return "", exitcode.Newf(exitcode.Usage, "the body is larger than %d bytes", maxBodyBytes)
	}
	if !utf8.Valid(b) {
		return "", exitcode.Newf(exitcode.Usage, "the body is not valid UTF-8 (JSON would alter it); convert it first")
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", exitcode.Newf(exitcode.Usage, "the body is empty")
	}
	return string(b), nil
}

// definitiveRefusalCodes are the GraphQL error codes that guarantee the request
// was refused BEFORE anything was sent: authorization, authentication and input
// validation are judged before the provider is called. Any other error can arise
// after the send took effect (a provider error, an internal error, an uncoded
// one), so it does not prove the mail was not sent.
var definitiveRefusalCodes = []string{"FORBIDDEN", "UNAUTHENTICATED", "BAD_USER_INPUT", "GRAPHQL_VALIDATION_FAILED", "GRAPHQL_PARSE_FAILED"}

// graphQLAnswered reports whether the failure carries a GraphQL error list.
func graphQLAnswered(err error) bool {
	var list gqlerror.List
	if errors.As(err, &list) && len(list) > 0 {
		return true
	}
	var one *gqlerror.Error
	if errors.As(err, &one) {
		return true
	}
	var httpErr *graphql.HTTPError
	return errors.As(err, &httpErr) && len(httpErr.Response.Errors) > 0
}

// isDefinitiveSendRefusal reports whether err is made only of errors that
// guarantee nothing was sent. A mixed list is not definitive.
func isDefinitiveSendRefusal(err error) bool {
	return graphQLAnswered(err) && api.OnlyErrorCodes(err, definitiveRefusalCodes...)
}

// bodyFailure maps a failure of a command that SUBMITTED a message body (reply,
// draft save, draft update). The server's error message, an HTTP body and a decode
// error can all quote the body (whole, in part or escaped), and no redaction of free
// text is reliable, so such a command never prints any of them: it prints its own
// words (plus the HTTP status, or the server's error CODE when that is on the fixed
// allowlist) and keeps the exit class. guidance is non-empty when an unclear failure
// may still have had an effect (a send, a draft created): it is appended whenever the
// failure is not a definitive pre-send refusal, and a failure with no GraphQL answer
// is then an unknown outcome (exit 7).
func bodyFailure(err error, guidance string) error {
	const withheldWhy = "because this command submits a message body"
	if !graphQLAnswered(err) {
		var msg string
		var httpErr *graphql.HTTPError
		if errors.As(err, &httpErr) {
			msg = fmt.Sprintf("the server answered HTTP %d with no usable error; its body is withheld %s", httpErr.StatusCode, withheldWhy)
		} else {
			msg = "the server's answer could not be used; its content is withheld " + withheldWhy
		}
		if guidance != "" {
			return exitcode.Newf(exitcode.Unavailable, "%s; %s", msg, guidance)
		}
		return exitcode.Newf(exitcode.FromError(api.MapError(err)), "%s", msg)
	}
	mapped := api.MapError(err)
	// The code is DATA from outside too: it is shown, and carried into the --json
	// envelope's serverCode, only when it is on the fixed allowlist, never as the raw
	// string the server sent.
	code := allowlistedServerCode(api.ServerCodeOf(err))
	msg := "the server answered with an error"
	if code != "" {
		msg += " (" + code + ")"
	}
	msg += "; its message is withheld " + withheldWhy
	if guidance != "" && !isDefinitiveSendRefusal(err) {
		msg += "; " + guidance
	}
	return api.WithCode(exitcode.Newf(exitcode.FromError(mapped), "%s", msg), code)
}

// knownServerCodes is the fixed allowlist of protocol error codes a body-submitting
// command will show or carry. A code is data from outside and can be derived from the
// submitted body, whatever its shape, so anything not on this list is dropped.
var knownServerCodes = map[string]bool{
	"FORBIDDEN": true, "UNAUTHENTICATED": true, "BAD_USER_INPUT": true, "NOT_FOUND": true,
	"GRAPHQL_VALIDATION_FAILED": true, "GRAPHQL_PARSE_FAILED": true,
	"EMAIL_TOOL_ERROR": true, "EMAIL_TOOL_UNREACHABLE": true, "INTERNAL_SERVER_ERROR": true,
	"RATE_LIMITED": true, "SERVICE_UNAVAILABLE": true,
}

// allowlistedServerCode returns code when it is on the allowlist and "" otherwise.
func allowlistedServerCode(code string) string {
	if knownServerCodes[code] {
		return code
	}
	return ""
}

// draftMayExist is the guidance for an unclear draft save: the draft may have been
// created, and a retry without the same idempotency key could make a second one.
const draftMayExist = "outcome unknown: a draft may have been created; check the Drafts folder before trying again (the CLI does not retry; --idempotency-key makes a retry safe)"

func unknownOutcome(where string) string {
	return fmt.Sprintf("outcome unknown: the request may have reached the server and the mail may have been sent; check the %s before trying again (the CLI does not retry)", where)
}

// sendFailure maps a failed send. A definitive pre-send refusal keeps its own words
// and exit code. Any other failure may have followed the send: a GraphQL error
// keeps the server's words and its exit class and adds that the outcome is unknown;
// no answer at all is an unknown outcome outright. Either way the CLI never says
// "failed" for what it cannot know, points at the folder to check, and never retries.
func sendFailure(err error, where string) error {
	if isDefinitiveSendRefusal(err) {
		return api.MapError(err)
	}
	if graphQLAnswered(err) {
		mapped := api.MapError(err)
		return api.WithServerCode(exitcode.Newf(exitcode.FromError(mapped), "%s; %s", mapped.Error(), unknownOutcome(where)), err)
	}
	return exitcode.Newf(exitcode.Unavailable, "%s", unknownOutcome(where))
}

type actionDTO struct {
	ConnectionID string `json:"connectionId"`
	MessageID    string `json:"messageId,omitempty"`
	DraftID      string `json:"draftId,omitempty"`
	Action       string `json:"action"`
	OK           bool   `json:"ok"`
}

// writeConfirmedAction renders a confirmed action, or the unconfirmed refusal.
func writeConfirmedAction(f *cmdutil.Factory, confirmed bool, dto actionDTO, line string, unconfirmed string) error {
	if !confirmed {
		return exitcode.Newf(exitcode.Unavailable, "%s", unconfirmed)
	}
	dto.OK = true
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		_, e := fmt.Fprintln(w, line)
		return e
	})
}

func connFlag(c *cobra.Command, v *string) {
	c.Flags().StringVar(v, "connection", "", "connection id, as listed by hadron email connections (required)")
	_ = c.MarkFlagRequired("connection")
}

func newReply(f *cmdutil.Factory) *cobra.Command {
	var connection, bodyFile string
	var yes bool
	c := &cobra.Command{Use: "reply <message-id> --connection <id> --body-file <path|-> [--yes]", Short: "Reply to a message's sender", Args: cobra.ExactArgs(1),
		Long: `Send a reply to one message. It goes to the message's SENDER only: the server takes
no recipient, so there is no --all and no --to. The body is read from --body-file
(a path, or - for stdin) and is never printed back.

THIS SENDS MAIL. It asks first on a terminal; without one, --yes is required (exit
2 and no request otherwise). Only the mailbox's owner (or an App with a covering
grant) may send; impersonated sessions are refused. A refusal keeps its exit class and
shows the server's error code when it is a recognised one; the server's message is
withheld, because it can quote the body.

One request, never retried. If no answer comes back, the outcome is unknown: the
mail may have been sent, so the CLI says "outcome unknown, check the Sent folder"
and never "failed". A false answer is reported as not confirmed.

Details:
  --json is {connectionId, messageId, action, ok}.`,
		Example: `  hadron email reply <message-id> --connection <id> --body-file reply.txt
  echo "Thanks" | hadron email reply <message-id> --connection <id> --body-file - --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := idArg("--connection id", connection)
			if err != nil {
				return err
			}
			msg, err := idArg("message id", args[0])
			if err != nil {
				return err
			}
			// Refuse a send that could never be confirmed BEFORE reading the body, so
			// piped input is not consumed (or waited on) for a reply that cannot go.
			if err := requireConfirmable(f, yes); err != nil {
				return err
			}
			body, err := readBody(f, bodyFile)
			if err != nil {
				return err
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, "Send a reply to message "+quotePromptID(msg)+"? This sends mail."); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.EmailReply(cmd.Context(), client, conn, msg, body)
			if err != nil {
				return bodyFailure(err, unknownOutcome("Sent folder"))
			}
			return writeConfirmedAction(f, r != nil && r.ExchangeReply, actionDTO{ConnectionID: conn, MessageID: msg, Action: "reply"},
				fmt.Sprintf("reply to message %q sent (server confirmed)", msg),
				"the server did not confirm the reply: outcome unknown, check the Sent folder before trying again")
		}}
	connFlag(c, &connection)
	c.Flags().StringVar(&bodyFile, "body-file", "", "file holding the reply body, or - for stdin (required)")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

func newDraft(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "draft <command>", Short: "Save, update and send mail drafts",
		Long: "Drafts live in the mailbox's own Drafts folder. \"save\" and \"update\" only change the\ndraft; \"send\" sends it. Only the mailbox's owner (or an App with a covering grant)\nmay use them."}
	c.AddCommand(newDraftSave(f), newDraftUpdate(f), newDraftSend(f))
	return c
}

type draftDTO struct {
	DraftID        string  `json:"draftId"`
	ConversationID *string `json:"conversationId"`
}

func newDraftSave(f *cmdutil.Factory) *cobra.Command {
	var connection, bodyFile, replyTo, subject, key string
	var to []string
	c := &cobra.Command{Use: "save --connection <id> (--reply-to <message-id> | --to <addr>... --subject <s>) --body-file <path|-> [--idempotency-key <k>]", Short: "Save a draft", Args: cobra.NoArgs,
		Long: `Save a draft in the mailbox's Drafts folder: a reply-draft (--reply-to a message)
or a fresh one (--to and --subject). Nothing is sent. The body is HTML, sent as
you wrote it, and is read from --body-file (a path, or - for stdin); it is never
printed back. The draft's ID is what "draft update" and "draft send" take.

--idempotency-key is passed to the server unchanged so a retry you make cannot
save two drafts (reuse the same valid key within the server's deduplication window);
the CLI never makes one up and never retries on its own. If the save is unclear (no
usable answer), a draft may have been created: check the Drafts folder before trying
again.

Details:
  --json is {draftId, conversationId} (conversationId is null when the server
  returns none).`,
		Example: `  hadron email draft save --connection <id> --reply-to <message-id> --body-file draft.html
  hadron email draft save --connection <id> --to a@example.com --subject "Hello" --body-file - < draft.html`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, err := idArg("--connection id", connection)
			if err != nil {
				return err
			}
			// Two exclusive forms, judged before anything is read or sent: a
			// reply-draft (--reply-to alone) or a fresh draft (--to and --subject).
			replying := cmd.Flags().Changed("reply-to")
			fresh := cmd.Flags().Changed("to") || cmd.Flags().Changed("subject")
			switch {
			case replying && fresh:
				return exitcode.Newf(exitcode.Usage, "give --reply-to (a reply-draft) or --to and --subject (a fresh draft), not both")
			case replying:
				if strings.TrimSpace(replyTo) == "" {
					return exitcode.Newf(exitcode.Usage, "--reply-to needs a message id")
				}
			case fresh:
				if len(to) == 0 || strings.TrimSpace(subject) == "" {
					return exitcode.Newf(exitcode.Usage, "a fresh draft needs both --to and --subject")
				}
				for _, a := range to {
					if strings.TrimSpace(a) == "" {
						return exitcode.Newf(exitcode.Usage, "a --to address must not be empty")
					}
				}
			default:
				return exitcode.Newf(exitcode.Usage, "give --reply-to <message-id> (a reply-draft) or --to and --subject (a fresh draft)")
			}
			body, err := readBody(f, bodyFile)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.EmailSaveDraft(cmd.Context(), client, conn, body, stringFlagIfSet(cmd, "reply-to", replyTo), stringFlagIfSet(cmd, "subject", subject), optList(cmd, "to", to), stringFlagIfSet(cmd, "idempotency-key", key))
			if err != nil {
				return bodyFailure(err, draftMayExist)
			}
			if r == nil || r.EmailSaveDraft == nil || strings.TrimSpace(r.EmailSaveDraft.DraftId) == "" {
				return exitcode.Newf(exitcode.Unavailable, "server returned no usable draft; %s", draftMayExist)
			}
			d := r.EmailSaveDraft
			// The ids are the server's own opaque values, printed as returned: the draft
			// WAS created, so refusing or altering an id on a guess about its contents
			// would lose the handle and invite a duplicate on retry. What this command
			// never prints back is the body, in anything it renders itself or takes from
			// a server error.
			dto := draftDTO{DraftID: d.DraftId, ConversationID: d.ConversationId}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, e := fmt.Fprintf(w, "draft saved: %q (not sent)\n", dto.DraftID)
				return e
			})
		}}
	connFlag(c, &connection)
	c.Flags().StringVar(&bodyFile, "body-file", "", "file holding the HTML body, or - for stdin (required)")
	c.Flags().StringVar(&replyTo, "reply-to", "", "make this a reply-draft to the message with this id")
	c.Flags().StringArrayVar(&to, "to", nil, "recipient address of a fresh draft (repeatable)")
	c.Flags().StringVar(&subject, "subject", "", "subject of a fresh draft")
	c.Flags().StringVar(&key, "idempotency-key", "", "key passed to the server unchanged, never invented")
	return c
}

func newDraftUpdate(f *cmdutil.Factory) *cobra.Command {
	var connection, bodyFile string
	c := &cobra.Command{Use: "update <draft-id> --connection <id> --body-file <path|->", Short: "Replace a draft's body", Args: cobra.ExactArgs(1),
		Long: `Replace the body of a saved draft (and nothing else: not the recipients or the
subject). The body is HTML, sent as you wrote it, read from --body-file (a path, or
- for stdin) and never printed back. Nothing is sent. The server has no idempotency
key for this, so the command has none: one request, never retried.

Details:
  --json is {connectionId, draftId, action, ok}.`,
		Example: `  hadron email draft update <draft-id> --connection <id> --body-file draft.html`,
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := idArg("--connection id", connection)
			if err != nil {
				return err
			}
			id, err := idArg("draft id", args[0])
			if err != nil {
				return err
			}
			body, err := readBody(f, bodyFile)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.EmailUpdateDraft(cmd.Context(), client, conn, id, body)
			if err != nil {
				return bodyFailure(err, "")
			}
			return writeConfirmedAction(f, r != nil && r.EmailUpdateDraft, actionDTO{ConnectionID: conn, DraftID: id, Action: "draft-update"},
				fmt.Sprintf("draft %q updated (not sent)", id), "the server did not confirm the draft update; read the draft back to see its body")
		}}
	connFlag(c, &connection)
	c.Flags().StringVar(&bodyFile, "body-file", "", "file holding the new HTML body, or - for stdin (required)")
	return c
}

func newDraftSend(f *cmdutil.Factory) *cobra.Command {
	var connection, key string
	var yes bool
	c := &cobra.Command{Use: "send <draft-id> --connection <id> [--idempotency-key <k>] [--yes]", Short: "Send a saved draft", Args: cobra.ExactArgs(1),
		Long: `Send a draft you saved. THIS SENDS MAIL. It asks first on a terminal; without
one, --yes is required (exit 2 and no request otherwise). Only the mailbox's owner
(or an App with a covering grant) may send.

--idempotency-key is passed to the server unchanged so a retry you make cannot send
twice; the CLI never makes one up and never retries on its own. If no answer comes
back, the outcome is unknown: the mail may have been sent, so the CLI says "outcome
unknown, check the Sent folder" and never "failed". A false answer is reported as
not confirmed.

Details:
  --json is {connectionId, draftId, action, ok}.`,
		Example: `  hadron email draft send <draft-id> --connection <id>
  hadron email draft send <draft-id> --connection <id> --idempotency-key send-2026-11-20-1 --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := idArg("--connection id", connection)
			if err != nil {
				return err
			}
			id, err := idArg("draft id", args[0])
			if err != nil {
				return err
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, "Send draft "+quotePromptID(id)+"? This sends mail."); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.EmailSendDraft(cmd.Context(), client, conn, id, stringFlagIfSet(cmd, "idempotency-key", key))
			if err != nil {
				return sendFailure(err, "Sent folder")
			}
			return writeConfirmedAction(f, r != nil && r.EmailSendDraft, actionDTO{ConnectionID: conn, DraftID: id, Action: "draft-send"},
				fmt.Sprintf("draft %q sent (server confirmed)", id),
				"the server did not confirm the send: outcome unknown, check the Sent folder before trying again")
		}}
	connFlag(c, &connection)
	c.Flags().StringVar(&key, "idempotency-key", "", "key passed to the server unchanged, never invented")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

func newMarkRead(f *cmdutil.Factory) *cobra.Command {
	var connection string
	var unread bool
	c := &cobra.Command{Use: "mark-read <message-id> --connection <id> [--unread]", Short: "Mark a message read (or unread)", Args: cobra.ExactArgs(1),
		Long: `Mark one message read; with --unread, mark it unread. Only the mailbox's owner (or
an App with a covering grant) may. One request, never retried.

Details:
  --json is {connectionId, messageId, action, ok}.`,
		Example: `  hadron email mark-read <message-id> --connection <id>
  hadron email mark-read <message-id> --connection <id> --unread`,
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, msg, err := connAndMessage(connection, args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var isRead *bool
			if unread {
				v := false
				isRead = &v
			}
			r, err := gen.EmailMarkRead(cmd.Context(), client, conn, msg, isRead)
			if err != nil {
				return api.MapError(err)
			}
			return writeConfirmedAction(f, r != nil && r.EmailMarkRead, actionDTO{ConnectionID: conn, MessageID: msg, Action: "mark-read"},
				fmt.Sprintf("message %q updated (server confirmed)", msg), "the server did not confirm the change; read the message back to see its state")
		}}
	connFlag(c, &connection)
	c.Flags().BoolVar(&unread, "unread", false, "mark the message unread instead")
	return c
}

func connAndMessage(connection, message string) (string, string, error) {
	conn, err := idArg("--connection id", connection)
	if err != nil {
		return "", "", err
	}
	msg, err := idArg("message id", message)
	return conn, msg, err
}

func newFlag(f *cmdutil.Factory) *cobra.Command {
	var connection, flag string
	c := &cobra.Command{Use: "flag <message-id> --connection <id> [--flag FLAGGED|NONE|COMPLETE]", Short: "Flag, unflag or complete a message", Args: cobra.ExactArgs(1),
		Long: `Set a message's flag: FLAGGED (the default when you give none), NONE to clear it, or
COMPLETE to mark it done. Only the mailbox's owner (or an App with a covering
grant) may. One request, never retried.

Details:
  --json is {connectionId, messageId, action, ok}.`,
		Example: `  hadron email flag <message-id> --connection <id>
  hadron email flag <message-id> --connection <id> --flag COMPLETE`,
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, msg, err := connAndMessage(connection, args[0])
			if err != nil {
				return err
			}
			var fl *gen.EmailFlag
			if cmd.Flags().Changed("flag") {
				v := gen.EmailFlag(strings.ToUpper(strings.TrimSpace(flag)))
				switch v {
				case gen.EmailFlagFlagged, gen.EmailFlagNone, gen.EmailFlagComplete:
				default:
					return exitcode.Newf(exitcode.Usage, "--flag must be FLAGGED, NONE or COMPLETE")
				}
				fl = &v
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.EmailFlagMessage(cmd.Context(), client, conn, msg, fl)
			if err != nil {
				return api.MapError(err)
			}
			return writeConfirmedAction(f, r != nil && r.EmailFlagMessage, actionDTO{ConnectionID: conn, MessageID: msg, Action: "flag"},
				fmt.Sprintf("message %q flag updated (server confirmed)", msg), "the server did not confirm the change; read the message back to see its flag")
		}}
	connFlag(c, &connection)
	c.Flags().StringVar(&flag, "flag", "", "FLAGGED, NONE or COMPLETE (default FLAGGED)")
	return c
}

func newCategorize(f *cmdutil.Factory) *cobra.Command {
	var connection string
	var categories []string
	var clear bool
	c := &cobra.Command{Use: "categorize <message-id> --connection <id> (--category <name>... | --clear)", Short: "Set a message's categories", Args: cobra.ExactArgs(1),
		Long: `Set the categories (colour labels) on one message to exactly the ones you name:
--category is repeatable, and --clear removes them all. Only the mailbox's owner (or
an App with a covering grant) may. One request, never retried.

Details:
  --json is {connectionId, messageId, action, ok}.`,
		Example: `  hadron email categorize <message-id> --connection <id> --category Red --category Blue
  hadron email categorize <message-id> --connection <id> --clear`,
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, msg, err := connAndMessage(connection, args[0])
			if err != nil {
				return err
			}
			if clear == (len(categories) > 0) {
				return exitcode.Newf(exitcode.Usage, "give --category <name> (repeatable) or --clear, not neither and not both")
			}
			list := []string{}
			for _, c := range categories {
				if strings.TrimSpace(c) == "" {
					return exitcode.Newf(exitcode.Usage, "a --category must not be empty")
				}
				list = append(list, c)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.EmailCategorizeMessage(cmd.Context(), client, conn, msg, list)
			if err != nil {
				return api.MapError(err)
			}
			return writeConfirmedAction(f, r != nil && r.EmailCategorizeMessage, actionDTO{ConnectionID: conn, MessageID: msg, Action: "categorize"},
				fmt.Sprintf("message %q categories set (server confirmed)", msg), "the server did not confirm the change; read the message back to see its categories")
		}}
	connFlag(c, &connection)
	c.Flags().StringArrayVar(&categories, "category", nil, "a category to set (repeatable)")
	c.Flags().BoolVar(&clear, "clear", false, "remove every category")
	return c
}

func newDeleteMessage(f *cmdutil.Factory) *cobra.Command {
	var connection string
	var yes bool
	c := &cobra.Command{Use: "delete <message-id> --connection <id> [--yes]", Short: "Delete a message", Args: cobra.ExactArgs(1),
		Long: `Delete one message with the provider's trash semantics (whether it can be recovered
is the provider's rule, not the CLI's). Asks first on a terminal; without one, --yes
is required (exit 2 and no request otherwise). Only the mailbox's owner (or an App
with a covering grant) may. One request, never retried.

Details:
  --json is {connectionId, messageId, action, ok}.`,
		Example: `  hadron email delete <message-id> --connection <id> --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, msg, err := connAndMessage(connection, args[0])
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "message "+quotePromptID(msg)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.EmailDeleteMessage(cmd.Context(), client, conn, msg)
			if err != nil {
				return api.MapError(err)
			}
			return writeConfirmedAction(f, r != nil && r.EmailDeleteMessage, actionDTO{ConnectionID: conn, MessageID: msg, Action: "delete"},
				fmt.Sprintf("message %q deleted (server confirmed)", msg), "the server did not confirm the delete; read the folder to see whether the message is still there")
		}}
	connFlag(c, &connection)
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

func newMove(f *cmdutil.Factory) *cobra.Command {
	var connection, folder string
	c := &cobra.Command{Use: "move <message-id> --connection <id> --folder <folder-id>", Short: "Move a message to another folder", Args: cobra.ExactArgs(1),
		Long: `Move one message to a folder. --folder is the folder ID that "hadron email folders"
prints. Only the mailbox's owner (or an App with a covering grant) may. One request,
never retried.

Details:
  --json is {connectionId, messageId, action, ok}.`,
		Example: `  hadron email move <message-id> --connection <id> --folder <folder-id>`,
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, msg, err := connAndMessage(connection, args[0])
			if err != nil {
				return err
			}
			fid, err := idArg("--folder id", folder)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.EmailMoveMessage(cmd.Context(), client, conn, msg, fid)
			if err != nil {
				return api.MapError(err)
			}
			return writeConfirmedAction(f, r != nil && r.ExchangeMoveMessage, actionDTO{ConnectionID: conn, MessageID: msg, Action: "move"},
				fmt.Sprintf("message %q moved (server confirmed)", msg), "the server did not confirm the move; read the folders to see where the message is")
		}}
	connFlag(c, &connection)
	c.Flags().StringVar(&folder, "folder", "", "folder id, as listed by hadron email folders (required)")
	_ = c.MarkFlagRequired("folder")
	return c
}

// stringFlagIfSet returns a pointer to the flag's value when the caller set it (even
// to an empty string, which is passed unchanged for the server to judge), else nil so
// the argument is omitted.
func stringFlagIfSet(cmd *cobra.Command, name, v string) *string {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	return &v
}

func optList(cmd *cobra.Command, name string, v []string) []string {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	return v
}
