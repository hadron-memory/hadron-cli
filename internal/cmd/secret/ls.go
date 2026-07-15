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
		Use:     "ls --scope <user|org|app|memory> [--owner <ref>]",
		Aliases: []string{"list"},
		Short:   "List inspectable secret metadata for one owner scope",
		Long: `List secrets for one owner scope. Output includes only the inspectable
half — name, kind, metadata, and audit fields. Secret values are never returned
by the API and never printed by the CLI.`,
		Example: `  hadron secret ls --scope user
  hadron secret ls --scope app --owner acme.com::monitor --json`,
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
				t := output.NewTable(w, "ID", "NAME", "KIND", "OWNER", "METADATA", "UPDATED")
				for _, s := range secrets {
					meta := "-"
					if s.Metadata != nil {
						meta = string(*s.Metadata)
					}
					t.Row(s.ID, s.Name, s.Kind, s.OwnerType+":"+s.OwnerID, meta, s.UpdatedAt)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "owner scope: user, org, app, or memory (required)")
	cmd.Flags().StringVar(&owner, "owner", "", "owner ID or URN (required except --scope user)")
	_ = cmd.MarkFlagRequired("scope")
	return cmd
}
