package schedule

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
		app, name, cron, tz, entry, aiConfig, agent, policy, runAt, scope string
		args                                                              []string
		asSelf, disabled                                                  bool
		budget                                                            cmdutil.BudgetFlags
	)
	cmd := &cobra.Command{
		Use:   "create --app <ref> --name <n> (--cron '<expr>' | --run-at <RFC3339>) --entry <node-urn>",
		Short: "Create a recurring or one-shot schedule",
		Long: `Create a recurring or one-shot schedule (cor:agt:010). Give exactly one of
--cron (5 fields, evaluated in --tz, default UTC) or --run-at (RFC3339 instant).
--scope names a readable scope by id or name resolved in the App context;
omitted means the App's attached memories. The server snapshots it per run.

--as-self runs on behalf of you (reaches your personal memories; authenticated
user only — an App-key caller gets UNAUTHENTICATED). --arg sets static template
args merged into each run (repeatable). --policy is a trigger-layer allow-list
{ "allow": [...] } (cor:acl:040). --ai-config selects a named AI config.

--budget-tokens / --budget-actions cap each run this schedule spawns (positive whole numbers;
omitted = the platform default). A value above the platform default is refused
by the server. Requires a server with trigger budgets.`,
		Example: `  hadron schedule create --app acme.com:ops --name nightly-digest \
    --cron '0 6 * * *' --tz America/New_York \
    --entry hrn:node:acme.com:ops:tasks:nightly-digest --as-self`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, cmdArgs []string) error {
			if cmd.Flags().Changed("cron") == cmd.Flags().Changed("run-at") {
				return exitcode.Newf(exitcode.Usage, "give exactly one of --cron or --run-at")
			}
			if (cmd.Flags().Changed("cron") && strings.TrimSpace(cron) == "") || (cmd.Flags().Changed("run-at") && strings.TrimSpace(runAt) == "") {
				return exitcode.Newf(exitcode.Usage, "--cron/--run-at cannot be empty")
			}
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
			budgetTokens, budgetActions, err := budget.Values(cmd)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			enabled := !disabled
			input := &gen.CreateAgentScheduleInput{
				AppRef:        appRef,
				Name:          name,
				EntryNodeUrn:  entryURN,
				Enabled:       &enabled,
				EventData:     eventData,
				Policy:        policyJSON,
				BudgetTokens:  budgetTokens,
				BudgetActions: budgetActions,
			}
			if cmd.Flags().Changed("cron") {
				input.Cron = &cron
			}
			if cmd.Flags().Changed("run-at") {
				input.RunAt = &runAt
			}
			if cmd.Flags().Changed("scope") {
				if strings.TrimSpace(scope) == "" {
					return exitcode.Newf(exitcode.Usage, "--scope is empty; omit it for App memories")
				}
				input.ScopeRef = &scope
			}
			if tz != "" {
				input.Timezone = &tz
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

			resp, err := gen.CreateAgentSchedule(cmd.Context(), client, input)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateAgentSchedule == nil {
				return exitcode.Newf(exitcode.Error, "server returned no schedule")
			}
			dto := dtoFromFields(resp.CreateAgentSchedule.AgentScheduleFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeScheduleLine(w, "✓ created", dto)
			})
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "App to run (ID or URN; defaults to the App context)")
	cmd.Flags().StringVar(&name, "name", "", "schedule name (required)")
	cmd.Flags().StringVar(&cron, "cron", "", "5-field cron expression (instead of --run-at)")
	cmd.Flags().StringVar(&tz, "tz", "", "IANA timezone for the cron expression (default UTC)")
	cmd.Flags().StringVar(&entry, "entry", "", "fully-qualified entry node URN (required)")
	cmd.Flags().BoolVar(&asSelf, "as-self", false, "run on behalf of you (authenticated user only)")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "static template arg key=value (repeatable; JSON or string)")
	cmd.Flags().StringVar(&policy, "policy", "", `trigger-layer allow-list JSON, e.g. '{"allow":["comm.outbound"]}'`)
	cmd.Flags().StringVar(&aiConfig, "ai-config", "", "named AI config for the runs (spec-036 walk override)")
	cmd.Flags().StringVar(&agent, "agent", "", "specific installed Agent (ID or URN)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "create the schedule disabled")
	budget.Add(cmd, false)
	_ = cmd.MarkFlagRequired("name")
	cmd.Flags().StringVar(&runAt, "run-at", "", "one-shot RFC3339 instant (instead of --cron)")
	cmd.Flags().StringVar(&scope, "scope", "", "readable scope id or name in the App context")
	_ = cmd.MarkFlagRequired("entry")
	return cmd
}
