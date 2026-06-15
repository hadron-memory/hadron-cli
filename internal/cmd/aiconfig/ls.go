package aiconfig

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// aiConfigDTO is the stable --json shape for a masked AiServiceConfig.
// It carries every masked field the server exposes — never key material
// beyond apiKeyPreview.
type aiConfigDTO struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	OwnerType     string           `json:"ownerType"`
	OwnerID       string           `json:"ownerId"`
	Provider      string           `json:"provider"`
	Model         string           `json:"model"`
	HasAPIKey     bool             `json:"hasApiKey"`
	APIKeyPreview *string          `json:"apiKeyPreview"`
	Params        *json.RawMessage `json:"params"`
	Enabled       bool             `json:"enabled"`
	CreatedAt     string           `json:"createdAt"`
	UpdatedAt     *string          `json:"updatedAt"`
}

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var agent string
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the AI service configs resolvable in an App's chat context",
		Long: `List the masked set of AI service configs resolvable in an App's chat
context — every distinct config name a chat could select, deduped with the
innermost owner winning (App → Agent → Org → HadronServer), enabled only.

Output is masked: never key material, only hasApiKey and a short apiKeyPreview.

--app defaults to the configured App context (hadron app use / global --app);
you must be a member of that App. --agent narrows to an agent installed in it.
Both accept an ID or a URN.`,
		Example: `  hadron ai-config ls --app acme.com:juno-app
  hadron ai-config ls --app acme.com:juno-app --agent acme.com:juno --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			appCtx, err := f.App()
			if err != nil {
				return err
			}
			resp, err := gen.ResolveAiServiceConfigs(cmd.Context(), client, optional(appCtx), optional(agent))
			if err != nil {
				return api.MapError(err)
			}

			configs := make([]aiConfigDTO, 0, len(resp.ResolveAiServiceConfigs))
			for _, c := range resp.ResolveAiServiceConfigs {
				configs = append(configs, aiConfigDTO{
					ID:            c.Id,
					Name:          c.Name,
					OwnerType:     string(c.OwnerType),
					OwnerID:       c.OwnerId,
					Provider:      c.Provider,
					Model:         c.Model,
					HasAPIKey:     c.HasApiKey,
					APIKeyPreview: c.ApiKeyPreview,
					Params:        c.Params,
					Enabled:       c.Enabled,
					CreatedAt:     c.CreatedAt,
					UpdatedAt:     c.UpdatedAt,
				})
			}

			return output.Write(f.IOStreams, f.JSON, configs, func(w io.Writer) error {
				t := output.NewTable(w, "NAME", "OWNER", "PROVIDER", "MODEL", "ENABLED", "KEY")
				for _, c := range configs {
					key := "—"
					if c.APIKeyPreview != nil && *c.APIKeyPreview != "" {
						key = *c.APIKeyPreview
					}
					enabled := "false"
					if c.Enabled {
						enabled = "true"
					}
					t.Row(c.Name, c.OwnerType, c.Provider, c.Model, enabled, key)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "Agent ID or URN to narrow the context")
	return cmd
}

// optional returns nil for an empty string so the GraphQL variable is
// omitted rather than sent as an explicit null (lets the server's
// platform-admin "may omit appId" path apply).
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
