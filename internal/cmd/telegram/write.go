package telegram

// telegram connect / disconnect (cli#2952, T2 + §10, over hadron-server#4824).
// The bot token is a write-only secret: it comes from a file or stdin, and
// appears in no argument, output, JSON field, error text or help example. The
// server owns every rule (org ADMIN/OWNER, one live connection per App and per
// bot, soft delete): this file builds one request and passes the answer
// through (cor:api:240:02, cor:int:030:01).

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type disconnectDTO struct {
	ConnectionID string `json:"connectionId"`
	Disconnected bool   `json:"disconnected"`
}

// redactConnectError is the final error of a failed connect. The server's text is
// untrusted everywhere it can reach the operator, so the finished error is cut
// from its raw chain and every rendered string (message, server code, internal
// fault detail) has the submitted token removed, in every spelling the shared
// redactor knows. The command's own recovery text is added BEFORE that, so an
// echo never costs the operator the unknown-outcome notice; the exit class and
// a clean server code survive.
//
// A TELEGRAM_TOOL_* refusal is a definitive answer — the server says it refused
// to register the bot — so it is never reworded as an unknown outcome and points
// at nothing: there is no registration to look for. Everything else in the
// exit-1/7 classes (a lost answer, an unmapped code, a post-commit crash) keeps
// the unknown-outcome notice and the status pointer.
func redactConnectError(raw error, secrets []string, where string) error {
	mapped := api.MapError(raw)
	// The Telegram error code rides its own extension (not extensions.code) on
	// TELEGRAM_TOOL_* refusals; surface it next to the server's message so the
	// operator sees e.g. invalid_token, as the help and agentic-usage promise.
	if code := api.ServerCodeOf(raw); code != "" {
		if tg, ok := api.ErrorCodeExtension(raw, code, "telegramErrorCode"); ok && tg != "" {
			mapped = exitcode.Newf(exitcode.FromError(mapped), "%s (Telegram error code: %s)", mapped, tg)
		}
	}
	base := mapped
	if class := exitcode.FromError(mapped); class == exitcode.Error || class == exitcode.Unavailable {
		if telegramToolRefusedAt(raw) {
			base = exitcode.Newf(class, "connect was refused: %s", mapped)
		} else {
			// The server's text sits in the middle and the suggested command is the
			// LAST thing in the message, so copying the end of the line is the command.
			base = exitcode.Newf(class, "%s: %s. %s", "connect outcome unknown (sent once, not retried)", mapped, where)
		}
	}
	return api.Detached(api.WithServerCode(base, raw), func(s string) string { return api.RedactSecretText(s, secrets) })
}

// telegramToolRefusedAt reports whether raw is the server's definitive refusal
// of the connect mutation itself: a TELEGRAM_TOOL_* code on
// createTelegramConnection, answered on this HTTP response (a 5xx envelope
// after a committed write is not a refusal — cli#1495), before or at the field
// (an error below it happened after the write committed — cli#398). Unmapped
// and INTERNAL codes deliberately fail this test: what the CLI cannot name, it
// treats as an unknown outcome.
func telegramToolRefusedAt(raw error) bool {
	return strings.HasPrefix(api.ServerCodeOf(raw), "TELEGRAM_TOOL_") &&
		api.IsDefiniteRefusalAt(raw, "createTelegramConnection") &&
		!api.FromNon200Envelope(raw)
}

// hasControlOrFormatCharacter reports whether s carries a Unicode control or
// format character (an id with ESC/CSI or a newline would reach the terminal
// raw once it is no longer Go-quoted), or a line/paragraph separator
// (U+2028/U+2029: Zl/Zp are not Cc, but some terminals and clipboards treat
// them as line breaks — the same line-forgery the guard exists to stop). Shell
// quoting is a separate step.
func hasControlOrFormatCharacter(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
	}) >= 0
}

// getConnectionSuggestion is the closing sentence of a disconnect message: how to
// check whether the connection still exists. The command sits ALONE on the last
// line (an indented line of its own, so copying a whole line is the command) and
// is runnable as printed: shell-quoted, with the -- terminator (a leading
// dash is a flag to cobra), and no outer prose quotes (single quotes are literal
// inside double quotes, so $( ) would expand again when pasted). An id with
// control characters is never named; the runnable listing command stands in.
func getConnectionSuggestion(id string) string {
	if hasControlOrFormatCharacter(id) {
		return "The listing is paged, so follow its --offset hint before concluding it is gone. Check whether it still exists by listing connections with:\n  hadron telegram status"
	}
	return "Check whether it still exists with:\n  hadron telegram get -- " + cmdutil.ShellQuote(id)
}

