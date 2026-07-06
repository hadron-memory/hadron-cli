package webhook

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
		app, name, entry, aiConfig, agent, policy, argsSchema string
		args                                                  []string
		asSelf, disabled                                      bool
	)
	cmd := &cobra.Command{
		Use:   "create --app <ref> --name <n> --entry <node-urn>",
		Short: "Create a webhook trigger (prints the shown-once secret)",
		Long: `Create a webhook trigger (D-2026-05-02). A POST to its URL fires the entry node.

The URL path and platform token are printed ONCE — store them now; the secret is
never queryable again. --name is lowercase alphanumeric/dash, 1-64 chars, and is
part of the URL. --args-schema stores a JSON Schema for POST args (enforcement is
follow-on). --as-self runs on behalf of you (authenticated user only).`,
		Example: `  hadron webhook create --app acme.com:ops --name deploy-notify \
    --entry acme.com::ops::tasks:on-deploy`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, cmdArgs []string) error {
			appRef, err := cmdutil.ResolveAppRef(f, app)
			if err != nil {
				return err
			}
			entryURN, err := cmdutil.CanonicalNodeURN(entry)
			if err != nil {
				return err
			}
			eventData, err := cmdutil.KeyValsToJSON(args, "arg")
			if err != nil {
				return err
			}
			policyJSON, err := cmdutil.ParseJSONArg(policy, "policy")
			if err != nil {
				return err
			}
			schemaJSON, err := cmdutil.ParseJSONArg(argsSchema, "args-schema")
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			enabled := !disabled
			input := &gen.CreateAgentWebhookInput{
				AppId:        appRef,
				Name:         name,
				EntryNodeUrn: entryURN,
				Enabled:      &enabled,
				EventData:    eventData,
				Policy:       policyJSON,
				ArgsSchema:   schemaJSON,
			}
			if aiConfig != "" {
				input.AiConfigName = &aiConfig
			}
			if agent != "" {
				input.AgentId = &agent
			}
			if asSelf {
				input.RunAsSelf = &asSelf
			}

			resp, err := gen.CreateAgentWebhook(cmd.Context(), client, input)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateAgentWebhook == nil || resp.CreateAgentWebhook.Webhook == nil {
				return exitcode.Newf(exitcode.Error, "server returned incomplete webhook credentials")
			}
			dto := credsFromFields(resp.CreateAgentWebhook.AgentWebhookCredentialFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeCredentials(w, "created", dto)
			})
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "App to run (ID or URN; defaults to the App context)")
	cmd.Flags().StringVar(&name, "name", "", "webhook name — lowercase alphanumeric/dash, part of the URL (required)")
	cmd.Flags().StringVar(&entry, "entry", "", "fully-qualified entry node URN (required)")
	cmd.Flags().BoolVar(&asSelf, "as-self", false, "run on behalf of you (authenticated user only)")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "static template arg key=value (repeatable; JSON or string)")
	cmd.Flags().StringVar(&policy, "policy", "", "trigger-layer allow-list JSON (cor:acl:040)")
	cmd.Flags().StringVar(&argsSchema, "args-schema", "", "JSON Schema for POST args (stored; enforcement follow-on)")
	cmd.Flags().StringVar(&aiConfig, "ai-config", "", "named AI config for the runs (spec-036 walk override)")
	cmd.Flags().StringVar(&agent, "agent", "", "specific installed Agent (ID or URN)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "create the webhook disabled")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("entry")
	return cmd
}
