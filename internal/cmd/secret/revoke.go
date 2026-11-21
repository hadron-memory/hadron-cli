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
		page, err := fetchClientPage(cmd.Context(), client, &l, &o)
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
came (exit 8). A grant that had already ended is returned as it is and is not
changed (the first end stands): the command prints its state and exits 0. One
revoked earlier reads REVOKED exactly as one revoked now; one that ended another
way (withdrawn, expired, spent, its holder gone) says it was already ended.

On a terminal it asks first; off one it needs --yes and sends nothing without it.

Exit codes: 0 the grant is REVOKED, or had already ended; 2 usage; 3 not signed in; 4 client not
found; 7 the answer was lost or did not confirm a revoke: the revoke is not
resent, so run 'hadron secret client list --all' (the grant id is in the error;
--all stops with an error after 1000 pages, and --limit with --offset goes past
that) to see whether the client ended (this includes a grant answered in a state
that is not an end, or one that still allows reads); 8 not permitted.`,
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
				return exitcode.Newf(exitcode.NotFound, "no read client %s among the clients you issued or hold (a client you neither issued nor hold does not list here, even where the server would let you revoke its grant as the App's manager); see `hadron secret client list --all` (--all stops with an error after 1000 pages; --limit with --offset goes past that)", output.SafeCell(fmt.Sprintf("%q", id)))
			}
			if strings.TrimSpace(c.GrantID) == "" {
				return exitcode.Newf(exitcode.Error, "the server returned read client %s with no grant id, so it cannot be revoked; nothing was sent", output.SafeCell(c.ClientID))
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
			var g *gen.RevokeSecretReadClientGrantRevokeGrant
			if resp != nil {
				g = resp.RevokeGrant
			}
			state, err := confirmedEndedState(g, c.GrantID)
			if err != nil {
				return err
			}
			d := revokeDTO{ClientID: c.ClientID, GrantID: c.GrantID, State: state, RevokedAt: g.RevokedAt, Allows: false}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				if state != string(gen.GrantStateRevoked) {
					_, e := fmt.Fprintf(w, "read client %s was already ended (%s); nothing changed\n", output.SafeCell(d.ClientID), output.SafeCell(state))
					return e
				}
				// REVOKED reads the same whether this call ended the grant or an earlier
				// revoke did (the first end stands, and the answer carries no marker of
				// which), so the text states the fact, not who did it.
				_, e := fmt.Fprintf(w, "read client %s: grant %s is %s (revokedAt %s)\n", output.SafeCell(d.ClientID), output.SafeCell(d.GrantID), output.SafeCell(state), output.SafeCell(output.Dash(d.RevokedAt)))
				return e
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "revoke without asking (required off a terminal)")
	return cmd
}

// definitiveRevokeRefusals are the GraphQL codes that are refusals BEFORE the
// grant is touched: the caller may not revoke it (GRANT_NOT_MANAGER, judged on the
// locked row before any write), is not signed in, or sent an unusable request. Only
// these, at the mutation's own level, keep their typed exit; every other failure
// after the request left may follow the write and is an unknown outcome.
var definitiveRevokeRefusals = []string{
	"GRANT_NOT_MANAGER", "FORBIDDEN", "UNAUTHENTICATED", "BAD_USER_INPUT",
	"GRAPHQL_VALIDATION_FAILED", "GRAPHQL_PARSE_FAILED",
}

// notConfirmed is the unknown-outcome error: the revoke may have ended the grant,
// so it is never resent, and the caller is told where to look.
func notConfirmed(grantID, why string) error {
	return exitcode.Newf(exitcode.Unavailable, "revoke not confirmed (%s): the outcome is unknown, grant %s may have been revoked. It is not resent: run `hadron secret client list --all` to check (--all stops with an error after 1000 pages; --limit with --offset goes past that)", why, output.SafeCell(grantID))
}

// revokeError maps a failed revoke. A definitive pre-write refusal keeps its typed
// exit. Anything else (a lost answer, a 5xx, an error beneath the mutation, a code
// that can follow the write) is an unknown outcome: exit 7 naming the grant and
// `client list`, never resent, with the server's code kept for --json.
func revokeError(err error, grantID string) error {
	mapped := api.MapErrorKeepingCode(err)
	if api.OnlyErrorCodesAtRoot(err, definitiveRevokeRefusals...) {
		return mapped
	}
	return api.WithServerCode(notConfirmed(grantID, mapped.Error()), err)
}

// confirmedEndedState checks the answer is the SAME grant in an explicit ended state.
func confirmedEndedState(g *gen.RevokeSecretReadClientGrantRevokeGrant, grantID string) (string, error) {
	if g == nil || g.Id == "" {
		return "", notConfirmed(grantID, "the server returned no grant")
	}
	if g.Id != grantID {
		return "", notConfirmed(grantID, "the server answered for a different grant")
	}
	switch g.State {
	case gen.GrantStateRevoked, gen.GrantStateExpired, gen.GrantStateWithdrawn, gen.GrantStateSpent, gen.GrantStateHolderGone:
		if g.Allows == nil {
			return "", notConfirmed(grantID, "the server did not say whether the grant still allows reads")
		}
		if *g.Allows {
			return "", notConfirmed(grantID, "the server returned the grant "+string(g.State)+" yet still allowing reads")
		}
		return string(g.State), nil
	case gen.GrantStateActive, gen.GrantStatePending:
		return "", notConfirmed(grantID, "the server returned the grant still "+string(g.State))
	}
	return "", notConfirmed(grantID, "the server returned no usable state")
}
