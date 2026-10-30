package secret

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var scope, owner string
	cmd := &cobra.Command{
		Use:     "list --scope <user|org> [--owner <ref>]",
		Aliases: []string{"ls"},
		Short:   "List inspectable secret metadata for one owner scope",
		Long: `List the secrets of one owner: name, kind, metadata, id and audit fields.
Values are never returned or printed.

  --scope user                      your own secrets
  --scope org --owner <org ref>     an org's secrets (any member of the org)

You only ever see your own user secrets, and an org's only if you are a member:
listing another user's, or an org you don't belong to, comes back empty. The
ID column is what
hadron secret rm takes. --json gives an array of id, ownerType, ownerId, name,
kind, metadata, createdAt, createdBy, updatedAt and updatedBy; an empty list
is [].`,
		Example: `  hadron secret list --scope user
  hadron secret list --scope org --owner hrn:org:acme.com --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ownerType, ownerRef, err := validateOwner(scope, owner)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			items, err := api.CollectAll(func(limit, offset int) ([]*gen.SecretsSecretsSecretsPageItemsSecret, int, error) {
				resp, err := gen.Secrets(cmd.Context(), client, ownerType, ownerRef, &limit, &offset)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.Secrets == nil {
					return nil, 0, nil
				}
				return resp.Secrets.Items, resp.Secrets.Total, nil
			})
			if err != nil {
				return err
			}
			secrets := make([]secretDTO, 0, len(items))
			for _, s := range items {
				if s == nil {
					continue
				}
				secrets = append(secrets, dtoFromFields(s.SecretFields))
			}
			return output.Write(f.IOStreams, f.JSON, secrets, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "NAME", "KIND", "OWNER", "METADATA", "CREATED")
				for _, s := range secrets {
					meta := "-"
					if s.Metadata != nil {
						meta = string(*s.Metadata)
					}
					t.Row(output.SafeCell(s.ID), output.SafeCell(s.Name), output.SafeCell(s.Kind), output.SafeCell(s.OwnerType+":"+s.OwnerID), output.SafeCell(meta), output.SafeCell(s.CreatedAt))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "owner scope: user or org (required)")
	cmd.Flags().StringVar(&owner, "owner", "", "owner ID or URN (required except --scope user)")
	_ = cmd.MarkFlagRequired("scope")
	return cmd
}
