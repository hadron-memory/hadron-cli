package agent

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// userAgentSubscriptionDTO is one user's subscription to an Agent (the paged
// reads): distinct from an organization's license (orgAgentGrantDTO).
type userAgentSubscriptionDTO struct {
	AgentID     string  `json:"agentId"`
	UserID      string  `json:"userId"`
	IsActive    bool    `json:"isActive"`
	CreatedAt   string  `json:"createdAt"`
	ActivatedAt *string `json:"activatedAt"`
	ExpiresAt   *string `json:"expiresAt"`
	RevokedAt   *string `json:"revokedAt"`
}

// subscriptionPageDTO is the --json shape of the paged reads. complete says
// whether items is the whole set: true after --all (or a page that ends the
// set), false when the server reports more rows than were fetched.
type subscriptionPageDTO struct {
	Items    []userAgentSubscriptionDTO `json:"items"`
	Total    int                        `json:"total"`
	HasMore  bool                       `json:"hasMore"`
	Complete bool                       `json:"complete"`
}

type rawSubPage struct {
	items   []userAgentSubscriptionDTO
	total   int
	hasMore bool
}

func newCmdSubscriptionAdd(f *cmdutil.Factory) *cobra.Command {
	var org, agent string
	c := &cobra.Command{
		Use: "add --org <id|urn> --agent <id|urn>", Short: "Subscribe an organization to a PUBLIC Agent",
		Long: `Subscribe an organization to a PUBLIC Agent without creating an App. The
subscription lets the organization create Apps that use the Agent and bundle it
into Agents it owns.

Only an admin of the subscribing organization may do this, and only for a PUBLIC
Agent you can read. An inactive (revoked) subscription is not reactivated by
subscribing again: the server refuses, and so does this command. The server
decides every one of these; the CLI infers nothing from an empty result.

The call is sent once. If its outcome cannot be known (a lost connection, an
unusable answer) nothing is retried: check with "hadron agent subscription list
--org <org>" before running it again.`,
		Example: `  hadron agent subscription add --org hrn:org:acme.com --agent hrn:agent:hadronmemory.com:scribe
  hadron agent subscription add --org hrn:org:acme.com --agent <agent-id> --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			org, agent = strings.TrimSpace(org), strings.TrimSpace(agent)
			if org == "" || agent == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty --org and --agent")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SubscribeOrgToAgent(cmd.Context(), client, agent, org)
			if err != nil {
				return orgGrantWriteError(err, "subscribe", org)
			}
			if resp == nil || resp.CreateAgentOrgGrant == nil {
				return orgGrantUnknown("subscribe", org, nil)
			}
			g := resp.CreateAgentOrgGrant
			return renderGrant(f, orgAgentGrantDTO{AgentID: g.AgentId, OrgID: g.OrgId, IsActive: g.IsActive, CreatedAt: g.CreatedAt, ActivatedAt: g.ActivatedAt, ExpiresAt: g.ExpiresAt, RevokedAt: g.RevokedAt}, "Subscribed")
		}}
	c.Flags().StringVar(&org, "org", "", "organization id or URN (required)")
	c.Flags().StringVar(&agent, "agent", "", "Agent id or URN (required)")
	_ = c.MarkFlagRequired("org")
	_ = c.MarkFlagRequired("agent")
	return c
}

func newCmdSubscriptionRevoke(f *cmdutil.Factory) *cobra.Command {
	var org, agent string
	var yes bool
	c := &cobra.Command{
		Use: "revoke --org <id|urn> --agent <id|urn> [--yes]", Aliases: []string{"unsubscribe"},
		Short: "Revoke an organization's Agent subscription",
		Long: `Revoke an organization's subscription to an Agent. This does not delete the
organization's install state, but the revoked subscription is not reactivated by
subscribing again: the server refuses a second subscribe, so treat this as
one-way unless the server team restores it.

Allowed for an admin of the subscribing organization or an admin of the Agent.
The server may answer "not found" for something you cannot see; that is not
proof it does not exist. It asks for confirmation on a terminal and needs --yes
without one.

The call is sent once. If its outcome cannot be known nothing is retried: check
with "hadron agent subscription list --org <org>" before running it again.`,
		Example: `  hadron agent subscription revoke --org hrn:org:acme.com --agent <agent-id> --yes
  hadron agent subscription unsubscribe --org hrn:org:acme.com --agent <agent-id> --yes --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			org, agent = strings.TrimSpace(org), strings.TrimSpace(agent)
			if org == "" || agent == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty --org and --agent")
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Revoke organization %s's subscription to Agent %s? It is not reactivated by subscribing again.", org, agent)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UnsubscribeOrgFromAgent(cmd.Context(), client, agent, org)
			if err != nil {
				return orgGrantWriteError(err, "revoke", org)
			}
			if resp == nil || resp.RevokeAgentOrgGrant == nil {
				return orgGrantUnknown("revoke", org, nil)
			}
			g := resp.RevokeAgentOrgGrant
			return renderGrant(f, orgAgentGrantDTO{AgentID: g.AgentId, OrgID: g.OrgId, IsActive: g.IsActive, CreatedAt: g.CreatedAt, ActivatedAt: g.ActivatedAt, ExpiresAt: g.ExpiresAt, RevokedAt: g.RevokedAt}, "Revoked")
		}}
	c.Flags().StringVar(&org, "org", "", "organization id or URN (required)")
	c.Flags().StringVar(&agent, "agent", "", "Agent id or URN (required)")
	c.Flags().BoolVar(&yes, "yes", false, "confirm without prompting (required without a terminal)")
	_ = c.MarkFlagRequired("org")
	_ = c.MarkFlagRequired("agent")
	return c
}

