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
		apiKey                string
		params                []string
		enabled               bool
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an AI service config",
		Long: `Update an AI service config by id. Only the fields you pass change.

--api-key sets a new key ("-" reads stdin); --api-key "" clears the stored key;
omit it to keep the current key. --param replaces the whole params object.
Find ids with 'hadron ai-config ls --json'.`,
		Example: `  hadron ai-config update cfg_123 --model claude-opus-4-8
  printf '%s' "$KEY" | hadron ai-config update cfg_123 --api-key -
  hadron ai-config update cfg_123 --api-key ""        # clear the key
  hadron ai-config update cfg_123 --enabled=false`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("name") && !changed("provider") && !changed("model") &&
				!changed("api-key") && !changed("param") && !changed("enabled") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}
			paramsJSON, err := parseParams(params)
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
	cmd.Flags().StringArrayVar(&params, "param", nil, "replace params with key=value (repeatable)")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "enable/disable the config (e.g. --enabled=false)")
	return cmd
}
