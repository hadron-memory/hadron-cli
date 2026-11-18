package team

// `team alarms` — the per-task join of a team's alarms (cli#1829): each alarm
// in effect beside its task's handle, title and status, the assignee and the
// assignee's roster state. Client-side composition over reads that already
// exist (the roster, the App's tasks, the alarm stream); the server decides
// what "in effect" means (cor:agt:050:20) and the CLI never re-derives it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// alarmRosterDTO is the assignee's roster entry exactly as the server returns
// it (cor:agt:050:22): state is the server's TeamRosterState, never relabelled.
// "No session" is checkStatus NO_SESSION (with its reason) or liveSession false.
type alarmRosterDTO struct {
	State       string           `json:"state"`
	Bound       bool             `json:"bound"`
	LiveSession bool             `json:"liveSession"`
	CheckStatus *channelCheckDTO `json:"checkStatus"`
}

type alarmTaskDTO struct {
	ID     string `json:"id"`
	Handle string `json:"handle"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type teamAlarmDTO struct {
	ID                string        `json:"id"`
	Type              string        `json:"type"`
	Severity          string        `json:"severity"`
	CreatedAt         string        `json:"createdAt"`
	AgeSeconds        int64         `json:"ageSeconds"`
	AcknowledgedAt    *string       `json:"acknowledgedAt"`
	AcknowledgedBy    *taskPartyDTO `json:"acknowledgedBy"`
	EscalationEndedAt *string       `json:"escalationEndedAt"`
	Task              alarmTaskDTO  `json:"task"`
	Assignee          *taskPartyDTO `json:"assignee"`
	// AssigneeRoster is null for a task with no assignee, a user assignee, or
	// a worker the roster does not list (it lists active workers only).
	AssigneeRoster *alarmRosterDTO `json:"assigneeRoster"`
	// With --all only: the task's acknowledged alarms and the age of its last
	// recorded activity — an acknowledgement stops the climb, not the stall.
	TaskAcknowledgedAlarms *int   `json:"taskAcknowledgedAlarms,omitempty"`
	TaskActivityAgeSeconds *int64 `json:"taskActivityAgeSeconds,omitempty"`
}

type teamAlarmsDTO struct {
	AppID string `json:"appId"`
	// Scope is "team" (with Coordinator) or "app" (every task of the App).
	Scope             string         `json:"scope"`
	Coordinator       *taskPartyDTO  `json:"coordinator"`
	CoordinatorSource string         `json:"coordinatorSource,omitempty"`
	All               bool           `json:"all"`
	Alarms            []teamAlarmDTO `json:"alarms"`
	// Incomplete is present only when the alarm walk stopped early.
	Incomplete *alarmCountsDTO `json:"incomplete,omitempty"`
}

func newCmdTeamAlarms(f *cmdutil.Factory) *cobra.Command {
	var team, since string
	var all bool
	var types, statuses []string
	cmd := &cobra.Command{
		Use:   "alarms [--team[=worker:<ref>]] [--all] [--type <t>,...] [--status <s>,...] [--since <time>]",
		Short: "Alarms in effect on a team's tasks, with each task and its assignee's roster state",
		Long: `List the task-stall alarms IN EFFECT, one row per alarm, each beside its task's
handle, title and status, the assignee, and the assignee's roster state: the
walk from task list to per-task events to task get to roster, in one command.

SCOPE is the one "team" means in ` + "`team task list --team`" + `: bare --team is the bound
worker's team, --team=worker:<ref> another coordinator's. Without --team the
view is every task of the App.

IN EFFECT is the server's answer (cor:agt:050:20), the same filter as
` + "`team events --alarms-in-effect`" + `: unacknowledged and with no escalation end
recorded. The CLI never works it out itself. --all lists every alarm instead,
acknowledged or past its escalation end, and adds per task the number of its
acknowledged alarms (ACKED) and the age of its last recorded activity (IDLE):
an acknowledgement stops the climb, not the stall. ACKED counts only the
alarms this command's walk read — a lower bound whenever the walk is
incomplete — and IDLE is absent, not zero, when the task's own timestamp is
unreadable: an unknown age is not a zero one.

Only alarms on a task appear; task-less alarms (dark-worker, coordinator-cadence)
are read with ` + "`team events --alarms`" + `. Tasks are PENDING and STARTED unless --status
names others, so an alarm on a finished task is not listed by default.

The roster state is the server's TeamRosterState as returned (YOUR_MOVE ...
NOT_LAUNCHABLE; cor:agt:050:22), not relabelled, with the check status beside it
(NO_SESSION and its reason say the assignee has no session). An assignee the
roster does not list, or a person, has none.

