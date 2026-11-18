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
	cmd := &cobra.Command{Use: "subscription", Short: "Read organization Agent licenses"}
	var org string
	list := &cobra.Command{
		Use: "list --org <id|urn>", Aliases: []string{"ls"}, Short: "List Agent licenses visible for one organization",
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

These are organization licenses, not the grants of "hadron grant" and not
per-user Agent subscriptions. This command only reads: it does not subscribe,
revoke or install anything.`,
		Example: `  hadron agent subscription list --org hrn:org:acme.com
  hadron agent subscription list --org hrn:org:acme.com --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(org) == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty --org id or URN")
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
	list.Flags().StringVar(&org, "org", "", "organization id or URN (required)")
	_ = list.MarkFlagRequired("org")
	cmd.AddCommand(list)
	return cmd
}
