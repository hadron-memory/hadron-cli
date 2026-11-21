package secret

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#2680: `secret client revoke`. A read client is one grant (cor:sec:060), so
// revoking the client is revokeGrant on its grantId. The server owns who may
// revoke and every refusal; the CLI adds no gate of its own (cor:api:240:01).

// revokeDTO is the --json shape of `client revoke`: the grant as the server
// returned it after the revoke.
type revokeDTO struct {
	ClientID  string  `json:"clientId"`
	GrantID   string  `json:"grantId"`
	State     string  `json:"state"`
	RevokedAt *string `json:"revokedAt"`
	Allows    bool    `json:"allows"`
}

// findClient pages the caller's read clients until it finds clientID. The id is
// matched exactly as given. A client the caller neither issued nor holds never
// lists, so it is not found here even where the server would let a manager revoke
// its grant.
func findClient(cmd *cobra.Command, f *cmdutil.Factory, clientID string) (*clientDTO, error) {
	client, err := f.GraphQLClient()
	if err != nil {
		return nil, err
	}
	for off, pages := 0, 0; ; pages++ {
		l, o := clientsPageSize, off
		page, err := fetchClients(cmd.Context(), client, &l, &o)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return nil, nil
		}
		if pages >= maxClientPages {
			return nil, exitcode.Newf(exitcode.Error, "stopped looking for the client after %d pages", maxClientPages)
		}
		for i := range page {
			if page[i].ClientID == clientID {
				return &page[i], nil
			}
		}
		off += len(page)
	}
}

func newCmdClientRevoke(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "revoke <client-id> [--yes]",
		Short: "Revoke a read client now",
		Long: `Revoke a read client: its grant ends now, unsigned, and the holder can no longer
read the secret. This cannot be undone: a new client needs a new issue and a
person's signature.

<client-id> is the id 'hadron secret client list' prints. The command looks it
up among the clients you issued or hold, then sends one revoke on its grant. A
client you do not see there (unknown, or someone else's) is a not-found (exit 4)
and nothing is sent.

You act as yourself: this never sends a worker session, even from a bound
worktree. The server decides who may revoke: the person who issued it (while still
a member of the App's Org) or whoever manages the App. A refusal is reported as it
came (exit 8). A grant that had already ended (revoked, withdrawn, expired, spent,
its holder gone) is returned as it is and is not changed: the command says it was
already ended and exits 0.

On a terminal it asks first; off one it needs --yes and sends nothing without it.

Exit codes: 0 revoked, or already ended; 2 usage; 3 not signed in; 4 client not
found; 7 the answer was lost or did not confirm a revoke: the revoke is not
resent, so run 'hadron secret client list' (the grant id is in the error) to see
whether the client ended; 8 not permitted.`,
		Example: `  hadron secret client revoke cl_123 --yes
  hadron secret client revoke cl_123 --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if strings.TrimSpace(id) == "" {
				return exitcode.Newf(exitcode.Usage, "client id is required")
			}
			// Nothing is sent, not even the read, when the answer cannot be asked for.
			if !yes && !f.IOStreams.IsInputTerminal() {
				return cmdutil.Confirm(f.IOStreams, false, "")
			}
			c, err := findClient(cmd, f, id)
			if err != nil {
				return err
			}
			if c == nil {
				return exitcode.Newf(exitcode.NotFound, "no read client %s among the clients you issued or hold (a client someone else issued does not list here); see `hadron secret client list`", output.SafeCell(fmt.Sprintf("%q", id)))
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Revoke read client %s for secret %s? This cannot be undone.", output.SafeCell(c.ClientID), output.SafeCell(c.SecretName))); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RevokeSecretReadClientGrant(cmd.Context(), client, c.GrantID)
			if err != nil {
				return revokeError(err, c.GrantID)
			}
			if resp == nil || resp.RevokeGrant == nil || resp.RevokeGrant.Id == "" {
				return exitcode.Newf(exitcode.Unavailable, "revoke not confirmed: the server returned no grant; the revoke is not resent. Run `hadron secret client list` (grant %s) to see whether it ended", output.SafeCell(c.GrantID))
			}
			g := resp.RevokeGrant
			state := string(g.State)
			if state == string(gen.GrantStateActive) || state == string(gen.GrantStatePending) {
				return exitcode.Newf(exitcode.Unavailable, "revoke not confirmed: the server returned grant %s still %s; the revoke is not resent. Run `hadron secret client list` to check", output.SafeCell(c.GrantID), state)
			}
			d := revokeDTO{ClientID: c.ClientID, GrantID: c.GrantID, State: state, RevokedAt: g.RevokedAt, Allows: g.Allows}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				if state != string(gen.GrantStateRevoked) {
					_, e := fmt.Fprintf(w, "read client %s was already ended (%s); nothing changed\n", output.SafeCell(d.ClientID), output.SafeCell(state))
					return e
				}
				_, e := fmt.Fprintf(w, "read client %s revoked: grant %s is %s (revokedAt %s)\n", output.SafeCell(d.ClientID), output.SafeCell(d.GrantID), output.SafeCell(state), output.SafeCell(output.Dash(d.RevokedAt)))
				return e
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "revoke without asking (required off a terminal)")
	return cmd
}

// revokeError maps a failed revoke. A lost answer (the 7 class) is an unknown
// outcome: the revoke may have ended the grant, so it says so, names the grant and
// `client list`, and the call is never resent.
func revokeError(err error, grantID string) error {
	mapped := api.MapErrorKeepingCode(err)
	if exitcode.FromError(mapped) == exitcode.Unavailable {
		msg := fmt.Sprintf("%s; the outcome is unknown: grant %s may have been revoked. It is not resent: run `hadron secret client list` to check", mapped.Error(), output.SafeCell(grantID))
		return api.WithServerCode(exitcode.Newf(exitcode.Unavailable, "%s", msg), err)
	}
	return mapped
}
