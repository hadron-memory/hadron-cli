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
		Long: `List the organization Agent grants returned to this caller for an explicit
organization id or URN. The server decides which grants are visible: an empty
array is not proof that the organization has no grants globally. Run/worker or
non-admin callers may receive an empty concealed projection.

This unpaginated provider field returns its visible rows in one request; no
client completeness or license-eligibility decision is made. Active and revoked
rows are preserved, with server isActive and nullable lifecycle timestamps.

These are organization licenses, not principal-action 'hadron grant' records
or per-user Agent subscriptions. This read does not subscribe, revoke, install
or clean up user memories. No implicit --mine or --agent scope is supported.`,
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
					t.Row(g.AgentID, g.OrgID, fmt.Sprint(g.IsActive), g.CreatedAt, dash(g.ActivatedAt), dash(g.ExpiresAt), dash(g.RevokedAt))
				}
				return t.Flush()
			})
		}}
	list.Flags().StringVar(&org, "org", "", "organization id or URN (required)")
	_ = list.MarkFlagRequired("org")
	cmd.AddCommand(list)
	return cmd
}
