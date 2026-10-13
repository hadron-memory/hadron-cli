package team

// `team stall-schedule` — an App's stall schedule (#864 over server
// #1706/#1707 and #1863): the four stall windows (cor:agt:050:14–:17) and the
// host-registration lapse count (cor:agt:070:01). `get` is any participant's
// read; `set` is whoever manages the App.

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/api/gqltypes"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type stallScheduleDTO struct {
	AppID                string  `json:"appId"`
	AssignmentMinutes    int     `json:"assignmentMinutes"`
	NotStartedMinutes    int     `json:"notStartedMinutes"`
	NoProgressMinutes    int     `json:"noProgressMinutes"`
	OwnerReactionMinutes int     `json:"ownerReactionMinutes"`
	HostLapseCheckIns    int     `json:"hostLapseCheckIns"`
	UpdatedAt            *string `json:"updatedAt"`
}

func stallScheduleDTOFrom(s *gen.AgentTaskStallScheduleFields) stallScheduleDTO {
	return stallScheduleDTO{
		AppID:                s.AppId,
		AssignmentMinutes:    s.AssignmentMinutes,
		NotStartedMinutes:    s.NotStartedMinutes,
		NoProgressMinutes:    s.NoProgressMinutes,
		OwnerReactionMinutes: s.OwnerReactionMinutes,
		HostLapseCheckIns:    s.HostLapseCheckIns,
		UpdatedAt:            s.UpdatedAt,
	}
}

func newCmdTeamStallSchedule(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stall-schedule <command>",
		Short: "An App's stall windows and host-lapse count",
		Long: `The App's stall schedule: how long a task may sit before each stall alarm
(cor:agt:050:14–:17), and how many missed host check-ins lapse a host's
registration (cor:agt:070:01). ` + "`get`" + ` shows it to any participant;
` + "`set`" + ` changes it and is for whoever manages the App.`,
	}
	cmd.AddCommand(newCmdTeamStallScheduleGet(f))
	cmd.AddCommand(newCmdTeamStallScheduleSet(f))
	return cmd
}

func newCmdTeamStallScheduleGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the App's stall schedule",
		Long:  `Show the App's four stall windows, in minutes, its host-lapse count, in check-ins, and when the App last changed them.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			appLabel := lazyAppLabel(ctx, f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamStallSchedule(ctx, client, scope.Ref)
			if err != nil {
				return api.MapError(err)
			}
			dto := stallScheduleDTOFrom(&resp.AgentTaskStallSchedule.AgentTaskStallScheduleFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				return renderStallSchedule(w, dto)
			})
		},
	}
}

// stallScheduleFlag is one settable field: its flag, its wire key, and the
// setter on the bound input.
type stallScheduleFlag struct {
	name  string
	usage string
	set   func(*gqltypes.AgentTaskStallScheduleInput, json.RawMessage)
}

var stallScheduleFlags = []stallScheduleFlag{
	{"assignment-minutes", "minutes a task may go unassigned before its alarm (cor:agt:050:17), or default",
		func(in *gqltypes.AgentTaskStallScheduleInput, v json.RawMessage) { in.AssignmentMinutes = v }},
	{"not-started-minutes", "minutes an assigned task may go unstarted (cor:agt:050:14), or default",
		func(in *gqltypes.AgentTaskStallScheduleInput, v json.RawMessage) { in.NotStartedMinutes = v }},
	{"no-progress-minutes", "minutes a started task may go without progress (cor:agt:050:15), or default",
		func(in *gqltypes.AgentTaskStallScheduleInput, v json.RawMessage) { in.NoProgressMinutes = v }},
	{"owner-reaction-minutes", "minutes an owner may leave a finished or cancelled child unanswered (cor:agt:050:16), or default",
		func(in *gqltypes.AgentTaskStallScheduleInput, v json.RawMessage) { in.OwnerReactionMinutes = v }},
	{"host-lapse-check-ins", "missed host check-ins before a registration lapses (cor:agt:070:01), or default",
		func(in *gqltypes.AgentTaskStallScheduleInput, v json.RawMessage) { in.HostLapseCheckIns = v }},
}

// stallScheduleValue reads one flag's value onto the wire: a whole number is
// sent as itself, `default` as an explicit null (the server's "return to the
// platform default"). Anything else — an empty value included, which is not
// the same as leaving the flag out — is refused here. Ranges (1–10080 minutes,
// 2–10 check-ins) are the server's rule and its BAD_USER_INPUT says them.
func stallScheduleValue(flag, raw string) (json.RawMessage, error) {
	if strings.EqualFold(raw, "default") {
		return json.RawMessage("null"), nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "--%s takes a whole number or default (the platform default), got %q", flag, raw)
	}
	return json.RawMessage(strconv.Itoa(n)), nil
}

func newCmdTeamStallScheduleSet(f *cmdutil.Factory) *cobra.Command {
	values := make([]string, len(stallScheduleFlags))
	cmd := &cobra.Command{
		Use:   "set [--assignment-minutes N|default] [--not-started-minutes N|default] [--no-progress-minutes N|default] [--owner-reaction-minutes N|default] [--host-lapse-check-ins N|default]",
		Short: "Change the App's stall schedule",
		Long: `Change one or more of the App's stall-schedule values. A flag you leave
