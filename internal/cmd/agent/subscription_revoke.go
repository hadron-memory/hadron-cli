package agent

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

// cli#2789: `agent subscription revoke`. One revokeAgentSubscription mutation; the
// server owns who may (Agent ADMIN, after a readable-Agent gate that answers an
// unreadable Agent exactly as an absent one) and whether the user's personal memory
// for the Agent is empty and so deleted (cor:dmo:050:08). The CLI adds no gate of its
// own, does not look at the memory first and does not promise an outcome for it.

// subscriptionRevokeDTO is the --json shape: the subscription as the server returned
// it after the revoke. It says nothing about the memory: the server does not report
// what it did with it.
type subscriptionRevokeDTO struct {
	AgentID     string  `json:"agentId"`
	UserID      string  `json:"userId"`
	IsActive    bool    `json:"isActive"`
	CreatedAt   string  `json:"createdAt"`
	ActivatedAt *string `json:"activatedAt"`
	ExpiresAt   *string `json:"expiresAt"`
	RevokedAt   *string `json:"revokedAt"`
	RevokedBy   *string `json:"revokedBy"`
}

// definitiveSubscriptionRevokeRefusals are the codes the server raises BEFORE the
// subscription is written: not signed in, an unusable request, an Agent or user that
// resolves to nothing, or a caller who is not an Agent admin. Only these, at the
// mutation's own level, keep their typed exit. Every other failure after the request
// left (a lost answer, a 5xx, an error beneath the mutation) may follow the write,
// including the memory deletion that runs after it, so it is an unknown outcome.
var definitiveSubscriptionRevokeRefusals = []string{
	"UNAUTHENTICATED", "FORBIDDEN", "NOT_FOUND", "AGENT_NOT_FOUND", "BAD_USER_INPUT",
	"URN_NOT_QUALIFIED", "GRAPHQL_VALIDATION_FAILED", "GRAPHQL_PARSE_FAILED",
}

// subscriptionRevokeNotConfirmed is the unknown-outcome error: the revoke, or the
// empty-memory deletion after it, may have happened, so it is never resent and the
// caller is told where to look.
func subscriptionRevokeNotConfirmed(why string) error {
	return exitcode.Newf(exitcode.Unavailable, "revoke not confirmed (%s): the outcome is unknown, the subscription of the user to the Agent (the refs given are not shown) may have been revoked and its empty personal memory deleted. It is not resent: check with `hadron memory list` whether that user's memory for the Agent is still there (or `hadron agent subscription list --agent <agent>`, which shows whether the subscription is revoked), then decide whether to run it again", why)
}

func subscriptionRevokeError(err error) error {
	mapped := api.MapErrorKeepingCode(err)
	if api.OnlyErrorCodesAtRoot(err, definitiveSubscriptionRevokeRefusals...) {
		return mapped
	}
	return api.WithServerCode(subscriptionRevokeNotConfirmed(mapped.Error()), err)
}

// confirmedRevoked checks the answer is a subscription that now reads as ended: not
// active, with a revokedAt. Anything else is not a confirmation.
func confirmedRevoked(s *gen.RevokeAgentSubscriptionRevokeAgentSubscription, agent, user string) (subscriptionRevokeDTO, error) {
	if s == nil || strings.TrimSpace(s.AgentId) == "" || strings.TrimSpace(s.UserId) == "" {
		return subscriptionRevokeDTO{}, subscriptionRevokeNotConfirmed("the server returned no subscription")
	}
	if s.IsActive {
		return subscriptionRevokeDTO{}, subscriptionRevokeNotConfirmed("the server returned the subscription still active")
	}
	if s.RevokedAt == nil {
		return subscriptionRevokeDTO{}, subscriptionRevokeNotConfirmed("the server returned the subscription without a revokedAt")
	}
	return subscriptionRevokeDTO{
		AgentID: s.AgentId, UserID: s.UserId, IsActive: s.IsActive, CreatedAt: s.CreatedAt,
		ActivatedAt: s.ActivatedAt, ExpiresAt: s.ExpiresAt, RevokedAt: s.RevokedAt, RevokedBy: s.RevokedBy,
	}, nil
}

