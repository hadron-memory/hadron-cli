package webhook

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var (
		app, name, entry, aiConfig, agent, policy, argsSchema, scope string
		args                                                         []string
		asSelf, disabled                                             bool
		budget                                                       cmdutil.BudgetFlags
	)
	cmd := &cobra.Command{
		Use:   "create --app <ref> --name <n> --entry <node-urn>",
		Short: "Create a webhook trigger (prints the shown-once secret)",
		Long: `Create a webhook: an address that, when something POSTs to it, starts a run of
the entry task node. Use it to let an outside system (a deploy script, a smart
home, a CI job) trigger work.

Creating it prints the URL path and a platform token ONCE — store them now; the
secret is never shown again (lose it and "webhook rotate" issues a new one).
--name is lowercase alphanumeric/dash, 1-64 chars, and is part of the URL.

CALLING IT: POST to the printed path on your Hadron server's host, with the
token in the hpt query parameter, and a JSON object as the body; its keys reach
the entry node as arguments, merged with any --arg values stored here:

  curl -X POST -H 'Content-Type: application/json' -d '{"service":"api"}' \
    "https://<server-host><path>?hpt=<token>"

A started run answers 202 with its run id; follow it with "hadron run get".
--args-schema stores a JSON Schema for the body; it is kept but not yet checked
on each call. --as-self runs on behalf of you (authenticated user only).

--policy narrows what the webhook's runs may do: an allow-list such as
{"allow":["run.execute","comm.outbound"]} naming actions. Left out, the webhook
adds no restriction of its own; the org, user and App policy layers still apply.
See "hadron policy".

--scope sets a readable scope id/name in the App context, snapshotted onto
each run; omitted means the App's attached memories.

--budget-tokens / --budget-actions cap each run this webhook spawns (positive whole numbers;
omitted = the platform default). A value above the platform default is refused
by the server. Requires a server with trigger budgets.`,
		Example: `  hadron webhook create --app acme.com:ops --name deploy-notify \
    --entry hrn:node:acme.com:ops:tasks:on-deploy`,
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
			budgetTokens, budgetActions, err := budget.Values(cmd)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			enabled := !disabled
			input := &gen.CreateAgentWebhookInput{
				AppRef:        appRef,
				Name:          name,
				EntryNodeUrn:  entryURN,
				Enabled:       &enabled,
				EventData:     eventData,
				Policy:        policyJSON,
				ArgsSchema:    schemaJSON,
				BudgetTokens:  budgetTokens,
				BudgetActions: budgetActions,
			}
			if cmd.Flags().Changed("scope") {
				if strings.TrimSpace(scope) == "" {
					return exitcode.Newf(exitcode.Usage, "--scope is empty; omit it for App memories")
				}
				input.ScopeRef = &scope
			}
			if aiConfig != "" {
				input.AiConfigName = &aiConfig
			}
			if agent != "" {
				input.AgentRef = &agent
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
	cmd.Flags().StringVar(&policy, "policy", "", `actions this webhook's runs may do, as JSON, e.g. '{"allow":["comm.outbound"]}' (default: no extra restriction; see "hadron policy")`)
	cmd.Flags().StringVar(&argsSchema, "args-schema", "", "JSON Schema for the POST body (stored, not yet checked on each call)")
	cmd.Flags().StringVar(&aiConfig, "ai-config", "", "named AI config for the runs (see \"hadron ai-config\")")
	cmd.Flags().StringVar(&agent, "agent", "", "specific installed Agent (ID or URN)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "create the webhook disabled")
	cmd.Flags().StringVar(&scope, "scope", "", "readable scope id or name in the App context")
	budget.Add(cmd, false)
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("entry")
	return cmd
}
