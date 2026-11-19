package schedule

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var (
		name, cron, tz, entry, aiConfig, policy, runAt, scope, agent string
		args                                                         []string
		budget                                                       cmdutil.BudgetFlags
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a schedule",
		Long: `Update a schedule. Only the fields you pass change; unset fields are preserved.
--enabled turns the schedule on and --enabled=false turns it off; --as-self runs
it on behalf of you and --as-self=false stops that. Leave a flag out to keep
the schedule's current value.

--run-at switches to one-shot and clears cron; --cron switches to recurring.
Pass at most one per call. --scope sets a scope id/name in the App context;
--scope "" restores App memories. --agent selects an installed Agent.
Scope is snapshotted onto each new run; existing runs do not change.

--budget-tokens / --budget-actions set the per-run limits; --clear-budget-tokens /
--clear-budget-actions clear one back to the platform default (a value and its
clear flag together is a usage error). A value above the platform default is
refused by the server. Needs a server version that supports budgets.`,
		Example: `  hadron schedule update sch_123 --cron '0 7 * * *' --enabled=false`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, cmdArgs []string) error {
			if cmd.Flags().Changed("cron") && cmd.Flags().Changed("run-at") {
				return exitcode.Newf(exitcode.Usage, "--cron and --run-at are mutually exclusive")
			}
			if (cmd.Flags().Changed("cron") && strings.TrimSpace(cron) == "") || (cmd.Flags().Changed("run-at") && strings.TrimSpace(runAt) == "") || (cmd.Flags().Changed("agent") && strings.TrimSpace(agent) == "") {
				return exitcode.Newf(exitcode.Usage, "--cron, --run-at and --agent cannot be empty")
			}
			input := &gen.UpdateAgentScheduleInput{}
			changed := false
			if cmd.Flags().Changed("run-at") {
				input.RunAt = &runAt
				changed = true
			}
			if cmd.Flags().Changed("scope") {
				input.ScopeRef = &scope
				changed = true
			}
			if cmd.Flags().Changed("agent") {
				input.AgentRef = &agent
				changed = true
			}
			if cmd.Flags().Changed("name") {
				input.Name = &name
				changed = true
			}
			if cmd.Flags().Changed("cron") {
				input.Cron = &cron
				changed = true
			}
			if cmd.Flags().Changed("tz") {
				input.Timezone = &tz
				changed = true
			}
			if cmd.Flags().Changed("entry") {
				entryURN, err := cmdutil.CanonicalNodeURN(entry)
				if err != nil {
					return err
				}
				input.EntryNodeUrn = &entryURN
				changed = true
			}
			if cmd.Flags().Changed("ai-config") {
				input.AiConfigName = &aiConfig
				changed = true
			}
			if cmd.Flags().Changed("policy") {
				// --policy "" clears the policy (explicit null); a non-empty
				// value sets it. ParseJSONArg's empty→nil "omit" would otherwise
				// make --policy "" a silent no-op.
				if strings.TrimSpace(policy) == "" {
					clear := json.RawMessage("null")
					input.Policy = &clear
				} else {
					policyJSON, err := cmdutil.ParseJSONArg(policy, "policy")
					if err != nil {
						return err
					}
					input.Policy = policyJSON
				}
				changed = true
			}
			if cmd.Flags().Changed("arg") {
				eventData, err := cmdutil.KeyValsToJSON(args, "arg")
				if err != nil {
					return err
				}
				input.EventData = eventData
				changed = true
			}
			if cmd.Flags().Changed("enabled") {
				v, _ := cmd.Flags().GetBool("enabled")
				input.Enabled = &v
				changed = true
			}
			if cmd.Flags().Changed("as-self") {
				v, _ := cmd.Flags().GetBool("as-self")
				input.RunAsSelf = &v
				changed = true
			}
			bt, ba, budgetChanged, err := budget.UpdateValues(cmd)
			if err != nil {
				return err
			}
			if budgetChanged {
				input.BudgetTokens, input.BudgetActions = bt, ba
				changed = true
			}
			if !changed {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field to change")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UpdateAgentSchedule(cmd.Context(), client, cmdArgs[0], input)
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateAgentSchedule == nil {
				return exitcode.Newf(exitcode.Error, "server returned no schedule")
			}
			dto := dtoFromFields(resp.UpdateAgentSchedule.AgentScheduleFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeScheduleLine(w, "✓ updated", dto)
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&cron, "cron", "", "new 5-field cron expression")
	cmd.Flags().StringVar(&tz, "tz", "", "new IANA timezone")
	cmd.Flags().StringVar(&entry, "entry", "", "new fully-qualified entry node URN")
	cmd.Flags().StringVar(&aiConfig, "ai-config", "", "new named AI config")
	cmd.Flags().StringVar(&policy, "policy", "", `new allow-list of actions the runs may do, as JSON (--policy "" clears it, so the schedule adds no restriction of its own; see "hadron policy")`)
	cmd.Flags().StringArrayVar(&args, "arg", nil, "replace static template args (repeatable key=value)")
	cmd.Flags().Bool("enabled", false, "turn the schedule on; --enabled=false turns it off (left out: unchanged)")
	cmd.Flags().Bool("as-self", false, "run on behalf of you; --as-self=false stops that (left out: unchanged)")
	cmd.Flags().StringVar(&runAt, "run-at", "", "switch to one-shot at this RFC3339 instant (not with --cron)")
	cmd.Flags().StringVar(&scope, "scope", "", "scope id/name; an empty string restores App memories")
	cmd.Flags().StringVar(&agent, "agent", "", "installed Agent id or URN")
	budget.Add(cmd, true)
	return cmd
}
