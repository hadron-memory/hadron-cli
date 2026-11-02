package aiconfig

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var (
		name, provider, model string
		apiKey, endpoint      string
		apiStyle              string
		clearAPIStyle         bool
		params                []string
		enabled               bool
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an AI service config",
		Long: `Update an AI service config by id. Only the fields you pass change.

--api-key sets a new key ("-" reads stdin); --api-key "" clears the stored key;
omit it to keep the current key. --param replaces the whole params object.
--endpoint sets a provider base URL; --endpoint "" clears the stored override
and returns to the server's provider default. Omitting --endpoint preserves it.
--api-style sets CHAT_COMPLETIONS or RESPONSES for an OpenAI-compatible
provider. --clear-api-style sends null to restore the provider default;
these flags exclude each other. Omitting both preserves the stored style.
The server decides which providers accept a style. Style-aware JSON includes
stored apiStyle and effectiveApiStyle plus the masked config and endpoints.
Find ids with 'hadron ai-config list --json', or list by --owner-type/--owner-id.`,
		Example: `  hadron ai-config update cfg_123 --model claude-opus-4-8
  printf '%s' "$KEY" | hadron ai-config update cfg_123 --api-key -
  hadron ai-config update cfg_123 --api-key ""        # clear the key
  hadron ai-config update cfg_123 --endpoint ""     # use provider default
  hadron ai-config update cfg_123 --enabled=false
  hadron ai-config update cfg_123 --api-style RESPONSES
  hadron ai-config update cfg_123 --clear-api-style`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if changed("api-style") && changed("clear-api-style") {
				return exitcode.Newf(exitcode.Usage, "--api-style and --clear-api-style are mutually exclusive")
			}
			if changed("clear-api-style") && !clearAPIStyle {
				return exitcode.Newf(exitcode.Usage, "--clear-api-style=false makes no change; omit the flag to preserve the style")
			}
			var styleArg gen.AiApiStyle
			if changed("api-style") {
				var err error
				styleArg, err = parseAPIStyle(apiStyle)
				if err != nil {
					return err
				}
			}
			if !changed("name") && !changed("provider") && !changed("model") &&
				!changed("api-key") && !changed("param") && !changed("enabled") && !changed("endpoint") && !changed("api-style") && !clearAPIStyle {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}
			paramsJSON, err := cmdutil.KeyValsToJSON(params, "param")
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			var nameArg, providerArg, modelArg, keyArg *string
			if changed("name") {
				nameArg = &name
			}
			if changed("provider") {
				providerArg = &provider
			}
			if changed("model") {
				modelArg = &model
			}
			if changed("api-key") {
				k, err := resolveSecret(apiKey, f.IOStreams.In)
				if err != nil {
					return err
				}
				keyArg = &k // empty string clears; non-empty replaces
			}
			var enabledArg *bool
			if changed("enabled") {
				enabledArg = &enabled
			}

			if changed("api-style") || clearAPIStyle {
				var endpointArg *string
				if changed("endpoint") {
					endpointArg = &endpoint
				}
				if clearAPIStyle {
					resp, err := gen.ClearAiServiceConfigApiStyle(cmd.Context(), client, args[0], nameArg, providerArg, modelArg, keyArg, enabledArg, paramsJSON, endpointArg)
					if err != nil {
						return api.MapError(err)
					}
					if resp.UpdateAiServiceConfig == nil {
						return exitcode.Newf(exitcode.Error, "server returned no config")
					}
					return writeStyleConfig(f, "✓ updated", resp.UpdateAiServiceConfig.AiServiceConfigStyleFields)
				}
				resp, err := gen.UpdateAiServiceConfigWithApiStyle(cmd.Context(), client, args[0], styleArg, nameArg, providerArg, modelArg, keyArg, enabledArg, paramsJSON, endpointArg)
				if err != nil {
					return api.MapError(err)
				}
				if resp.UpdateAiServiceConfig == nil {
					return exitcode.Newf(exitcode.Error, "server returned no config")
				}
				return writeStyleConfig(f, "✓ updated", resp.UpdateAiServiceConfig.AiServiceConfigStyleFields)
			}
			if changed("endpoint") {
				resp, err := gen.UpdateAiServiceConfigWithEndpoint(cmd.Context(), client, args[0], nameArg, providerArg, modelArg, keyArg, enabledArg, paramsJSON, &endpoint)
				if err != nil {
					return mapEndpointError(err)
				}
				if resp.UpdateAiServiceConfig == nil {
					return exitcode.Newf(exitcode.Error, "server returned no config")
				}
				dto := dtoFromEndpointFields(resp.UpdateAiServiceConfig.AiServiceConfigEndpointFields)
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					if err := writeConfigLine(w, "✓ updated", dto.aiConfigDTO); err != nil {
						return err
					}
					return writeEndpointLines(w, dto)
				})
			}
			resp, err := gen.UpdateAiServiceConfig(cmd.Context(), client, args[0], nameArg, providerArg, modelArg, keyArg, enabledArg, paramsJSON)
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateAiServiceConfig == nil {
				return exitcode.Newf(exitcode.Error, "server returned no config")
			}
			dto := dtoFromFields(resp.UpdateAiServiceConfig.AiServiceConfigFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeConfigLine(w, "✓ updated", dto)
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new config name")
	cmd.Flags().StringVar(&provider, "provider", "", "new provider id")
	cmd.Flags().StringVar(&model, "model", "", "new model identifier")
	cmd.Flags().StringVar(&apiKey, "api-key", "", `new API key ("-" reads stdin; "" clears)`)
	cmd.Flags().StringVar(&endpoint, "endpoint", "", `provider base URL ("" clears to the server default)`)
	cmd.Flags().StringVar(&apiStyle, "api-style", "", "OpenAI-compatible wire protocol: CHAT_COMPLETIONS or RESPONSES; omit to preserve")
	cmd.Flags().BoolVar(&clearAPIStyle, "clear-api-style", false, "clear the stored API style to the provider default (sends null)")
	cmd.Flags().StringArrayVar(&params, "param", nil, "replace params with key=value (repeatable)")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "enable/disable the config (e.g. --enabled=false)")
	return cmd
}
