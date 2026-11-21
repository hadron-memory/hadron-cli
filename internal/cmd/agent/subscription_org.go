package agent

import (
	"encoding/json"
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

// MaxSubscriptionRows is the hard row cap of one --all read (cli#2855). It is a variable only so
// a test can lower it.
var MaxSubscriptionRows = 10000

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
				return orgGrantWriteError(err, "subscribe", org, "createAgentOrgGrant")
			}
			if resp == nil || resp.CreateAgentOrgGrant == nil {
				return orgGrantUnknown("subscribe", org, nil)
			}
			g := resp.CreateAgentOrgGrant
			// Rule (same active predicate as revoke, mirrored): a subscribe must
			// come back ACTIVE and not revoked.
			if !g.IsActive || g.RevokedAt != nil {
				return exitcode.Newf(exitcode.Error, "the server answered, but the subscription of organization %s to Agent %s does not come back active and unrevoked (active: %v); check with `hadron agent subscription list --org %s`", org, agent, g.IsActive, cmdutil.ShellQuote(org))
			}
			return renderGrant(f, orgAgentGrantDTO{AgentID: g.AgentId, OrgID: g.OrgId, IsActive: g.IsActive, CreatedAt: g.CreatedAt, ActivatedAt: g.ActivatedAt, ExpiresAt: g.ExpiresAt, RevokedAt: g.RevokedAt}, "Subscribed")
		}}
	c.Flags().StringVar(&org, "org", "", "organization id or URN (required)")
	c.Flags().StringVar(&agent, "agent", "", "Agent id or URN (required)")
	_ = c.MarkFlagRequired("org")
	_ = c.MarkFlagRequired("agent")
	return c
}

// runOrgRevoke is `revoke <agent> --org <ref>`: revoke an organization's
// subscription to an Agent (revokeAgentOrgGrant). The Long text of the one
// revoke command (subscription_revoke.go) documents it.
func runOrgRevoke(f *cmdutil.Factory, cmd *cobra.Command, agent, org string, yes bool) error {
	if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Revoke organization %s's subscription to Agent %s? It is not reactivated by subscribing again.", org, agent)); err != nil {
		return err
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	resp, err := gen.UnsubscribeOrgFromAgent(cmd.Context(), client, agent, org)
	if err != nil {
		return orgGrantWriteError(err, "revoke", org, "revokeAgentOrgGrant")
	}
	if resp == nil || resp.RevokeAgentOrgGrant == nil {
		return orgGrantUnknown("revoke", org, nil)
	}
	g := resp.RevokeAgentOrgGrant
	// Rule: a revoke must come back revoked (revokedAt set) AND inactive.
	if g.IsActive || g.RevokedAt == nil {
		return exitcode.Newf(exitcode.Error, "the server answered, but the subscription of organization %s to Agent %s does not come back revoked (active: %v); check with `hadron agent subscription list --org %s`", org, agent, g.IsActive, cmdutil.ShellQuote(org))
	}
	return renderGrant(f, orgAgentGrantDTO{AgentID: g.AgentId, OrgID: g.OrgId, IsActive: g.IsActive, CreatedAt: g.CreatedAt, ActivatedAt: g.ActivatedAt, ExpiresAt: g.ExpiresAt, RevokedAt: g.RevokedAt}, "Revoked")
}

// orgGrantWriteError maps a failed write. Only a coded server refusal of the
// mutation field itself (no path, or exactly [field]) says it did not take
// effect; an error below the field came from resolving the returned object after
// the write committed, and anything else may follow a commit too: both are
// reported as an unknown outcome, never retried.
func orgGrantWriteError(err error, verb, org, field string) error {
	if api.IsDefiniteRefusalAt(err, field) {
		return api.MapError(err)
	}
	return orgGrantUnknown(verb, org, err)
}

