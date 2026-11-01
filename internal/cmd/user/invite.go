package user

import (
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type platformInvitationDTO struct {
	ID              string  `json:"id"`
	Slug            string  `json:"slug"`
	Email           *string `json:"email"`
	Name            *string `json:"name"`
	GithubUsername  *string `json:"githubUsername"`
	MemberRole      string  `json:"memberRole"`
	OrganizationID  *string `json:"organizationId"`
	MaxActivations  *int    `json:"maxActivations"`
	ActivationCount int     `json:"activationCount"`
	ExpiresAt       *string `json:"expiresAt"`
	AcceptedAt      *string `json:"acceptedAt"`
	CreatedAt       string  `json:"createdAt"`
}

func newCmdInvite(f *cmdutil.Factory) *cobra.Command {
	var email, github, name string
	var expires, max int
	cmd := &cobra.Command{Use: "invite", Short: "Create a platform invitation and print its private acceptance slug", Long: `Create a platform invitation without adding an organization membership.
The server enforces authorization; this command makes no local role decision.

No email is sent. Deliver the slug privately: it is an acceptance token, and
whoever holds it can accept while signed in with hadron org invite accept.
--email, --github-username and --name record who it was intended for, not who
may accept it. All three are optional. To invite into an organization instead,
use hadron org invite create.

By default one activation is allowed with no expiry. --max-activations 0
means unlimited; --expires-in-days 0 means never expires. Unset flags leave
the server defaults. Text prints the slug and acceptance command; --json
returns invitation metadata, including slug. Treat that output as private.`, Example: "  hadron user invite --email alice@example.com --expires-in-days 7\n  hadron user invite --github-username alice --max-activations 1 --json", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if expires < 0 || max < 0 {
			return exitcode.Newf(exitcode.Usage, "expiry days and max activations must be non-negative")
		}
		var ep, gp, np *string
		var exp, mp *int
		if cmd.Flags().Changed("email") {
			ep = &email
		}
		if cmd.Flags().Changed("github-username") {
			gp = &github
		}
		if cmd.Flags().Changed("name") {
			np = &name
		}
		if cmd.Flags().Changed("expires-in-days") {
			exp = &expires
		}
		if cmd.Flags().Changed("max-activations") {
			mp = &max
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		resp, err := gen.InviteUser(cmd.Context(), client, ep, gp, np, exp, mp)
		if err != nil {
			return api.MapErrorKeepingCode(err)
		}
		if resp == nil || resp.InviteUser == nil {
			return exitcode.Newf(exitcode.Error, "server returned no invitation")
		}
		v := resp.InviteUser.InvitationFields
		dto := platformInvitationDTO{v.Id, v.Slug, v.Email, v.Name, v.GithubUsername, string(v.MemberRole), v.OrganizationId, v.MaxActivations, v.ActivationCount, v.ExpiresAt, v.AcceptedAt, v.CreatedAt}
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			_, e := fmt.Fprintf(w, "invitation %s\n  accept with: hadron org invite accept %s\n  deliver this slug privately; no email was sent\n", output.SafeCell(dto.Slug), output.SafeCell(dto.Slug))
			return e
		})
	}}
	cmd.Flags().StringVar(&email, "email", "", "intended invitee email (not an acceptance restriction)")
	cmd.Flags().StringVar(&github, "github-username", "", "intended invitee GitHub username")
	cmd.Flags().StringVar(&name, "name", "", "intended invitee display name")
	cmd.Flags().IntVar(&expires, "expires-in-days", 0, "days until expiry (default and 0: never)")
	cmd.Flags().IntVar(&max, "max-activations", 0, "activation limit (default 1; 0: unlimited)")
	return cmd
}
