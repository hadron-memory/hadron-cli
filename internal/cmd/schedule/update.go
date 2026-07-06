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
		name, cron, tz, entry, aiConfig, policy string
		args                                    []string
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a schedule",
		Long: `Update a schedule. Only the fields you pass change; unset fields are preserved.
--enabled / --enabled=false toggles it; --as-self / --as-self=false toggles the
on-behalf-of-you flag.`,
		Example: `  hadron schedule update sch_123 --cron '0 7 * * *' --enabled=false`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, cmdArgs []string) error {
			input := &gen.UpdateAgentScheduleInput{}
			changed := false
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
	cmd.Flags().StringVar(&policy, "policy", "", `new trigger-layer allow-list JSON (--policy "" clears it)`)
	cmd.Flags().StringArrayVar(&args, "arg", nil, "replace static template args (repeatable key=value)")
	cmd.Flags().Bool("enabled", true, "enable or disable the schedule (--enabled=false to disable)")
	cmd.Flags().Bool("as-self", false, "toggle the on-behalf-of-you flag")
	return cmd
}
