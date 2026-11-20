package email

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#198 Slice B: probe a connection and remove it. One request each and
// never a retry. The CLI decides nothing about who may do either (the owner/admin
// rules, impersonation and grants are the server's); it shows the server's single
// Forbidden as it comes and invents no "not found", "already disconnected" or
// "reconnect" wording the server did not give.

// connectionIDArg trims and requires the positional connection id before any
// request.
func connectionIDArg(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", exitcode.Newf(exitcode.Usage, "give a nonempty connection id (the ID that `hadron email connections` prints; check for an unset variable)")
	}
	return v, nil
}

type testDTO struct {
	ConnectionID string `json:"connectionId"`
	OK           bool   `json:"ok"`
}

func newTest(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "test <connection-id>", Short: "Probe a connection and record the result", Args: cobra.ExactArgs(1),
		Long: `Ask the server to probe one connection (the ID that "hadron email connections"
prints) and report whether it answered. The server records the outcome as the
connection's sync status, so this is not a pure read.

Who may: the connection's owner, or an App with a covering grant (it is gated
like reading messages); an org admin who is not the owner is refused, and so is
an impersonated session. The server's refusal is shown as it comes.

One request, never retried. A true answer prints that the probe passed. A false
answer is the server saying the probe did not pass: it is reported as such and
the command exits 1. The CLI does not say why or what to do about it; reconnecting
an account is "hadron connection connect".

Details:
  --json is {connectionId, ok}. Errors keep the usual exit codes; unknown provider
  errors do not tell you to reconnect.`,
		Example: `  hadron email test <connection-id>
  hadron email test <connection-id> --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := connectionIDArg(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.EmailTestConnection(cmd.Context(), client, id)
			if err != nil {
				return api.MapError(err)
			}
			// An absent or null answer is a missing result, never a definite failed
			// probe: only an explicit false is "did not pass".
			if r == nil || r.TestExchangeConnection == nil {
				return exitcode.Newf(exitcode.Unavailable, "server returned no probe result; the connection's state is unknown")
			}
			dto := testDTO{ConnectionID: id, OK: *r.TestExchangeConnection}
			if e := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.OK {
					_, e := fmt.Fprintf(w, "connection %q: the server's probe passed\n", id)
					return e
				}
				_, e := fmt.Fprintf(w, "connection %q: the server's probe did not pass\n", id)
				return e
			}); e != nil {
				return e
			}
			if !dto.OK {
				return exitcode.Silent(exitcode.Error)
			}
			return nil
		}}
	return c
}

type disconnectDTO struct {
	ConnectionID string `json:"connectionId"`
	Disconnected bool   `json:"disconnected"`
}

func newDisconnect(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	c := &cobra.Command{Use: "disconnect <connection-id> [--yes]", Short: "Remove a connection (org admin)", Args: cobra.ExactArgs(1),
		Long: `Delete one connection record (the ID that "hadron email connections" prints).
This applies to ANY provider's connection, not only mail: it is what the portal's
disconnect does. It cannot be undone here; to connect again use "hadron
connection connect".

Who may: an ADMIN of the organization that owns the connection. Being the
mailbox's owner or holding a grant does not open it, and an impersonated session
is refused. The server answers a refusal with one Forbidden for every reason
(missing, already deleted or not an admin); it is shown as it comes and never
turned into "not found" or "already disconnected".

"Disconnected" means Hadron's record is deleted. Removing the access the provider
granted is the server's best-effort step, and this command cannot tell you it
happened.

Asks first on a terminal; without one, --yes is required (exit 2 and no request
otherwise). One request, never retried: if the answer is lost, list the
connections to see whether it still exists. A false answer is not a success: it
is reported as unconfirmed (exit 7).

Details:
  --json is {connectionId, disconnected}. Errors keep the usual exit codes;
  unknown provider errors do not tell you to reconnect.`,
		Example: `  hadron email disconnect <connection-id>
  hadron email disconnect <connection-id> --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := connectionIDArg(args[0])
			if err != nil {
				return err
			}
			// The prompt shows the id made visible (control and escape sequences
			// neutralised); the id SENT is unchanged.
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "connection "+output.SafeCell(strconv.Quote(id))); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.EmailDisconnect(cmd.Context(), client, id)
			if err != nil {
				return api.MapError(err)
			}
			if r == nil || r.DeleteExchangeConnection == nil || !*r.DeleteExchangeConnection {
				return exitcode.Newf(exitcode.Unavailable, "the server did not confirm the disconnect; list the connections to see whether it still exists")
			}
			return output.Write(f.IOStreams, f.JSON, disconnectDTO{ConnectionID: id, Disconnected: true}, func(w io.Writer) error {
				_, e := fmt.Fprintf(w, "disconnected %q (Hadron's record deleted, server confirmed)\n", id)
				return e
			})
		}}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}
