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

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var (
		app, agent, org       string
		name, provider, model string
		apiKey                string
		params                []string
		disabled              bool
	)
	cmd := &cobra.Command{
		Use:   "create (--app | --agent | --org <id-or-urn>) --name <n> --provider <p> --model <m>",
		Short: "Create an AI service config",
		Long: `Create an AI service config owned by an App, Agent, or Organization.

The API key is a secret: pass it on stdin with --api-key - (recommended, keeps
it out of argv and shell history) or inline; it is never echoed back (output is
masked to a preview). Omit --api-key to store a key-less config and set the key
later with 'ai-config update'. --param sets provider knobs (repeatable).`,
		Example: `  printf '%s' "$KEY" | hadron ai-config create --app acme.com:juno-app \
    --name default --provider anthropic --model claude-opus-4-8 --api-key -
  hadron ai-config create --org acme.com --name fast --provider openai \
    --model gpt-4o-mini --param maxTokens=4096`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ownerType, ownerID, err := resolveOwner(app, agent, org)
			if err != nil {
				return err
			}
			paramsJSON, err := cmdutil.KeyValsToJSON(params, "param")
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			var keyArg *string
			if cmd.Flags().Changed("api-key") {
				k, err := resolveSecret(apiKey, f.IOStreams.In)
				if err != nil {
					return err
				}
				if k != "" {
					keyArg = &k
				}
			}
			enabled := !disabled

			resp, err := gen.CreateAiServiceConfig(cmd.Context(), client, name, provider, model, ownerID, ownerType, keyArg, &enabled, paramsJSON)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateAiServiceConfig == nil {
				return exitcode.Newf(exitcode.Error, "server returned no config")
			}
			dto := dtoFromFields(resp.CreateAiServiceConfig.AiServiceConfigFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeConfigLine(w, "✓ created", dto)
			})
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "owning App (ID or URN)")
	cmd.Flags().StringVar(&agent, "agent", "", "owning Agent (ID or URN)")
	cmd.Flags().StringVar(&org, "org", "", "owning Organization (ID or URN)")
	cmd.Flags().StringVar(&name, "name", "", "config name (1-64 chars, [a-z0-9_-], unique per owner)")
	cmd.Flags().StringVar(&provider, "provider", "", "provider id (anthropic, openai, glm, bedrock)")
	cmd.Flags().StringVar(&model, "model", "", "model identifier")
	cmd.Flags().StringVar(&apiKey, "api-key", "", `provider API key ("-" reads stdin)`)
	cmd.Flags().StringArrayVar(&params, "param", nil, "provider param key=value (repeatable; value parsed as JSON or string)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "create the config disabled")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("model")
	return cmd
}