--type filters alarm types (e.g. stall.not-started), repeatable or comma
separated. --since is an RFC 3339 time: only alarms strictly after it. Rows are
newest alarm first. The walk pages the stream itself to its end; if it cannot
finish, the output says it is incomplete and --json carries "incomplete": the
marker names the last event read and, when the server offered one, the cursor
to resume from (lastEventId/resumeCursor); a cursor the walk has already used,
repeated immediately or cycling, stops as a stall.`,
		Example: `  hadron team alarms --team
  hadron team alarms --team=worker:Eden --status STARTED
  hadron team alarms --team --all --type stall.no-progress,stall.owner-unreactive --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			teamGiven := cmd.Flags().Changed("team")
			if teamGiven && team != teamFlagBound {
				p, err := parseTaskPartyFlag("--team", team)
				if err != nil {
					return err
				}
				if p.Worker == nil {
					return exitcode.Newf(exitcode.Usage, "--team names a worker (worker:<id, URN or name>), got %q", team)
				}
			}
			wanted := map[string]bool{}
			if len(statuses) == 0 {
				wanted["PENDING"], wanted["STARTED"] = true, true
			}
			for _, s := range statuses {
				if !taskStatuses[strings.ToUpper(s)] {
					return exitcode.Newf(exitcode.Usage, "--status takes %s, got %q", taskStatusDoc, s)
				}
				wanted[strings.ToUpper(s)] = true
			}
			for _, ty := range types {
				if strings.TrimSpace(ty) == "" {
					return exitcode.Newf(exitcode.Usage, "--type takes alarm types such as stall.not-started, got an empty value")
				}
			}
			if cmd.Flags().Changed("since") {
				if _, err := time.Parse(time.RFC3339, since); err != nil {
					return exitcode.Newf(exitcode.Usage, "--since takes an RFC 3339 timestamp, e.g. 2026-10-12T00:00:00Z, got %q", since)
				}
			}
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil && !errors.Is(err, errNoWorktree) {
				return err
			}
			if teamGiven && team == teamFlagBound && (b == nil || b.WorkerID == "") {
				return exitcode.Newf(exitcode.Usage, "a bare --team means the bound worker's team, and this worktree has no worker session — name one with --team=worker:<ref>")
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
			rosterResp, err := gen.TeamAlarmRoster(ctx, client, scope.Ref)
			if err != nil {
				return mapTaskError(err)
			}
			if rosterResp.TeamRoster == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no roster for %s; nothing was read", appLabel())
			}
			workers := []rosterWorker{}
			rosterByWorker := map[string]*alarmRosterDTO{}
			for _, w := range rosterResp.TeamRoster.Workers {
				if w == nil || w.Worker == nil {
					continue
				}
				rw := rosterWorker{party: *partyDTO(w.Worker)}
				if w.ReportsTo != nil {
					rw.reportsTo = w.ReportsTo.Id
				}
				workers = append(workers, rw)
				entry := &alarmRosterDTO{State: string(w.State), Bound: w.Bound, LiveSession: w.LiveSession}
				if w.CheckStatus != nil {
					entry.CheckStatus = channelCheckFromFields(w.CheckStatus.ChannelCheckFields)
				}
				rosterByWorker[rw.party.ID] = entry
			}
			dto := teamAlarmsDTO{AppID: rosterResp.TeamRoster.AppId, Scope: "app", All: all, Alarms: []teamAlarmDTO{}}
			var inTeam map[string]bool
			if teamGiven {
				var coordinatorRef, source string
				if team == teamFlagBound {
					// Only a bare --team reads the binding (guarded above).
					coordinatorRef, source = b.WorkerID, "the bound worker"
				} else {
					_, coordinatorRef, _ = strings.Cut(team, ":")
					source = "from --team"
				}
				coordinator, members, ok := resolveTeam(workers, coordinatorRef)
				if !ok {
					return exitcode.Newf(exitcode.NotFound, "worker %s is not on the roster of %s (the roster lists active workers; a retired worker leads no team here)", coordinatorRef, appLabel())
				}
				inTeam = map[string]bool{}
				for _, m := range members {
					inTeam[m.ID] = true
				}
				dto.Scope, dto.Coordinator, dto.CoordinatorSource = "team", &coordinator, source
			}
			tasks, err := readAllTasks(ctx, f, scope.Ref, nil)
			if err != nil {
				return err
			}
			var inScope map[string]string
			if inTeam != nil {
				inScope = selectTeamTasks(tasks, inTeam)
			}
			byID := map[string]taskDTO{}
			for _, t := range tasks {
				if inScope != nil {
					if _, ok := inScope[t.ID]; !ok {
						continue
					}
				}
				if wanted[t.Status] {
					byID[t.ID] = t
				}
			}

			events, stop, err := walkAlarms(ctx, client, f.IOStreams.ErrOut, rosterResp.TeamRoster.AppId, !all, since, types)
			if err != nil {
				return err
			}
			if stop != nil {
				dto.Incomplete = &alarmCountsDTO{Complete: false, Reason: stop.Reason, LastEventID: stop.LastEventID, ResumeCursor: stop.ResumeCursor, Pages: stop.Pages}
			}
			now := time.Now()
			ackedByTask := map[string]int{}
			for _, e := range events {
				if e.AcknowledgedAt != nil && e.TaskId != nil {
					ackedByTask[*e.TaskId]++
				}
			}
			for _, e := range events {
				if e.TaskId == nil {
					continue
				}
				t, ok := byID[*e.TaskId]
				if !ok {
					continue
				}
				row := teamAlarmDTO{
					ID: e.Id, Type: e.Type, CreatedAt: e.CreatedAt,
					AcknowledgedAt: e.AcknowledgedAt, AcknowledgedBy: partyDTO(e.AcknowledgedBy),
					EscalationEndedAt: e.EscalationEndedAt,
					Task:              alarmTaskDTO{ID: t.ID, Handle: t.Handle, Title: t.Title, Status: t.Status},
					Assignee:          t.Assignee,
				}
				if e.Severity != nil {
					row.Severity = string(*e.Severity)
				}
				if at, perr := time.Parse(time.RFC3339Nano, e.CreatedAt); perr == nil {
					row.AgeSeconds = int64(now.Sub(at) / time.Second)
				}
				if t.Assignee != nil && t.Assignee.Kind == string(gen.AgentTaskPartyKindWorker) {
					row.AssigneeRoster = rosterByWorker[t.Assignee.ID]
				}
				if all {
					n := ackedByTask[t.ID]
					row.TaskAcknowledgedAlarms = &n
					if age, _, aerr := taskAge(t, now); aerr == nil {
						secs := int64(age / time.Second)
						row.TaskActivityAgeSeconds = &secs
					}
				}
				dto.Alarms = append(dto.Alarms, row)
			}

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				if dto.Coordinator != nil {
					if _, err := fmt.Fprintf(w, "team: %s (%s)\n", partyCell(dto.Coordinator), dto.CoordinatorSource); err != nil {
						return err
					}
				}
				if stop != nil {
					if _, err := fmt.Fprintf(w, "alarms: the list is INCOMPLETE (paging stopped: %s %s after %d pages); more alarms may exist\n", stop.Reason, stop.stopPoint(), stop.Pages); err != nil {
						return err
					}
				}
				if len(dto.Alarms) == 0 {
					// An incomplete walk proves nothing about the pages it never
					// read: "none in effect" would be a claim outrunning its evidence.
					if stop != nil {
						_, err := fmt.Fprintln(w, "no alarms found in the pages read")
						return err
					}
					if all {
						_, err := fmt.Fprintln(w, "no alarms")
						return err
					}
					_, err := fmt.Fprintln(w, "no alarms in effect")
					return err
				}
				return renderTeamAlarms(w, dto, all)
			})
		},
	}
	cmd.Flags().StringVar(&team, "team", "", "a coordinator's team: bare for the bound worker, or --team=worker:<ref> (default: every task of the App)")
	cmd.Flags().Lookup("team").NoOptDefVal = teamFlagBound
	cmd.Flags().BoolVar(&all, "all", false, "every alarm, acknowledged or past its escalation end too; adds ACKED and IDLE per task")
	cmd.Flags().StringSliceVar(&types, "type", nil, "alarm type filter, repeatable or comma separated (e.g. stall.not-started)")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "task status filter, repeatable ("+taskStatusDoc+"; default PENDING and STARTED)")
	cmd.Flags().StringVar(&since, "since", "", "RFC 3339 time; alarms strictly after it")
	return cmd
}

