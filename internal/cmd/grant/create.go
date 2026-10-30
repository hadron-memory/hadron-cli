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
		Long: `Grant one org member extra management actions on top of their role. Needs
ADMIN or OWNER in the org (else exit 8). The grantee must be a current member;
the grant stops applying if they leave.

Interactive-only means the server accepts this only from a person acting as
themselves: a personal login or personal API token, including a worker
session. An App key, an agent run acting for someone, or an impersonation
session is refused (exit 8). It is not a terminal check, so it works in
scripts, and it asks no confirmation.

--org is the org's id, URN or root. --user is the grantee's id, handle or
hrn:user:<handle>. --action takes one or more actions (repeat it or separate
with commas), each exact (memory.clone), a prefix (memory.*) or *. The
actions checked today are memory.clone, memory.create and memory.delete; any
other well-formed name is accepted but grants nothing. --expires is an
ISO-8601 time with a timezone, in the future; without it the grant does not
expire.

Prints "✓ granted <actions> to <user> in <org> (grant <id>, <expiry>)"; the
grant id is what hadron grant revoke takes.`,
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
			// The schema marks the payload non-null, so this only fires on a
			// malformed response — guard rather than panic (PR-214 review).
			if resp == nil || resp.CreatePrincipalGrant == nil {
				return exitcode.Newf(exitcode.Error, "server returned no grant payload")
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
