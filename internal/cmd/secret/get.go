package secret

import (
	"context"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
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
hrn:secret:<org or user handle>:<name> (an App secret by its id).

You can read your own user secrets and the secrets of an org you belong to.
A secret that does not exist and one you may not see both exit 4, with the
same message, so the answer says nothing about whether it exists.

--json gives id, ownerType, ownerId, name, kind, metadata, createdAt,
createdBy, updatedAt, updatedBy, currentVersion (null for org and user
secrets), protected and deleteAfterUse.

delete after use (deleteAfterUse) is the option that lets a read client's
auto-delete mark delete a protected secret; the server reports false for org and
user secrets. A server without the option shows "-" (null in --json). Change it
with hadron secret update --delete-after-use.`,
		Example: `  hadron secret get hrn:secret:alice:github-token
  hadron secret get 01a18cf643247d0fa261f320ec74d878 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			fields, option, err := readSecret(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if fields == nil {
				return exitcode.Newf(exitcode.NotFound, "secret not found, or not visible to you (the id or URN given is not shown)")
			}
			dto := secretDetailDTO{secretDTO: dtoFromFields(*fields), DeleteAfterUse: option}
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
					{"version", versionCell(dto.CurrentVersion)},
					{"protected", fmt.Sprint(dto.Protected)},
					{"created", dto.CreatedAt + " by " + output.Dash(dto.CreatedBy)},
					{"updated", dto.UpdatedAt + " by " + output.Dash(dto.UpdatedBy)},
				}
				for _, r := range rows {
					if _, err := fmt.Fprintf(w, "%-11s %s\n", r[0]+":", output.SafeCell(r[1])); err != nil {
						return err
					}
				}
				// Its label is wider than the column above, so it comes last.
				if _, err := fmt.Fprintf(w, "delete after use: %s\n", optionCell(dto.DeleteAfterUse)); err != nil {
					return err
				}
				return nil
			})
		},
	}
}

// readSecret reads one secret with its delete-after-use option; nil fields mean
// not found or not visible. A server whose schema lacks the field is read again
// without it, and the option is then unknown (nil).
func readSecret(ctx context.Context, client graphql.Client, ref string) (*gen.SecretFields, *bool, error) {
	resp, err := gen.Secret(ctx, client, ref)
	switch {
	case err == nil:
		if resp == nil || resp.Secret == nil {
			return nil, nil, nil
		}
		return &resp.Secret.SecretFields, resp.Secret.DeleteAfterUse, nil
	case !api.IsFrameworkValidationOnlyFor(err, "deleteAfterUse"):
		return nil, nil, err
	}
	old, err := gen.SecretWithoutDeleteAfterUse(ctx, client, ref)
	if err != nil || old == nil || old.Secret == nil {
		return nil, nil, err
	}
	return &old.Secret.SecretFields, nil, nil
}