// walkAlarms reads the App's alarm events newest-first to the server's page
// end. inEffect is the server's own filter (cor:agt:050:20); the rows are
// returned as the server sent them. A walk that cannot finish returns an
// alarmWalkStop rather than a silent truncation.
func walkAlarms(ctx context.Context, client graphql.Client, errOut io.Writer, appID string, inEffect bool, since string, types []string) ([]*gen.AgentTeamEventFields, *alarmWalkStop, error) {
	var out []*gen.AgentTeamEventFields
	var before *string
	pageSize := teamEventPageCap
	seen := map[string]bool{}
	// Cursors already paged from: a revisit is a stall, not progress.
	cursors := map[string]bool{}
	stop := func(reason, lastEventID, resumeCursor string, pages int) ([]*gen.AgentTeamEventFields, *alarmWalkStop, error) {
		s := &alarmWalkStop{Reason: reason, LastEventID: lastEventID, ResumeCursor: resumeCursor, Pages: pages}
		fmt.Fprintf(errOut, "note: alarm paging stopped (%s) %s without exhausting the stream; the list is incomplete\n", reason, s.stopPoint())
		return out, s, nil
	}
	for pages := 1; ; pages++ {
		resp, err := gen.AgentTeamEventsPage(ctx, client, appID, nil, before, boolPtr(true), nil, boolPtr(true), boolPtr(inEffect), &pageSize, optStr(since), nil, types)
		if err != nil {
			return nil, nil, api.MapError(err)
		}
		page := resp.AgentTeamEventsPage
		if page == nil {
			return nil, nil, exitcode.Newf(exitcode.Error, "the server returned no alarm page; the list cannot be trusted")
		}
		last := ""
		for _, e := range page.Events {
			if e == nil {
				continue
			}
			last = e.Id
			if seen[e.Id] {
				continue
			}
			seen[e.Id] = true
			ev := e.AgentTeamEventFields
			out = append(out, &ev)
		}
		if !page.HasMore {
			return out, nil, nil
		}
		next := ""
		if page.NextCursor != nil {
			next = *page.NextCursor
		}
		if next == "" {
			return stop(alarmStopCursorStalled, last, "", pages)
		}
		// A cursor the walk has already paged from — the immediate repeat, or
		// a longer A→B→A cycle — can only re-read pages it has read (cli#2349:
		// the old check caught only the immediate repeat and paged a cycle to
		// the page bound).
		if cursors[next] {
			return stop(alarmStopCursorStalled, last, next, pages)
		}
		if pages >= alarmPageLimit {
			return stop(alarmStopPageLimit, last, next, pages)
		}
		cursors[next] = true
		before = &next
	}
}