// orgGrantWriteError maps a failed write. Only a coded server refusal says it
// did not take effect; anything else may follow a commit and is reported as an
// unknown outcome, never retried.
func orgGrantWriteError(err error, verb, org string) error {
	if api.IsDefiniteRefusal(err) {
		return api.MapError(err)
	}
	return orgGrantUnknown(verb, org, err)
}

func orgGrantUnknown(verb, org string, cause error) error {
	why := "the answer was unusable"
	if cause != nil {
		why = api.MapError(cause).Error()
	}
	return exitcode.Newf(exitcode.Unavailable, "%s outcome unknown (%s). Nothing was retried. Check with `hadron agent subscription list --org %s` before running it again", verb, why, cmdutil.ShellQuote(org))
}

func renderGrant(f *cmdutil.Factory, g orgAgentGrantDTO, verb string) error {
	return output.Write(f.IOStreams, f.JSON, g, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "%s: Agent %s, organization %s (active: %v, revoked: %s)\n", verb, output.SafeCell(g.AgentID), output.SafeCell(g.OrgID), g.IsActive, output.SafeCell(dash(g.RevokedAt)))
		return err
	})
}

// fetchSubscriptions reads the paged --agent / --mine list: one page at the
// caller's limit/offset, or every page with all.
func fetchSubscriptions(f *cmdutil.Factory, cmd *cobra.Command, agent string, mine, all bool, limit, offset int, limitSet, offsetSet bool) (subscriptionPageDTO, error) {
	client, err := f.GraphQLClient()
	if err != nil {
		return subscriptionPageDTO{}, err
	}
	fetch := func(l, o int) (rawSubPage, error) {
		var lp, op *int
		if l > 0 {
			lp = &l
		}
		if o > 0 || offsetSet {
			op = &o
		}
		out := rawSubPage{items: []userAgentSubscriptionDTO{}}
		conv := func(a, u, c string, act bool, activated, ex, rv *string) userAgentSubscriptionDTO {
			return userAgentSubscriptionDTO{AgentID: a, UserID: u, IsActive: act, CreatedAt: c, ActivatedAt: activated, ExpiresAt: ex, RevokedAt: rv}
		}
		if mine {
			r, err := gen.MyAgentSubscriptionsPage(cmd.Context(), client, lp, op)
			if err != nil {
				return out, api.MapError(err)
			}
			if r == nil || r.MyAgentSubscriptionsPage == nil {
				return out, exitcode.Newf(exitcode.Error, "the server returned no subscription page")
			}
			out.total, out.hasMore = r.MyAgentSubscriptionsPage.Total, r.MyAgentSubscriptionsPage.HasMore
			for _, i := range r.MyAgentSubscriptionsPage.Items {
				if i == nil {
					return out, exitcode.Newf(exitcode.Error, "the server returned a null subscription row")
				}
				out.items = append(out.items, conv(i.AgentId, i.UserId, i.CreatedAt, i.IsActive, i.ActivatedAt, i.ExpiresAt, i.RevokedAt))
			}
			return out, nil
		}
		r, err := gen.AgentSubscriptionsPage(cmd.Context(), client, agent, lp, op)
		if err != nil {
			return out, api.MapError(err)
		}
		if r == nil || r.AgentSubscriptionsPage == nil {
			return out, exitcode.Newf(exitcode.Error, "the server returned no subscription page")
		}
		out.total, out.hasMore = r.AgentSubscriptionsPage.Total, r.AgentSubscriptionsPage.HasMore
		for _, i := range r.AgentSubscriptionsPage.Items {
			if i == nil {
				return out, exitcode.Newf(exitcode.Error, "the server returned a null subscription row")
			}
			out.items = append(out.items, conv(i.AgentId, i.UserId, i.CreatedAt, i.IsActive, i.ActivatedAt, i.ExpiresAt, i.RevokedAt))
		}
		return out, nil
	}
	if !all {
		l := 0
		if limitSet {
			l = limit
		}
		p, err := fetch(l, offset)
		if err != nil {
			return subscriptionPageDTO{}, err
		}
		return subscriptionPageDTO{Items: p.items, Total: p.total, HasMore: p.hasMore, Complete: !p.hasMore && offset == 0 && len(p.items) == p.total}, nil
	}
	var items []userAgentSubscriptionDTO
	total := 0
	for o := 0; ; {
		p, err := fetch(api.PageLimit, o)
		if err != nil {
			return subscriptionPageDTO{}, err
		}
		items = append(items, p.items...)
		total = p.total
		o += len(p.items)
		// A server that ignores the offset (or a set that moved under the walk)
		// hands back more rows than its own total: stop rather than spin and
		// grow without bound.
		if len(items) > total {
			return subscriptionPageDTO{Items: items, Total: total, HasMore: true}, exitcode.Newf(exitcode.Error, "the server returned more rows than its total (%d rows against a total of %d); the list is inconsistent", len(items), total)
		}
		if !p.hasMore || len(p.items) == 0 {
			// An empty page that still claims more is a server bug: say so
			// rather than present a truncated set as the whole.
			if p.hasMore {
				return subscriptionPageDTO{Items: items, Total: total, HasMore: true}, exitcode.Newf(exitcode.Error, "the server reported more subscriptions but returned an empty page after %d rows; the list is incomplete", len(items))
			}
			break
		}
	}
	if items == nil {
		items = []userAgentSubscriptionDTO{}
	}
	// complete:true is a claim about the whole set: hold it to the server's own
	// count instead of trusting the last page's hasMore alone.
	if len(items) != total {
		return subscriptionPageDTO{Items: items, Total: total}, exitcode.Newf(exitcode.Error, "the server's total is %d but %d rows were returned; the list is inconsistent", total, len(items))
	}
	return subscriptionPageDTO{Items: items, Total: total, Complete: true}, nil
}

