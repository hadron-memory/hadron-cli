package schedule

import (
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var app string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List an App's schedules",
		Example: `  hadron schedule ls --app acme.com:ops --json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := cmdutil.ResolveAppRef(f, app)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			items, err := api.CollectAll(func(limit, offset int) ([]*gen.AgentSchedulesAgentSchedulesAgentSchedulesPageItemsAgentSchedule, int, error) {
				resp, err := gen.AgentSchedules(cmd.Context(), client, appRef, &limit, &offset)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.AgentSchedules == nil {
					return nil, 0, nil
				}
				return resp.AgentSchedules.Items, resp.AgentSchedules.Total, nil
			})
			if err != nil {
				return err
			}

			schedules := make([]scheduleDTO, 0, len(items))
			for _, s := range items {
				if s == nil {
					continue
				}
				schedules = append(schedules, dtoFromFields(s.AgentScheduleFields))
			}

			return output.Write(f.IOStreams, f.JSON, schedules, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "NAME", "CRON", "TZ", "ENABLED", "NEXT")
				for _, s := range schedules {
					t.Row(s.ID, s.Name, s.Cron, s.Timezone, strconv.FormatBool(s.Enabled), output.Dash(s.NextRunAt))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "App to list schedules for (ID or URN; defaults to the App context)")
	return cmd
}
