package connection

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

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one connection grant you created",
		Long: `Show one connection grant by the id from hadron connection grant list.

Only grants you created can be read. A grant that does not exist and one that
is not yours both exit 4, with the same message, so the answer says nothing
about whether it exists.

--json gives id, connectionId, granteeAppId, granteeAppName, granteeAppUrn,
scopes, expiresAt and createdAt (the same fields as one list row).`,
		Example: `  hadron connection grant get cg_123
  hadron connection grant get cg_123 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.ConnectionGrant(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.ConnectionGrant == nil {
				return exitcode.Newf(exitcode.NotFound, "connection grant %q not found (or not yours)", args[0])
			}
			dto := dtoFromFields(resp.ConnectionGrant.ConnectionGrantFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				expiry := "perpetual"
				if dto.ExpiresAt != nil && *dto.ExpiresAt != "" {
					expiry = "expires " + *dto.ExpiresAt
				}
				rows := [][2]string{
					{"id", dto.ID},
					{"connection", dto.ConnectionID},
					{"grantee", dto.grantee()},
					{"scopes", strings.Join(dto.Scopes, ",")},
					{"expiry", expiry},
					{"created", dto.CreatedAt},
				}
				for _, r := range rows {
					if _, err := fmt.Fprintf(w, "%-11s %s\n", r[0]+":", output.SafeCell(r[1])); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}
