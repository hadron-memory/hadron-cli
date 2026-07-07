package schedule

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
		app, name, cron, tz, entry, aiConfig, agent, policy string
		args                                                []string
		asSelf, disabled                                    bool
	)
	cmd := &cobra.Command{
		Use:   "create --app <ref> --name <n> --cron '<expr>' --entry <node-urn>",
		Short: "Create a recurring schedule",
		Long: `Create a recurring schedule (cor:agt:010). --cron is a 5-field cron expression
evaluated in --tz (default UTC).

--as-self runs on behalf of you (reaches your personal memories; authenticated
user only — an App-key caller gets UNAUTHENTICATED). --arg sets static template
args merged into each run (repeatable). --policy is a trigger-layer allow-list
{ "allow": [...] } (cor:acl:040). --ai-config selects a named AI config.`,
		Example: `  hadron schedule create --app acme.com:ops --name nightly-digest \
    --cron '0 6 * * *' --tz America/New_York \
    --entry acme.com::ops::tasks:nightly-digest --as-self`,
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
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			enabled := !disabled
			input := &gen.CreateAgentScheduleInput{
				AppRef:       appRef,
				Name:         name,
				Cron:         &cron,
				EntryNodeUrn: entryURN,
				Enabled:      &enabled,
				EventData:    eventData,
				Policy:       policyJSON,
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
	cmd.Flags().StringVar(&cron, "cron", "", "5-field cron expression (required)")
	cmd.Flags().StringVar(&tz, "tz", "", "IANA timezone for the cron expression (default UTC)")
	cmd.Flags().StringVar(&entry, "entry", "", "fully-qualified entry node URN (required)")
	cmd.Flags().BoolVar(&asSelf, "as-self", false, "run on behalf of you (authenticated user only)")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "static template arg key=value (repeatable; JSON or string)")
	cmd.Flags().StringVar(&policy, "policy", "", `trigger-layer allow-list JSON, e.g. '{"allow":["comm.outbound"]}'`)
	cmd.Flags().StringVar(&aiConfig, "ai-config", "", "named AI config for the runs (spec-036 walk override)")
	cmd.Flags().StringVar(&agent, "agent", "", "specific installed Agent (ID or URN)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "create the schedule disabled")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("cron")
	_ = cmd.MarkFlagRequired("entry")
	return cmd
}
