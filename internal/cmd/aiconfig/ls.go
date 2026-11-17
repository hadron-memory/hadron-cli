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
	var withEndpoints bool
	var masked maskedListFlags
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List resolved AI configs, or masked configs by owner",
		Long: `List the masked set of AI service configs resolvable in an App's chat
context — every distinct config name a chat could select, deduped with the
innermost owner winning (App → Agent → Org → HadronServer), enabled only.

Output is masked: never key material, only hasApiKey and a short apiKeyPreview.

--app defaults to the configured App context (hadron app use / global --app);
you must be a member of that App. --agent narrows to an agent installed in it.
Both accept an ID or a URN. --with-endpoints adds the stored endpoint and the
server-resolved effective endpoint; it requires a server with endpoint support.
The default list remains usable with older servers.

For the uniform management list (including disabled configs), pass both
--owner-type and --owner-id. Owner types: APP, AGENT, ORGANIZATION,
HADRON_SERVER. The owner accepts an ID or URN (HADRON_SERVER: ID only).
Or pass --all-owners for the platform ADMIN/OWNER cross-owner view. The
server decides owner permissions; non-admin callers must name an owner.
These modes use aiServiceConfigs, not the resolved chat-context picker.
The global --app remains context; it never supplies this owner filter.
--agent and --with-endpoints describe the resolved mode; the management
page already includes endpoint and API style fields in JSON.

The management list is one name-ascending page (id breaks ties). --limit
defaults to the server's 20, cap 200; --limit 0 returns only the count.
--offset defaults to 0; increase it by the number of returned items to
continue. JSON is {items:[],total:N}; resolved-mode JSON remains an array.
Both views are masked: only hasApiKey and apiKeyPreview, never the key.`,
		Example: `  hadron ai-config list --app acme.com:juno-app
  hadron ai-config list --app acme.com:juno-app --agent acme.com:juno --json
  hadron ai-config list --owner-type APP --owner-id acme.com:juno-app --json
  hadron ai-config list --all-owners --limit 0 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if masked.requested(cmd) {
				return masked.run(cmd, f)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			appCtx, err := f.App()
			if err != nil {
				return err
			}
			if withEndpoints {
				configs := make([]urnConfigDTO, 0)
				resp, err := gen.ResolveAiServiceConfigsWithEndpointAndUrn(cmd.Context(), client, optional(appCtx), optional(agent))
				switch {
				case err == nil:
					for _, c := range resp.ResolveAiServiceConfigs {
						if c != nil {
							configs = append(configs, urnConfigDTO{endpointConfigDTO: dtoFromEndpointFields(c.AiServiceConfigEndpointFields), URN: c.Urn})
						}
					}
				case isMissingURNField(err):
					old, oldErr := gen.ResolveAiServiceConfigsWithEndpoint(cmd.Context(), client, optional(appCtx), optional(agent))
					if oldErr != nil {
						return mapEndpointError(oldErr)
					}
					for _, c := range old.ResolveAiServiceConfigs {
						if c != nil {
							configs = append(configs, urnConfigDTO{endpointConfigDTO: dtoFromEndpointFields(c.AiServiceConfigEndpointFields)})
						}
					}
				default:
					return mapEndpointError(err)
				}
				return output.Write(f.IOStreams, f.JSON, configs, func(w io.Writer) error {
					showURN := false
					for _, c := range configs {
						showURN = showURN || hasURN(c.URN)
					}
					header := []string{"NAME", "OWNER", "PROVIDER", "MODEL", "ENABLED", "KEY", "STORED ENDPOINT", "EFFECTIVE ENDPOINT"}
					if showURN {
						header = append(header, "URN")
					}
					t := output.NewTable(w, header...)
					for _, c := range configs {
						key := endpointDisplay(c.APIKeyPreview, "—")
						enabled := "false"
						if c.Enabled {
							enabled = "true"
						}
						row := []string{output.SafeCell(c.Name), output.SafeCell(c.OwnerType), output.SafeCell(c.Provider), output.SafeCell(c.Model), output.SafeCell(enabled), output.SafeCell(key),
							output.SafeCell(endpointDisplay(c.Endpoint, "provider default")), output.SafeCell(endpointDisplay(c.EffectiveEndpoint, "server-derived"))}
						if showURN {
							row = append(row, output.SafeCell(urnCell(c.URN)))
						}
						t.Row(row...)
					}
					return t.Flush()
				})
			}
			configs := make([]urnMaskedDTO, 0)
			resp, err := gen.ResolveAiServiceConfigsWithUrn(cmd.Context(), client, optional(appCtx), optional(agent))
			switch {
			case err == nil:
				for _, c := range resp.ResolveAiServiceConfigs {
					if c == nil {
						continue
					}
					configs = append(configs, urnMaskedDTO{aiConfigDTO: aiConfigDTO{
						ID: c.Id, Name: c.Name, OwnerType: string(c.OwnerType), OwnerID: c.OwnerId,
						Provider: c.Provider, Model: c.Model, HasAPIKey: c.HasApiKey,
						APIKeyPreview: c.ApiKeyPreview, Params: c.Params, Enabled: c.Enabled,
						CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
					}, URN: c.Urn})
				}
			case isMissingURNField(err):
				old, oldErr := gen.ResolveAiServiceConfigs(cmd.Context(), client, optional(appCtx), optional(agent))
				if oldErr != nil {
					return api.MapError(oldErr)
				}
				for _, c := range old.ResolveAiServiceConfigs {
					if c == nil {
						continue
					}
					configs = append(configs, urnMaskedDTO{aiConfigDTO: aiConfigDTO{
						ID: c.Id, Name: c.Name, OwnerType: string(c.OwnerType), OwnerID: c.OwnerId,
						Provider: c.Provider, Model: c.Model, HasAPIKey: c.HasApiKey,
						APIKeyPreview: c.ApiKeyPreview, Params: c.Params, Enabled: c.Enabled,
						CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
					}})
				}
			default:
				return api.MapError(err)
			}

			return output.Write(f.IOStreams, f.JSON, configs, func(w io.Writer) error {
				showURN := false
				for _, c := range configs {
					showURN = showURN || hasURN(c.URN)
				}
				header := []string{"NAME", "OWNER", "PROVIDER", "MODEL", "ENABLED", "KEY"}
				if showURN {
					header = append(header, "URN")
				}
				t := output.NewTable(w, header...)
				for _, c := range configs {
					key := "—"
					if c.APIKeyPreview != nil && *c.APIKeyPreview != "" {
						key = *c.APIKeyPreview
					}
					enabled := "false"
					if c.Enabled {
						enabled = "true"
					}
					row := []string{output.SafeCell(c.Name), output.SafeCell(c.OwnerType), output.SafeCell(c.Provider), output.SafeCell(c.Model), output.SafeCell(enabled), output.SafeCell(key)}
					if showURN {
						row = append(row, output.SafeCell(urnCell(c.URN)))
					}
					t.Row(row...)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "Agent ID or URN to narrow the context")
	cmd.Flags().BoolVar(&withEndpoints, "with-endpoints", false, "include configured and server-resolved endpoints (requires newer server)")
	cmd.Flags().StringVar(&masked.ownerType, "owner-type", "", "masked owner list: APP, AGENT, ORGANIZATION or HADRON_SERVER (with --owner-id)")
	cmd.Flags().StringVar(&masked.ownerID, "owner-id", "", "masked list owner ID or URN (HADRON_SERVER: ID only)")
	cmd.Flags().BoolVar(&masked.allOwners, "all-owners", false, "masked cross-owner list; server requires platform ADMIN/OWNER")
	cmd.Flags().IntVar(&masked.limit, "limit", 20, "masked list page size (server default 20, cap 200; 0 counts only)")
	cmd.Flags().IntVar(&masked.offset, "offset", 0, "masked list offset (server default 0)")
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