out keeps its current value; ` + "`default`" + ` returns that value to the
platform default. Each window is a whole number of minutes from 1 to 10080
(a week); the host-lapse count is a whole number of check-ins from 2 to 10.
Only whoever manages the App may set it (APP_MANAGER_REQUIRED otherwise).

The write is always the person's, never a bound worktree's worker: the
server treats a worker as never managing the App, so attributing it to the
session would refuse every manager working from one.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var input gqltypes.AgentTaskStallScheduleInput
			changed := false
			for i, fl := range stallScheduleFlags {
				if !cmd.Flags().Changed(fl.name) {
					continue
				}
				v, err := stallScheduleValue(fl.name, values[i])
				if err != nil {
					return err
				}
				fl.set(&input, v)
				changed = true
			}
			if !changed {
				return exitcode.Newf(exitcode.Usage, "name at least one value to change: --assignment-minutes, --not-started-minutes, --no-progress-minutes, --owner-reaction-minutes or --host-lapse-check-ins")
			}
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			appLabel := lazyAppLabel(ctx, f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamStallScheduleSet(ctx, client, scope.Ref, &input)
			if err != nil {
				return api.MapError(err)
			}
			dto := stallScheduleDTOFrom(&resp.SetAgentTaskStallSchedule.AgentTaskStallScheduleFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ stall schedule set for %s\n", appLabel()); err != nil {
					return err
				}
				return renderStallSchedule(w, dto)
			})
		},
	}
	for i, fl := range stallScheduleFlags {
		cmd.Flags().StringVar(&values[i], fl.name, "", fl.usage)
	}
	return cmd
}

func renderStallSchedule(w io.Writer, dto stallScheduleDTO) error {
	t := output.NewTable(w, "SETTING", "VALUE", "SPEC")
	t.Row("assignment", fmt.Sprintf("%d min", dto.AssignmentMinutes), "cor:agt:050:17")
	t.Row("not started", fmt.Sprintf("%d min", dto.NotStartedMinutes), "cor:agt:050:14")
	t.Row("no progress", fmt.Sprintf("%d min", dto.NoProgressMinutes), "cor:agt:050:15")
	t.Row("owner reaction", fmt.Sprintf("%d min", dto.OwnerReactionMinutes), "cor:agt:050:16")
	t.Row("host lapse", fmt.Sprintf("%d check-ins", dto.HostLapseCheckIns), "cor:agt:070:01")
	if err := t.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "updated: %s\n", strOr(dto.UpdatedAt))
	return err
}