// readToken reads one token from its file flag. "-" is stdin and is refused on
// a terminal (the token would be typed in the clear); the file reader itself
// never puts the path or the material in an error.
func readToken(f *cmdutil.Factory, source, flag, label, what string) (string, error) {
	if source == "-" && f.IOStreams.IsInputTerminal() {
		return "", exitcode.Newf(exitcode.Usage, "--%s - reads stdin, which is a terminal: pipe the token in, or name a file. Nothing was sent", flag)
	}
	return cmdutil.ReadSecretInput(f.IOStreams, source, flag, label, what)
}

func newConnect(f *cmdutil.Factory) *cobra.Command {
	var app, botFile string
	c := &cobra.Command{
		Use:   "connect --app <ref> --bot-token-file <path|->",
		Short: "Register a Telegram bot with an App (org ADMIN)",
		Long: `Register a Telegram bot with one App, from the bot token the BotFather
issued. The connection is App-owned: no --org is taken, the organization comes
from the App, and the server allows one active connection per App and one per
bot. The server validates the token with Telegram and stores it where this CLI
and every read of the connection cannot see them; no command returns it.

The token comes from a file, never from the command line, so it stays out of
your shell history and the process list: --bot-token-file takes a file path or
- for stdin. A missing flag is refused before anything is sent, and so is - on
a terminal. A trailing newline is dropped.

--app takes an ID or URN. The server decides who may connect, and one
Forbidden refusal covers a caller who is not an organization ADMIN or OWNER, an
unknown or ambiguous App, and a user-owned App alike: they answer identically,
so this CLI does not guess which one applied and renders the refusal as
received. It also passes through the server's refusals for an App that already
has a Telegram connection, a bot that is already connected, and a Telegram
tool that is not configured, unreachable or failing (with the Telegram error
code, e.g. an invalid token). This command does not check any of them.

One request is sent and never retried. If its answer is lost, the outcome is
unknown: the bot may be registered even though nothing was confirmed — a lost
registration answer can even leave a bot registered on the Telegram side that
Hadron cannot see. "hadron telegram status" lists the
connections you can see, one page at a time, so follow its --offset hint
before deciding the bot is not connected.
On success it shows the connection the way "hadron telegram get" does (never a
token). Connecting a bot that was disconnected registers it again.`,
		Example: `  hadron telegram connect --app hrn:app:acme.com:assistant --bot-token-file ./bot.token
  printf %s "$BOT_TOKEN" | hadron telegram connect --app acme.com:assistant --bot-token-file - --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			appRef := strings.TrimSpace(app)
			if appRef == "" {
				return exitcode.Newf(exitcode.Usage, "--app is required: name the App (ID or URN)")
			}
			canonicalAppRef, err := cmdutil.CanonicalAppRef("--app", appRef)
			if err != nil {
				return err
			}
			botSource := strings.TrimSpace(botFile)
			if botSource == "" {
				return exitcode.Newf(exitcode.Usage, "--bot-token-file is required: it takes a file path or - for stdin. Nothing was sent")
			}
			bot, err := readToken(f, botSource, "bot-token-file", "Telegram bot token", "bot token")
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			secrets := []string{bot}
			// The recovery pointer names no flag of this command: telegram status
			// narrows by --org, which connect does not take, so the plain listing is
			// the runnable suggestion. (Unlike slack connect there is no --org value
			// that could be carried into the command and echo a mistaken token; the
			// submitted token itself is still redacted from every error below.)
			// Prose first, the command last: nothing may follow it on the line.
			where := "The listing is paged, so follow its --offset hint before deciding the bot is not connected. Check with:\n  hadron telegram status"
			resp, err := gen.CreateTelegramConnection(cmd.Context(), client, canonicalAppRef, bot)
			if err != nil {
				return redactConnectError(err, secrets, where)
			}
			if resp == nil || resp.CreateTelegramConnection == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server returned no connection. %s", where)
			}
			v := resp.CreateTelegramConnection
			// A success answer is the server's text too: a field that repeats the
			// submitted token is never rendered, and nothing from it (not even the
			// id) goes into the message.
			for _, field := range []string{v.Id, v.OrganizationId, v.AppId, v.Provider, v.Status, v.DisplayName, v.BotId, v.BotUsername, v.CreatedAt, v.UpdatedAt} {
				if api.ContainsSecret(field, secrets) {
					return exitcode.Newf(exitcode.Unavailable, "the server's answer repeated the submitted token and was withheld; the connection may exist. %s", where)
				}
			}
			// Success needs a usable id: without one nothing is confirmed, and no
			// "get" command can be named.
			if strings.TrimSpace(v.Id) == "" {
				return exitcode.Newf(exitcode.Unavailable, "the server's answer carried no connection id, so nothing is confirmed; the connection may exist. %s", where)
			}
			dto, err := toDTO(v.Id, v.OrganizationId, v.AppId, v.Provider, v.Status, v.DisplayName, v.BotId, v.BotUsername, v.CreatedAt, v.UpdatedAt)
			if err != nil {
				// "telegram get" and "telegram status" refuse the same unknown status, so
				// they would loop: the one command that always works is disconnect. An
				// id with control characters is never printed (the terminal would act on it).
				if hasControlOrFormatCharacter(v.Id) {
					// No command is suggested: get/status refuse this same status, and the
					// id cannot be named safely. Upgrade, or an administrator removes it.
					return exitcode.Newf(exitcode.Unavailable, "the server answered with a connection id this CLI will not print (it contains control characters) and a status it does not recognize (a newer server?); upgrade hadron, or ask the server's administrator to remove that registration")
				}
				return exitcode.Newf(exitcode.Unavailable, "the server answered with connection id %s and a status this CLI does not recognize (a newer server?), so it cannot be read back here: upgrade hadron, or remove the registration with: hadron telegram disconnect -- %s", cmdutil.ShellQuote(v.Id), cmdutil.ShellQuote(v.Id))
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				// Two literal formats, never a variable format string (go vet's
				// printf check; a variable would misrender any future % in it).
				name := output.SafeCell(strconv.Quote(dto.DisplayName))
				if dto.Status == "ERROR" {
					// ERROR is a recorded failure (see "hadron telegram --help"), not success.
					if _, e := fmt.Fprintf(w, "! registered Telegram bot %s, but the server has recorded a failure (status ERROR; see \"hadron telegram --help\")\n", name); e != nil {
						return e
					}
				} else if _, e := fmt.Fprintf(w, "✓ connected Telegram bot %s\n", name); e != nil {
					return e
				}
				return writeConnection(w, dto)
			})
		},
	}
	c.Flags().StringVar(&app, "app", "", "the App the bot answers for (ID or URN; required)")
	c.Flags().StringVar(&botFile, "bot-token-file", "", "file holding the Telegram bot token, or - for stdin (required; never the token itself)")
	return c
}

func newDisconnect(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "disconnect <connection-id> [--yes]",
		Short: "Remove a Telegram bot connection (org ADMIN)",
		Long: `Remove one Telegram bot connection, by its ID (from "hadron telegram status").
The server marks the connection deleted and tries to remove the stored bot
token and the Telegram-side registration on its side (best effort; it does not
say how far it got). Connecting the bot again with "hadron telegram connect"
registers it anew.

On a terminal this asks first; declining the prompt exits 6 (Cancelled) and
nothing is sent. Without one, --yes is required and nothing is
sent without it. The server decides who may disconnect (organization ADMIN or
OWNER); this command does not check it. An unknown ID, one in another
organization, and one visible only to a revoked member are refused the same
way (exit 4), so a refusal does not tell you whether the connection exists for
someone else. One request is sent and never retried: if its answer is lost,
read the connection with "hadron telegram get" before trying again.`,
		Example: `  hadron telegram disconnect 01a1... --yes
  hadron telegram disconnect 01a1... --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty connection id")
			}
			// Not ConfirmDeletion: its "cannot be undone" is false here. The server
			// soft-deletes, and "hadron telegram connect" registers the bot again.
			if err := cmdutil.Confirm(f.IOStreams, yes, "Disconnect Telegram connection "+strconv.Quote(id)+"? The server marks it deleted and tries to remove the stored bot token and the Telegram-side registration (best effort, not reported); connecting the bot again registers it anew"); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.DeleteTelegramConnection(cmd.Context(), client, id)
			if err != nil {
				mapped := api.MapErrorKeepingCode(err)
				if code := exitcode.FromError(mapped); code == exitcode.Error || code == exitcode.Unavailable {
					mapped = api.WithServerCode(exitcode.Newf(code, "disconnect outcome unknown (sent once, not retried): %s. %s", mapped, getConnectionSuggestion(id)), err)
				}
				return mapped
			}
			if resp == nil || !resp.DeleteTelegramConnection {
				return exitcode.Newf(exitcode.Unavailable, "the server did not confirm the disconnect. %s", getConnectionSuggestion(id))
			}
			dto := disconnectDTO{ConnectionID: id, Disconnected: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, e := fmt.Fprintf(w, "disconnected Telegram connection %s (server confirmed)\n", output.SafeCell(strconv.Quote(id)))
				return e
			})
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "do not ask first (required without a terminal)")
	return c
}
