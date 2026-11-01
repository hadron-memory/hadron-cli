package secret

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
		Use:   "get <id|urn>",
		Short: "Show one secret's metadata (never its value)",
		Long: `Show one secret: name, kind, owner, metadata and the created/updated fields.
The value is never returned or printed; there is no way to read it back.

Name it by the id from hadron secret list, or by URN:
hrn:secret:<org or user handle>:<name>.

You can read your own user secrets and the secrets of an org you belong to.
A secret that does not exist and one you may not see both exit 4, with the
same message, so the answer says nothing about whether it exists.

--json gives id, ownerType, ownerId, name, kind, metadata, createdAt,
createdBy, updatedAt and updatedBy.`,
		Example: `  hadron secret get hrn:secret:alice:github-token
  hadron secret get 01a18cf643247d0fa261f320ec74d878 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.Secret(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.Secret == nil {
				return exitcode.Newf(exitcode.NotFound, "secret %q not found (or not visible to you)", args[0])
			}
			dto := dtoFromFields(resp.Secret.SecretFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				meta := "-"
				if dto.Metadata != nil {
					meta = string(*dto.Metadata)
				}
				rows := [][2]string{
					{"id", dto.ID},
					{"name", dto.Name},
					{"kind", dto.Kind},
					{"owner", dto.OwnerType + ":" + dto.OwnerID},
					{"metadata", meta},
					{"created", dto.CreatedAt + " by " + output.Dash(dto.CreatedBy)},
					{"updated", dto.UpdatedAt + " by " + output.Dash(dto.UpdatedBy)},
				}
				for _, r := range rows {
					if _, err := fmt.Fprintf(w, "%-9s %s\n", r[0]+":", output.SafeCell(r[1])); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}