func orgGrantUnknown(verb, org string, cause error) error {
	check := fmt.Sprintf("Nothing was retried. Check with `hadron agent subscription list --org %s` before running it again", cmdutil.ShellQuote(org))
	if cause == nil {
		return exitcode.Newf(exitcode.Unavailable, "%s outcome unknown (the answer was unusable). %s", verb, check)
	}
	// The cause stays wrapped, not flattened to text (cli#2855): what the server sent, its code and
	// request id, reaches the --json envelope, and the message adds nothing it did not say.
	mapped := api.MapError(cause)
	return exitcode.New(exitcode.Unavailable, fmt.Errorf("%s outcome unknown (%s). %s: %w", verb, mapped.Error(), check, mapped))
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
		// An empty --mine list is no whole-set claim: a run or impersonated caller has no personal
		// subscriptions, so "none" says nothing about the person (cli#2855).
		complete := !p.hasMore && offset == 0 && len(p.items) == p.total && (!mine || p.total != 0)
		return subscriptionPageDTO{Items: p.items, Total: p.total, HasMore: p.hasMore, Complete: complete}, nil
	}
	var items []userAgentSubscriptionDTO
	total := 0
	seen := map[string]bool{}
	for o := 0; ; {
		p, err := fetch(api.PageLimit, o)
		if err != nil {
			return subscriptionPageDTO{}, err
		}
		// Offset paging over a newest-first list: a row created mid-walk shifts
		// the rest down and raises the total. Pin the first page's total and
		// refuse a repeated row, or len==total could hold with a duplicate and
		// a missing row.
		if o == 0 {
			total = p.total
		} else if p.total != total {
			return subscriptionPageDTO{Items: items, Total: total, HasMore: true}, exitcode.Newf(exitcode.Error, "the set changed during the read (total %d, then %d); run it again", total, p.total)
		}
		// A repeated row is keyed on the WHOLE row, not (agent, user): the list
		// carries no row id, and a user may hold more than one row for an Agent
		// (a revoked one and a later one), which must not fail every walk.
		// A later page that starts with the FIRST page's first row is a server
		// that ignores --offset (the len>total stop below only catches it when
		// the total is under two pages): say so, not "run it again".
		if o > 0 && len(p.items) > 0 && len(items) > 0 {
			a, _ := json.Marshal(p.items[0])
			b, _ := json.Marshal(items[0])
			if string(a) == string(b) {
				return subscriptionPageDTO{Items: items, Total: total, HasMore: true}, exitcode.Newf(exitcode.Error, "the server returned the first page again at offset %d: it is ignoring --offset, so the list cannot be read in full", o)
			}
		}
		dup := ""
		for _, it := range p.items {
			b, _ := json.Marshal(it)
			if seen[string(b)] && dup == "" {
				dup = it.AgentID + "/" + it.UserID
			}
			seen[string(b)] = true
		}
		items = append(items, p.items...)
		o += len(p.items)
		// A server that ignores the offset hands back more rows than its own
		// total: stop rather than spin and grow without bound. This is judged
		// BEFORE the repeated-row rule, which would otherwise misname it.
		if len(items) > total {
			return subscriptionPageDTO{Items: items, Total: total, HasMore: true}, exitcode.Newf(exitcode.Error, "the server returned more rows than its total (%d rows against a total of %d); the list is inconsistent (a server that ignores --offset does this)", len(items), total)
		}
		if dup != "" {
			return subscriptionPageDTO{Items: items, Total: total, HasMore: true}, exitcode.Newf(exitcode.Error, "the server returned an identical subscription row twice (Agent/user %s) without exceeding its total: a row created during the walk shifted the pages; run it again", output.SafeCell(dup))
		}
		// The hard cap (cli#2855): a walk that would go on past MaxSubscriptionRows stops here and says
		// where it was cut, never returning a long prefix as if it were the set.
		if p.hasMore && len(items) >= MaxSubscriptionRows {
			return subscriptionPageDTO{Items: items, Total: total, HasMore: true}, exitcode.Newf(exitcode.Error, "the walk was cut at %d rows (the CLI reads at most %d with --all) of a total of %d; this is NOT the whole set. Narrow the question, or page on with --offset %d", len(items), MaxSubscriptionRows, total, len(items))
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
	return subscriptionPageDTO{Items: items, Total: total, Complete: !mine || total != 0}, nil
}

func renderSubscriptionPage(f *cmdutil.Factory, pg subscriptionPageDTO, mine bool, offset int) error {
	return output.Write(f.IOStreams, f.JSON, pg, func(w io.Writer) error {
		switch {
		case len(pg.Items) == 0 && pg.Total == 0 && offset == 0:
			msg := "No subscriptions are visible to this caller."
			if mine {
				msg += " (A run or impersonated caller has no personal subscriptions, so an empty list does not prove there are none.)"
			}
			_, err := fmt.Fprintln(w, msg)
			return err
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
		if offset == 0 {
			_, err := fmt.Fprintf(w, "Showing %d of %d (from offset 0); this is NOT the whole set: the server's answer is inconsistent with its own total. Run it again, or use --all.\n", len(pg.Items), pg.Total)
			return err
		}
		_, err := fmt.Fprintf(w, "Showing %d of %d (from offset %d); this is NOT the whole set: the rows before this offset are not shown. Use --offset 0, or --all.\n", len(pg.Items), pg.Total, offset)
		return err
	})
}
