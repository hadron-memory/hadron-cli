package team

// `team stall-schedule` — an App's stall schedule (#864 over server
// #1706/#1707 and #1863): the four stall windows (cor:agt:050:14–:17), the
// host-registration lapse count (cor:agt:070:01), and (#983) the session-end
// grace (cor:agt:050:10) and host report timeout (cor:agt:070:02). `get` is
// any participant's read; `set` is whoever manages the App.

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
	AppID                string `json:"appId"`
	AssignmentMinutes    int    `json:"assignmentMinutes"`
	NotStartedMinutes    int    `json:"notStartedMinutes"`
	NoProgressMinutes    int    `json:"noProgressMinutes"`
	OwnerReactionMinutes int    `json:"ownerReactionMinutes"`
	HostLapseCheckIns    int    `json:"hostLapseCheckIns"`
	// #983: effective values as the server returns them (defaults 10 and 60
	// today) — never computed here.
	SessionEndGraceMinutes   int     `json:"sessionEndGraceMinutes"`
	HostReportTimeoutMinutes int     `json:"hostReportTimeoutMinutes"`
	UpdatedAt                *string `json:"updatedAt"`
}

func stallScheduleDTOFrom(s *gen.AgentTaskStallScheduleFields) stallScheduleDTO {
	return stallScheduleDTO{
		AppID:                    s.AppId,
		AssignmentMinutes:        s.AssignmentMinutes,
		NotStartedMinutes:        s.NotStartedMinutes,
		NoProgressMinutes:        s.NoProgressMinutes,
		OwnerReactionMinutes:     s.OwnerReactionMinutes,
		HostLapseCheckIns:        s.HostLapseCheckIns,
		SessionEndGraceMinutes:   s.SessionEndGraceMinutes,
		HostReportTimeoutMinutes: s.HostReportTimeoutMinutes,
		UpdatedAt:                s.UpdatedAt,
	}
}