func newCmdSubscriptionRevoke(f *cmdutil.Factory) *cobra.Command {
	var user, org string
	var yes bool
	cmd := &cobra.Command{
		Use:   "revoke <agent> (--user <ref> | --org <ref>) [--yes]",
		Short: "Revoke a user's subscription to an Agent (may delete their empty personal memory) or an organization's",
		Long: `Exactly one of --user and --org. --org revokes an ORGANIZATION's subscription
(the license "subscription list --org" shows) and is described at the end.

With --user, revoke one user's subscription to an Agent: the user can no longer use the Agent
through that subscription. This is not an organization license (that is the
'subscription list --org' of the same group) and it does not remove the Agent.

It can also DELETE a memory. When the subscription ends, the server removes the
user's personal memory for this Agent if that memory has no nodes, and keeps it
if it has any. The server decides which, when the revoke runs; the CLI does not
look first and cannot tell you beforehand. The answer does not say which happened:
check afterwards with 'hadron memory list'. A deleted memory is gone for good.

<agent> is the Agent's ID or URN. --user is the user's ID, email, handle or
hrn:user:<handle> URN. Only an admin or owner of the Agent's organization can do
this; an Agent you cannot read answers exactly as one that does not exist (exit
4), and nothing says which. The server's refusals keep their exit codes (3 not
signed in, 8 not permitted, 2 an unusable request).

On a terminal it asks first, naming the memory side effect; off one it needs
--yes and sends nothing without it. One mutation is sent and it is never resent.

Exit codes: 0 the subscription reads as ended; 2 usage; 3 not signed in; 4 the
Agent or user was not found; 7 the answer was lost, or did not show an ended
subscription: the revoke may or may not have happened, and the empty-memory
deletion after it may or may not have run. It is not resent: check with
'hadron memory list' (or "hadron agent subscription list --agent <agent>", which shows whether the subscription is revoked)
and decide; 8 not permitted.

With --org <id|urn> (revokeAgentOrgGrant) it revokes the organization's subscription
to the Agent. The organization's install state is left as it is, and a revoked
subscription is NOT reactivated by subscribing again (the server refuses a second
subscribe), so treat it as one-way. Allowed for an admin of the subscribing
organization or an admin of the Agent; the server may answer "not found" for
something you cannot see. It asks on a terminal and needs --yes without one. The
call is sent once; if its outcome cannot be known (exit 7) nothing is retried: check
with "hadron agent subscription list --org <org>" first (readable only by an admin
or owner of the organization; an Agent admin who is not one gets an empty list,
which does not settle it). --json is the grant {agentId,orgId,isActive,createdAt,
activatedAt,expiresAt,revokedAt}.`,
		Example: `  hadron agent subscription revoke hrn:agent:acme.com:helper --user alice@acme.com
  hadron agent subscription revoke hrn:agent:acme.com:helper --user hrn:user:alice --yes --json
  hadron agent subscription revoke hrn:agent:acme.com:helper --org hrn:org:acme.com --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentRef := strings.TrimSpace(args[0])
			userRef := strings.TrimSpace(user)
			orgRef := strings.TrimSpace(org)
			if agentRef == "" {
				return exitcode.Newf(exitcode.Usage, "give the Agent's ID or URN")
			}
			if cmd.Flags().Changed("user") == cmd.Flags().Changed("org") {
				return exitcode.Newf(exitcode.Usage, "give exactly one of --user and --org")
			}
			if cmd.Flags().Changed("org") {
				if orgRef == "" {
					return exitcode.Newf(exitcode.Usage, "--org is empty: give the organization's ID or URN")
				}
				return runOrgRevoke(f, cmd, agentRef, orgRef, yes)
			}
			if userRef == "" {
				return exitcode.Newf(exitcode.Usage, "give --user: the user's ID, email, handle or hrn:user URN")
			}
			prompt := fmt.Sprintf("Revoke %s's subscription to Agent %s? This also DELETES that user's personal memory for the Agent if it has no nodes (a memory with nodes is kept; the server decides when you confirm). This cannot be undone.", output.SafeCell(userRef), output.SafeCell(agentRef))
			// Nothing is sent when the answer cannot be asked for, or is no.
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RevokeAgentSubscription(cmd.Context(), client, agentRef, userRef)
			if err != nil {
				return subscriptionRevokeError(err)
			}
			var s *gen.RevokeAgentSubscriptionRevokeAgentSubscription
			if resp != nil {
				s = resp.RevokeAgentSubscription
			}
			d, err := confirmedRevoked(s, agentRef, userRef)
			if err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				_, e := fmt.Fprintf(w, "subscription of user %s to Agent %s is revoked (revokedAt %s).\nIts empty personal memory is deleted by the server and a memory with nodes is kept; the server does not say which here. Check with `hadron memory list`.\n",
					output.SafeCell(d.UserID), output.SafeCell(d.AgentID), output.SafeCell(output.Dash(d.RevokedAt)))
				return e
			})
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "revoke this user's subscription: ID, email, handle or hrn:user URN")
	cmd.Flags().StringVar(&org, "org", "", "revoke this organization's subscription: id or URN")
	cmd.Flags().BoolVar(&yes, "yes", false, "revoke without asking (required off a terminal)")
	return cmd
}