func renderSubscriptionPage(f *cmdutil.Factory, pg subscriptionPageDTO, mine bool, offset int) error {
	return output.Write(f.IOStreams, f.JSON, pg, func(w io.Writer) error {
		switch {
		case len(pg.Items) == 0 && pg.Total == 0 && offset == 0:
			msg := "No subscriptions are visible to this caller."
			if mine {
				msg += " (A run or impersonated caller has no personal subscriptions, so an empty list does not prove there are none.)"
			}
			if _, err := fmt.Fprintln(w, msg); err != nil {
				return err
			}
		case len(pg.Items) == 0:
			// Past the end (or a set that shrank): the page is empty, which says
			// nothing about whether subscriptions exist.
			if _, err := fmt.Fprintf(w, "No rows at offset %d; the server reports %d subscription(s) in all. Use --offset 0 or --all.\n", offset, pg.Total); err != nil {
				return err
			}
			return nil
		default:
			t := output.NewTable(w, "AGENT ID", "USER ID", "ACTIVE", "CREATED", "ACTIVATED", "EXPIRES", "REVOKED")
			for _, g := range pg.Items {
				t.Row(output.SafeCell(g.AgentID), output.SafeCell(g.UserID), fmt.Sprint(g.IsActive), output.SafeCell(g.CreatedAt), output.SafeCell(dash(g.ActivatedAt)), output.SafeCell(dash(g.ExpiresAt)), output.SafeCell(dash(g.RevokedAt)))
			}
			if err := t.Flush(); err != nil {
				return err
			}
		}
		if pg.Complete {
			return nil
		}
		if pg.HasMore {
			_, err := fmt.Fprintf(w, "Showing %d of %d (from offset %d); this is NOT the whole set. Page on with --offset %d, or use --all.\n", len(pg.Items), pg.Total, offset, offset+len(pg.Items))
			return err
		}
		_, err := fmt.Fprintf(w, "Showing %d of %d (from offset %d); this is NOT the whole set: the rows before this offset are not shown. Use --offset 0, or --all.\n", len(pg.Items), pg.Total, offset)
		return err
	})
}