func newCmdTeamStallSchedule(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stall-schedule <command>",
		Short: "An App's stall windows, host timings and session-end grace",
		Long: `The App's stall schedule: how long a task may sit before each stall alarm
(cor:agt:050:14–:17), how many missed host check-ins lapse a host's
registration (cor:agt:070:01), how long a host has to report an operation it
collected (cor:agt:070:02), and how long a worker asked to end its session
has before the platform ends it (cor:agt:050:10). Each has a platform
default the App can change. ` + "`get`" + ` shows it to any participant;
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
		Long:  `Show the App's four stall windows, in minutes, its host-lapse count, in check-ins, its host report timeout and session-end grace, in minutes, and when the App last changed them. Every value is the one the server returns, the platform default included.`,
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
// setter on the bound input. min/max, when set (#983), are that field's own
// range, checked before any call; a zero max leaves the range to the server,
// as the windows and the lapse count always have.
type stallScheduleFlag struct {
	name     string
	usage    string
	set      func(*gqltypes.AgentTaskStallScheduleInput, json.RawMessage)
	min, max int
}

var stallScheduleFlags = []stallScheduleFlag{
	{"assignment-minutes", "minutes a task may go unassigned before its alarm (cor:agt:050:17), or default",
		func(in *gqltypes.AgentTaskStallScheduleInput, v json.RawMessage) { in.AssignmentMinutes = v }, 0, 0},
	{"not-started-minutes", "minutes an assigned task may go unstarted (cor:agt:050:14), or default",
		func(in *gqltypes.AgentTaskStallScheduleInput, v json.RawMessage) { in.NotStartedMinutes = v }, 0, 0},
	{"no-progress-minutes", "minutes a started task may go without progress (cor:agt:050:15), or default",
		func(in *gqltypes.AgentTaskStallScheduleInput, v json.RawMessage) { in.NoProgressMinutes = v }, 0, 0},
	{"owner-reaction-minutes", "minutes an owner may leave a finished or cancelled child unanswered (cor:agt:050:16), or default",
		func(in *gqltypes.AgentTaskStallScheduleInput, v json.RawMessage) { in.OwnerReactionMinutes = v }, 0, 0},
	{"host-lapse-check-ins", "missed host check-ins before a registration lapses (cor:agt:070:01), or default",
		func(in *gqltypes.AgentTaskStallScheduleInput, v json.RawMessage) { in.HostLapseCheckIns = v }, 0, 0},
	{"host-report-timeout-minutes", "minutes a host has to report an operation it collected (cor:agt:070:02; 5–1440, platform default 60), or default",
		func(in *gqltypes.AgentTaskStallScheduleInput, v json.RawMessage) { in.HostReportTimeoutMinutes = v }, 5, 1440},
	{"session-end-grace-minutes", "minutes a worker asked to end its session has before the platform ends it (cor:agt:050:10; 1–1440, platform default 10), or default",
		func(in *gqltypes.AgentTaskStallScheduleInput, v json.RawMessage) { in.SessionEndGraceMinutes = v }, 1, 1440},
}

// stallScheduleValue reads one flag's value onto the wire: a whole number is
// sent as itself, `default` as an explicit null (the server's "return to the
// platform default"). Anything else — an empty value included, which is not
// the same as leaving the flag out — is refused here. For the windows and
// the lapse count the range (1–10080 minutes, 2–10 check-ins) is the
// server's rule and its BAD_USER_INPUT says it; a flag with its own range
// (#983) is held to it here, so an out-of-range value sends nothing.
func stallScheduleValue(fl stallScheduleFlag, raw string) (json.RawMessage, error) {
	if strings.EqualFold(raw, "default") {
		return json.RawMessage("null"), nil
	}
	n, err := strconv.Atoi(raw)
	if fl.max > 0 && (err != nil || n < fl.min || n > fl.max) {
		return nil, exitcode.Newf(exitcode.Usage, "--%s takes a whole number of minutes from %d to %d, or default (the platform default), got %q", fl.name, fl.min, fl.max, raw)
	}
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "--%s takes a whole number or default (the platform default), got %q", fl.name, raw)
	}
	return json.RawMessage(strconv.Itoa(n)), nil
}

func newCmdTeamStallScheduleSet(f *cmdutil.Factory) *cobra.Command {
	values := make([]string, len(stallScheduleFlags))
	cmd := &cobra.Command{
		Use:   "set [--assignment-minutes N|default] [--not-started-minutes N|default] [--no-progress-minutes N|default] [--owner-reaction-minutes N|default] [--host-lapse-check-ins N|default] [--host-report-timeout-minutes N|default] [--session-end-grace-minutes N|default]",
		Short: "Change the App's stall schedule",
		Long: `Change one or more of the App's stall-schedule values. A flag you leave
out keeps its current value; ` + "`default`" + ` returns that value to the
platform default. Each window is a whole number of minutes from 1 to 10080
(a week); the host-lapse count is a whole number of check-ins from 2 to 10;
the host report timeout is 5 to 1440 minutes and the session-end grace 1 to
1440 minutes. Those last two are checked here, before anything is sent, so a
set with any value out of its range changes nothing.
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
				v, err := stallScheduleValue(fl, values[i])
				if err != nil {
					return err
				}
				fl.set(&input, v)
				changed = true
			}
			if !changed {
				return exitcode.Newf(exitcode.Usage, "name at least one value to change: --assignment-minutes, --not-started-minutes, --no-progress-minutes, --owner-reaction-minutes, --host-lapse-check-ins, --host-report-timeout-minutes or --session-end-grace-minutes")
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
	t.Row("host report timeout", fmt.Sprintf("%d min", dto.HostReportTimeoutMinutes), "cor:agt:070:02")
	t.Row("session-end grace", fmt.Sprintf("%d min", dto.SessionEndGraceMinutes), "cor:agt:050:10")
	if err := t.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "updated: %s\n", strOr(dto.UpdatedAt))
	return err
}