func renderTeamAlarms(w io.Writer, dto teamAlarmsDTO, all bool) error {
	headers := []string{"ALARM", "AGE", "TYPE", "ACK", "TASK", "STATUS", "TITLE", "ASSIGNEE", "ROSTER"}
	if all {
		headers = append(headers, "ACKED", "IDLE")
	}
	t := output.NewTable(w, headers...)
	for _, a := range dto.Alarms {
		ack := "open"
		if a.AcknowledgedAt != nil {
			ack = "✓"
			if a.AcknowledgedBy != nil {
				ack += " " + partyCell(a.AcknowledgedBy)
			}
		}
		assignee := "—"
		if a.Assignee != nil {
			assignee = partyCell(a.Assignee)
		}
		roster := "—"
		if a.AssigneeRoster != nil {
			roster = a.AssigneeRoster.State
			if a.AssigneeRoster.CheckStatus != nil {
				roster += " · " + channelCheckLabel(a.AssigneeRoster.CheckStatus)
			}
		}
		cells := []string{output.SafeCell(a.ID), idleCell(a.AgeSeconds), output.SafeCell(a.Type), output.SafeCell(ack),
			taskRef(a.Task.Handle, output.SafeCell(a.Task.ID)), output.SafeCell(a.Task.Status), output.SafeCell(a.Task.Title),
			output.SafeCell(assignee), output.SafeCell(roster)}
		if all {
			acked, idle := "—", "—"
			if a.TaskAcknowledgedAlarms != nil {
				acked = fmt.Sprint(*a.TaskAcknowledgedAlarms)
			}
			if a.TaskActivityAgeSeconds != nil {
				idle = idleCell(*a.TaskActivityAgeSeconds)
			}
			cells = append(cells, acked, idle)
		}
		t.Row(cells...)
	}
	return t.Flush()
}
