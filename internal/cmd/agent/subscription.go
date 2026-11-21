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

// Organization licenses, distinct from principal-action grants and user subscriptions.
type orgAgentGrantDTO struct {
	AgentID     string  `json:"agentId"`
	OrgID       string  `json:"orgId"`
	IsActive    bool    `json:"isActive"`
	CreatedAt   string  `json:"createdAt"`
	ActivatedAt *string `json:"activatedAt"`
	ExpiresAt   *string `json:"expiresAt"`
	RevokedAt   *string `json:"revokedAt"`
}

func newCmdSubscription(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "subscription", Short: "Read and manage Agent subscriptions; revoke a user's or an organization's"}
	cmd.AddCommand(newCmdSubscriptionAdd(f), newCmdSubscriptionRevoke(f))
	var org, agent string
	var mine, all bool
	var limit, offset int
	list := &cobra.Command{
		Use: "list (--org <id|urn> | --agent <id|urn> | --mine) [--limit N] [--offset N] [--all]", Aliases: []string{"ls"}, Short: "List an organization's Agent licenses, or an Agent's or your own subscriptions",
		Long: `List the Agent licenses an organization holds. An Agent license lets the
organization create Apps that use that Agent, and bundle that Agent into Agents
it owns. --org is the organization's ID or URN (for example hrn:org:acme.com).

You see the licenses only if you are an admin or owner of the organization; for
anyone else, and for a worker or run, the list is empty. An empty list therefore
does not prove the organization has no licenses.

The table shows one row per license: AGENT ID, ORG ID, whether it is ACTIVE, and
when it was CREATED, ACTIVATED, EXPIRES or was REVOKED ("—" when never). Every
license the server returns is listed, whether or not it is still active; the
ACTIVE column and the dates tell them apart. --json is the same rows as an
array, with the dates as null when they do not apply.

--agent <id|urn> lists the users subscribed to one Agent (you must be its
admin), and --mine lists your own Agent subscriptions (a run or impersonated
caller gets an empty list, which does not prove there are none). These two
read one server page by default (--limit/--offset choose it) and say, in the
table and as "complete": false in --json, that the list is NOT the whole set;
--all follows every page to the end. --json for them is
{"items":[...],"total":N,"hasMore":bool,"complete":bool}.

These are subscriptions, not the grants of "hadron grant". This command only
reads: "add" and "revoke" change organization subscriptions.`,
		Example: `  hadron agent subscription list --org hrn:org:acme.com
  hadron agent subscription list --org hrn:org:acme.com --json
  hadron agent subscription list --agent <agent-id> --all --json
  hadron agent subscription list --mine --limit 20`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			org, agent = strings.TrimSpace(org), strings.TrimSpace(agent)
			// A blank selector is not "absent": --org "" next to --mine must
			// not silently run --mine alone.
			for _, n := range []string{"org", "agent"} {
				if cmd.Flags().Changed(n) && (n == "org" && org == "" || n == "agent" && agent == "") {
					return exitcode.Newf(exitcode.Usage, "--%s is empty: give a value or omit the flag", n)
				}
			}
			sel := 0
			for _, on := range []bool{org != "", agent != "", mine} {
				if on {
					sel++
				}
			}
			if sel != 1 {
				return exitcode.Newf(exitcode.Usage, "give exactly one of --org, --agent or --mine")
			}
			limitSet, offsetSet := cmd.Flags().Changed("limit"), cmd.Flags().Changed("offset")
			if org != "" && (limitSet || offsetSet || all) {
				return exitcode.Newf(exitcode.Usage, "--limit, --offset and --all apply to --agent and --mine, not --org")
			}
			if all && (limitSet || offsetSet) {
				return exitcode.Newf(exitcode.Usage, "--all fetches every page: do not combine it with --limit or --offset")
			}
			if limitSet && limit < 1 || offsetSet && offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit must be at least 1 and --offset at least 0")
			}
			if org == "" {
				pg, err := fetchSubscriptions(f, cmd, agent, mine, all, limit, offset, limitSet, offsetSet)
				if err != nil {
					return err
				}
				return renderSubscriptionPage(f, pg, mine, offset)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.OrgAgentGrants(cmd.Context(), client, org)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Organization == nil {
				return exitcode.Newf(exitcode.NotFound, "organization %q is not readable here", org)
			}
			rows := []orgAgentGrantDTO{}
			for _, g := range resp.Organization.AgentOrgGrants {
				if g == nil {
					return exitcode.Newf(exitcode.Error, "provider returned a null organization grant row")
				}
				rows = append(rows, orgAgentGrantDTO{AgentID: g.AgentId, OrgID: g.OrgId, IsActive: g.IsActive, CreatedAt: g.CreatedAt, ActivatedAt: g.ActivatedAt, ExpiresAt: g.ExpiresAt, RevokedAt: g.RevokedAt})
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if len(rows) == 0 {
					_, err := fmt.Fprintln(w, "No organization Agent grants are visible to this caller.")
					return err
				}
				t := output.NewTable(w, "AGENT ID", "ORG ID", "ACTIVE", "CREATED", "ACTIVATED", "EXPIRES", "REVOKED")
				for _, g := range rows {
					t.Row(output.SafeCell(g.AgentID), output.SafeCell(g.OrgID), fmt.Sprint(g.IsActive), output.SafeCell(g.CreatedAt), output.SafeCell(dash(g.ActivatedAt)), output.SafeCell(dash(g.ExpiresAt)), output.SafeCell(dash(g.RevokedAt)))
				}
				return t.Flush()
			})
		}}
	list.Flags().StringVar(&org, "org", "", "list this organization's Agent licenses (id or URN)")
	list.Flags().StringVar(&agent, "agent", "", "list the users subscribed to this Agent (id or URN); you must be its admin")
	list.Flags().BoolVar(&mine, "mine", false, "list your own Agent subscriptions")
	list.Flags().IntVar(&limit, "limit", 0, "with --agent/--mine: page size (server default when unset)")
	list.Flags().IntVar(&offset, "offset", 0, "with --agent/--mine: rows to skip")
	list.Flags().BoolVar(&all, "all", false, "with --agent/--mine: follow every page to the end")
	cmd.AddCommand(list)
	return cmd
}
