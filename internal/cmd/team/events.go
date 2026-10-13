package team

// `team events` and `team alarm ack` — the team App's append-only event
// stream (#830 over server #1705/#1706; cor:agt:050:11 for the stream,
// cor:agt:050:20 for the acknowledgement rule). Reads are plain; the ack is
// an authored write the middleware attributes when a session is bound.

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const (
	teamEventsDefaultLimit = 50
	// teamEventPageCap is the server's clampListTake ceiling; a larger
	// --limit would be silently clamped, so the CLI refuses it instead and
	// names the boundary (cycle 1, Diego P2-2).
	teamEventPageCap = 200
)

func newCmdTeamEvents(f *cmdutil.Factory) *cobra.Command {
	var alarms bool
	var since, after, task string
	var types []string
	var limit int
	cmd := &cobra.Command{
		Use:   "events",
		Short: "The team App's task-work event stream",
		Long: `Read the team App's append-only stream of task work — task.created,
task.assigned, task.started, task.progress, task.finished, task.cancelled,
the stall alarms, availability.changed and the assignment/host events. The
payload carries ids, counts and outcome words only, never message text.

ORDER, so nobody is surprised by page one: the stream reads OLDEST-FIRST
from the beginning of the App (the server's order, (createdAt, id)). Narrow
with --since (strictly after an RFC 3339 time) and page FORWARD with --after
<the last event id you read>. When a page comes back full, the receipt names
the exact --after that continues it.

--alarms keeps alarm events only; --task and --type filter further.
--limit takes 1 to ` + strconv.Itoa(teamEventPageCap) + ` — the server's page cap; above it the request is clamped silently, so the CLI refuses it instead.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if limit < 1 || limit > teamEventPageCap {
				return exitcode.Newf(exitcode.Usage, "--limit takes 1 to %d (the server's page cap) — %d would be clamped silently", teamEventPageCap, limit)
			}
			if since != "" {
				if _, err := time.Parse(time.RFC3339, since); err != nil {
					return exitcode.Newf(exitcode.Usage, "--since takes an RFC 3339 timestamp, e.g. 2026-10-12T00:00:00Z, got %q", since)
				}
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
			resp, err := gen.AgentTeamEvents(ctx, client, scope.Ref, optStr(after), optBool(alarms), &limit, optStr(since), optStr(task), types)
			if err != nil {
				return api.MapError(err)
			}
			events := []teamEventDTO{}
			for _, e := range resp.AgentTeamEvents {
				if e != nil {
					events = append(events, teamEventDTOFrom(&e.AgentTeamEventFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, events, func(w io.Writer) error {
				// Which stream, from where (#470) — page one of a long
				// history looks empty-shaped enough already.
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				t := output.NewTable(w, "ID", "AT", "TYPE", "ACTOR", "TASK", "ACK")
				for _, e := range events {
					ack := ""
					if e.Severity != nil {
						if e.AcknowledgedAt != nil {
							ack = "✓"
						} else {
							ack = "open"
						}
					}
					task := "—"
					if e.TaskID != nil {
						task = *e.TaskID
					}
					t.Row(e.ID, e.CreatedAt, e.Type, eventActorCell(e), task, ack)
				}
				if err := t.Flush(); err != nil {
					return err
				}
				// A full page means more may follow. Name the exact cursor
				// rather than letting a long history look complete. The
				// nonempty guard is load-bearing: an empty page at --limit 0
				// used to index -1 here (cycle 1, Diego P2-2) — 0 now refuses
				// at the gate, and this comparison can no longer trip on a
				// page the server emptied.
				if len(events) > 0 && len(events) == limit {
					last := events[len(events)-1].ID
					_, err := fmt.Fprintf(w, "page full at %d events — continue with --after %s\n", len(events), last)
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&alarms, "alarms", false, "alarm events only")
	cmd.Flags().StringVar(&since, "since", "", "RFC 3339 time; events strictly after it")
	cmd.Flags().StringVar(&after, "after", "", "event id; the page continues strictly after it")
	cmd.Flags().StringVar(&task, "task", "", "events of one task")
	cmd.Flags().StringSliceVar(&types, "type", nil, "event type filter, repeatable (e.g. task.created)")
	cmd.Flags().IntVar(&limit, "limit", teamEventsDefaultLimit, "page size (server cap 200)")
	return cmd
}

// eventActorCell renders who made an event. A lapse (#858 over server
// #1851, cor:agt:060:02) is written by the platform, so its actor is null —
// the payload's setBy "lapse" and lapsedAt say what happened, and the cell
// says it rather than the bare "—" an unnamed platform change shows. Any
// other event, or a payload that is not an object, renders as before.
func eventActorCell(e teamEventDTO) string {
	if e.Actor == nil && e.Type == "availability.changed" {
		var p struct {
			SetBy    string `json:"setBy"`
			LapsedAt string `json:"lapsedAt"`
		}
		if json.Unmarshal(e.Payload, &p) == nil && p.SetBy == "lapse" {
			if p.LapsedAt == "" {
				return "lapse"
			}
			return "lapse at " + p.LapsedAt
		}
	}
	return partyCell(e.Actor)
}

// ---- alarm ack ------------------------------------------------------------

func newCmdTeamAlarm(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alarm <command>",
		Short: "A team App's stall alarms",
		Long: `Stall alarms — the events that wake a task's owner chain
(cor:agt:050:19). See them with ` + "`team events --alarms`" + `.`,
	}
	cmd.AddCommand(newCmdTeamAlarmAck(f))
	return cmd
}

func newCmdTeamAlarmAck(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ack <eventId>",
		Short: "Acknowledge an alarm",
		Long: `Acknowledge an alarm (cor:agt:050:20): it stops waking higher
owners — the stall episode itself goes on until the task moves. Any owner in
the alarm's chain may; the time is the server's. The first acknowledgement
stands (ALARM_ALREADY_ACKNOWLEDGED names the refusal).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eventId := strings.TrimSpace(args[0])
			if eventId == "" {
				return exitcode.Newf(exitcode.Usage, "pass the alarm event id shown by `team events --alarms`")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AlarmAcknowledge(cmd.Context(), client, eventId)
			if err != nil {
				return api.MapError(err)
			}
			event := teamEventDTOFrom(&resp.AcknowledgeAlarm.AgentTeamEventFields)
			return output.Write(f.IOStreams, f.JSON, event, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ acknowledged alarm %s (%s", event.ID, event.Type)
				if err != nil {
					return err
				}
				if event.TaskID != nil {
					if _, err := fmt.Fprintf(w, " on task %s", *event.TaskID); err != nil {
						return err
					}
				}
				if event.AcknowledgedAt != nil {
					if _, err := fmt.Fprintf(w, ", at %s", *event.AcknowledgedAt); err != nil {
						return err
					}
				}
				_, err = io.WriteString(w, ")\n")
				return err
			})
		},
	}
	return cmd
}
