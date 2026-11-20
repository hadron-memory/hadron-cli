package slack

// slack connect / disconnect (cli#206, T2 + §10). The two Slack tokens are
// write-only secrets: they come from a file or stdin, one flag per token, and
// appear in no argument, output, JSON field, error text or help example. The
// server owns every rule (org ADMIN/OWNER, App-in-org, one live connection per
// workspace, soft delete): this file builds one request and passes the answer
// through (cor:api:240:01/:02, cor:int:030:01).

import (
	"fmt"
	"io"
	"strconv"
	"strings"

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

// guardedWriteError is the final error of a failed connect. The server's text is
// untrusted everywhere it can reach the operator, so the finished error is cut
// from its raw chain and every rendered string (message, server code, internal
// fault detail) has both submitted tokens removed, in every spelling the shared
// redactor knows. The command's own recovery text is added BEFORE that, so an
// echo never costs the operator the unknown-outcome notice; the exit class and
// a clean server code survive.
func guardedWriteError(raw error, secrets []string, recovery string) error {
	mapped := api.MapError(raw)
	base := mapped
	if class := exitcode.FromError(mapped); class == exitcode.Error || class == exitcode.Unavailable {
		base = exitcode.Newf(class, "%s: %s", recovery, mapped)
	}
	return api.Detached(api.WithServerCode(base, raw), func(s string) string { return api.RedactSecretText(s, secrets) })
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
	var org, app, botFile, appFile string
	c := &cobra.Command{
		Use:   "connect --org <ref> --app <ref> --bot-token-file <path|-> --app-token-file <path|->",
		Short: "Register a Slack workspace with an organization (org ADMIN)",
		Long: `Register a Slack workspace with an organization and one of its Apps, from the
workspace's two Slack tokens: the bot token (xoxb-...) and the app-level token
(xapp-...). The server validates both with Slack and stores them where this
CLI and every read of the connection cannot see them; no command returns them.

Each token comes from a file, never from the command line, so it stays out of
your shell history and the process list: --bot-token-file and --app-token-file
each take a file path or - for stdin. Only one of the two may be - (one stdin
holds one token); both being - is refused before anything is sent, as is a
missing flag, and so is - on a terminal. A trailing newline is dropped.

--org and --app take an ID or URN. The App must belong to the organization. The
server decides who may connect (organization ADMIN or OWNER), and refuses an
unknown organization or App, an App outside the organization, and a workspace
that is already connected (a registration that shows ERROR still counts: disconnect
it first); those refusals are passed through. This command does
not check any of them.

One request is sent and never retried. If its answer is lost, the outcome is
unknown: "hadron slack status --org <ref>" lists the organization's connections
one page at a time, so follow its --offset hint before deciding the workspace is
not connected.
On success it shows the connection the way "hadron slack get" does (never a
token). Re-connecting a workspace that was disconnected registers it again.`,
		Example: `  hadron slack connect --org hrn:org:acme.com --app hrn:app:acme.com:assistant \
    --bot-token-file ./bot.token --app-token-file ./app.token
  printf %s "$BOT_TOKEN" | hadron slack connect --org acme.com --app acme.com:assistant \
    --bot-token-file - --app-token-file ./app.token --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			orgRef := strings.TrimSpace(org)
			if orgRef == "" {
				return exitcode.Newf(exitcode.Usage, "--org is required: name the organization (ID or URN)")
			}
			if strings.TrimSpace(app) == "" {
				return exitcode.Newf(exitcode.Usage, "--app is required: name the App (ID or URN)")
			}
			appRef, err := cmdutil.CanonicalAppRef("--app", app)
			if err != nil {
				return err
			}
			if strings.TrimSpace(botFile) == "" || strings.TrimSpace(appFile) == "" {
				return exitcode.Newf(exitcode.Usage, "--bot-token-file and --app-token-file are both required: each takes a file path or - for stdin. Nothing was sent")
			}
			if botFile == "-" && appFile == "-" {
				return exitcode.Newf(exitcode.Usage, "only one of --bot-token-file and --app-token-file may be - (stdin holds one token): put the other in a file. Nothing was sent")
			}
			bot, err := readToken(f, botFile, "bot-token-file", "Slack bot token", "bot token")
			if err != nil {
				return err
			}
			appTok, err := readToken(f, appFile, "app-token-file", "Slack app-level token", "app-level token")
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			secrets := []string{bot, appTok}
			where := fmt.Sprintf("run \"hadron slack status --org %s\": it lists connections one page at a time, so follow its --offset hint before deciding the workspace is not connected", orgRef)
			resp, err := gen.CreateSlackConnection(cmd.Context(), client, orgRef, appRef, bot, appTok)
			if err != nil {
				return guardedWriteError(err, secrets, "connect outcome unknown (sent once, not retried); "+where)
			}
			if resp == nil || resp.CreateSlackConnection == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server returned no connection; %s", where)
			}
			v := resp.CreateSlackConnection
			// A success answer is the server's text too: a field that repeats a
			// submitted token is never rendered, and nothing from it (not even the
			// id) goes into the message.
			for _, field := range []string{v.Id, v.OrganizationId, v.DefaultAppId, v.Provider, v.Status, v.TeamId, v.TeamName, v.BotId, v.BotUserId, v.CreatedAt, v.UpdatedAt} {
				if api.HoldsSecret(field, secrets) {
					return exitcode.Newf(exitcode.Unavailable, "the server's answer repeated a submitted token and was withheld; the connection may exist: %s", where)
				}
			}
			dto, err := toDTO(v.Id, v.OrganizationId, v.DefaultAppId, v.Provider, v.Status, v.TeamId, v.TeamName, v.BotId, v.BotUserId, v.CreatedAt, v.UpdatedAt)
			if err != nil {
				return exitcode.Newf(exitcode.Unavailable, "the connection was created (id %s) but the server returned an unknown status; read it with \"hadron slack get\"", strconv.Quote(v.Id))
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, e := fmt.Fprintf(w, "✓ connected Slack workspace %s\n", output.SafeCell(strconv.Quote(dto.TeamName))); e != nil {
					return e
				}
				return writeConnection(w, dto)
			})
		},
	}
	c.Flags().StringVar(&org, "org", "", "organization to register the workspace with (ID or URN; required)")
	c.Flags().StringVar(&app, "app", "", "the organization's App that answers in the workspace (ID or URN; required)")
	c.Flags().StringVar(&botFile, "bot-token-file", "", "file holding the Slack bot token, or - for stdin (required; never the token itself)")
	c.Flags().StringVar(&appFile, "app-token-file", "", "file holding the Slack app-level token, or - for stdin (required; never the token itself)")
	return c
}

func newDisconnect(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "disconnect <connection-id> [--yes]",
		Short: "Remove a Slack workspace connection (org ADMIN)",
		Long: `Remove one Slack workspace connection, by its ID (from "hadron slack status").
The server marks the connection deleted and tries to remove the stored Slack
tokens on its side (best effort; it does not say how far it got). Connecting
the workspace again with "hadron slack connect" registers it anew.

On a terminal this asks first; without one, --yes is required and nothing is
sent without it. The server decides who may disconnect (organization ADMIN or
OWNER); this command does not check it. An unknown ID and one in another
organization are refused the same way (exit 4), so a refusal does not tell you
whether the connection exists for someone else. One request is sent and never
retried: if its answer is lost, read the connection with "hadron slack get"
before trying again.`,
		Example: `  hadron slack disconnect 01a1... --yes
  hadron slack disconnect 01a1... --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty connection id")
			}
			// Not ConfirmDeletion: its "cannot be undone" is false here. The server
			// soft-deletes, and "hadron slack connect" registers the workspace again.
			if err := cmdutil.Confirm(f.IOStreams, yes, "Disconnect Slack connection "+strconv.Quote(id)+"? The server marks it deleted and tries to remove the stored Slack tokens (best effort, not reported); connecting the workspace again registers it anew"); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.DeleteSlackConnection(cmd.Context(), client, id)
			if err != nil {
				mapped := api.MapErrorKeepingCode(err)
				if code := exitcode.FromError(mapped); code == exitcode.Error || code == exitcode.Unavailable {
					mapped = api.WithServerCode(exitcode.Newf(code, "disconnect outcome unknown (sent once, not retried); \"hadron slack get %s\" shows whether the connection still exists: %s", strconv.Quote(id), mapped), err)
				}
				return mapped
			}
			if resp == nil || !resp.DeleteSlackConnection {
				return exitcode.Newf(exitcode.Unavailable, "the server did not confirm the disconnect; \"hadron slack get %s\" shows whether the connection still exists", strconv.Quote(id))
			}
			dto := disconnectDTO{ConnectionID: id, Disconnected: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, e := fmt.Fprintf(w, "disconnected Slack connection %s (server confirmed)\n", output.SafeCell(strconv.Quote(id)))
				return e
			})
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "do not ask first (required without a terminal)")
	return c
}
