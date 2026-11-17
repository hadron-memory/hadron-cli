package grant

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one action grant",
		Long: `Show one action grant by the id from hadron grant list.

You can read a grant made to you, and an ADMIN or OWNER of the grant's org can
read any grant in it. A grant that does not exist and one you may not see
both exit 4, with the same message, so the answer says nothing about whether
it exists.

--json gives id, principalType, principalId, principalHandle, organizationId,
organizationUrn, actions, expiresAt and createdAt (the same fields as one
hadron grant list row).`,
		Example: `  hadron grant get 01a18cf643247d0fa261f320ec74d878
  hadron grant get 01a18cf643247d0fa261f320ec74d878 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.PrincipalGrant(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.PrincipalGrant == nil {
				return exitcode.Newf(exitcode.NotFound, "grant %q not found (or not visible to you)", args[0])
			}
			dto := dtoFromFields(resp.PrincipalGrant.PrincipalGrantFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				rows := [][2]string{
					{"id", dto.ID},
					{"grantee", dto.grantee()},
					{"org", output.Dash(dto.OrganizationURN)},
					{"actions", dto.actionList()},
					{"expiry", dto.expiry()},
					{"created", dto.CreatedAt},
				}
				for _, r := range rows {
					if _, err := fmt.Fprintf(w, "%-8s %s\n", r[0]+":", output.SafeCell(r[1])); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}
