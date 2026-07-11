package grant

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

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var (
		org, user, expires string
		actions            []string
	)
	cmd := &cobra.Command{
		Use:   "create --org <ref> --user <ref> --action <action>[,...]",
		Short: "Grant a member extra management actions (org ADMIN)",
		Long: `Grant one org member extra management actions on top of their role bundle
(org ADMIN, interactive-only). The grantee must be a live member of the org —
a grant is an exception on top of membership, and it dies with the membership.

Action entries use the access-control matcher grammar: exact (memory.clone),
prefix (memory.*), or *. The server validates entry shape; an unknown exact
verb simply never matches a gate.`,
		Example: `  hadron grant create --org acme.com --user hrn:user:jane --action memory.clone
  hadron grant create --org acme.com --user jane --action memory.clone,memory.create \
    --expires 2027-01-01T00:00:00Z`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// --action is repeatable and comma-splittable; normalize + dedupe.
			seen := map[string]bool{}
			var actionSet []string
			for _, a := range actions {
				for _, part := range strings.Split(a, ",") {
					part = strings.TrimSpace(part)
					if part == "" || seen[part] {
						continue
					}
					seen[part] = true
					actionSet = append(actionSet, part)
				}
			}
			if len(actionSet) == 0 {
				return exitcode.Newf(exitcode.Usage, "at least one --action is required (e.g. --action memory.clone)")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var expiresAt *string
			if expires != "" {
				expiresAt = &expires
			}
			resp, err := gen.CreatePrincipalGrant(cmd.Context(), client, org, user, actionSet, expiresAt)
			if err != nil {
				return api.MapError(err)
			}
			dto := dtoFromFields(resp.CreatePrincipalGrant.PrincipalGrantFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ granted %s to %s in %s (grant %s, %s)\n",
					dto.actionList(), dto.grantee(), output.Dash(dto.OrganizationURN), dto.ID, dto.expiry())
				return err
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization the grant applies within (ID or URN; required)")
	cmd.Flags().StringVar(&user, "user", "", "grantee user (ID, handle, or hrn:user:<handle>; required)")
	cmd.Flags().StringSliceVar(&actions, "action", nil, "action to grant (repeatable or comma-separated; required)")
	cmd.Flags().StringVar(&expires, "expires", "", "ISO-8601 expiry timestamp with timezone (omit for perpetual)")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("action")
	return cmd
}
