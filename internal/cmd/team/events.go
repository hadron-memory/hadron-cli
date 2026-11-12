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
	teamEventsDefaultLimit = 20
	// teamEventPageCap is the server's clampListTake ceiling; a larger
	// --limit would be silently clamped, so the CLI refuses it instead and
	// names the boundary (cycle 1, Diego P2-2).
	teamEventPageCap = 200
)

// teamEventsPageDTO is `team events --json` (cli#1919, J2): the events in the
// server's order plus the server's page end. Events is never null.
type teamEventsPageDTO struct {
	Events     []teamEventDTO `json:"events"`
	HasMore    bool           `json:"hasMore"`
	NextCursor *string        `json:"nextCursor"`
}

func newCmdTeamEvents(f *cmdutil.Factory) *cobra.Command {
	var alarms bool
	var since, after, before, task, codePrefix string
	var newest bool
	var types []string
	var limit int
	cmd := &cobra.Command{
		Use:   "events",
		Short: "The team App's task-work event stream",
		Long: `Read the team App's append-only stream of task work — task.created,
task.assigned, task.started, task.progress, task.finished, task.cancelled,
task-stall alarms (stall.not-started, stall.no-progress, stall.owner-unreactive,
assignment.unassignable and stall.finish-by), dark-worker and coordinator-cadence
alarms, availability.changed, the
assignment/host events and app.properties-changed (whose payload names the
changed top-level keys, never values). Platform payloads carry ids, counts and outcome words; App-defined action payloads
are participant-readable objects and may carry their own values.

ORDER, so nobody is surprised by page one: the stream reads OLDEST-FIRST
from the beginning of the App (the server's order, (createdAt, id)). Narrow
with --since (strictly after an RFC 3339 time) and page FORWARD with --after
<the last event id you read>. --newest reads from the NEWEST end instead —
newest events first, the server's mirror order — and --before <id> continues
it toward older events; --after is refused with --newest and --before without
it. The page end is the server's: the receipt names the exact continuation
(--after, or --newest --before) only when the server says more events remain,
never because a page happened to be full. With --json the output is
{events, hasMore, nextCursor}; events keep the server's order and nextCursor
is the cursor to pass on (null on the last page).

--code-prefix filters the server event code and combines with the other flags.
app. also matches platform types app.properties-changed, app.credential-act
and app.manager-act; only app.<slug>. is App-only. No prefix is added when
omitted. App-defined action payloads may contain participant-readable values;
keep secrets out of them.

--alarms keeps alarm events only; --task and --type filter further. Task-less
coordinator-cadence alarms show coordinator <name> in the TASK cell; no payload
reason or evidence is added. A missing subject stays —.
--limit takes 1 to ` + strconv.Itoa(teamEventPageCap) + ` — the server's page cap; above it the request is clamped silently, so the CLI refuses it instead.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if limit < 1 || limit > teamEventPageCap {
				return exitcode.Newf(exitcode.Usage, "--limit takes 1 to %d (the server's page cap) — %d would be clamped silently", teamEventPageCap, limit)
			}
			if newest && after != "" {
				return exitcode.Newf(exitcode.Usage, "--after continues an oldest-first read; with --newest page toward older events with --before <id>")
			}
			if before != "" && !newest {
				return exitcode.Newf(exitcode.Usage, "--before continues a --newest read; add --newest, or page an oldest-first read with --after <id>")
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
			var prefix *string
			if cmd.Flags().Changed("code-prefix") {
				prefix = &codePrefix
			}
			resp, err := gen.AgentTeamEventsPage(ctx, client, scope.Ref, optStr(after), optStr(before), optBool(newest), prefix, optBool(alarms), &limit, optStr(since), optStr(task), types)
			if err != nil {
				return api.MapError(err)
			}
			page := teamEventsPageDTO{Events: []teamEventDTO{}, HasMore: resp.AgentTeamEventsPage.HasMore, NextCursor: resp.AgentTeamEventsPage.NextCursor}
			for _, e := range resp.AgentTeamEventsPage.Events {
				if e != nil {
					page.Events = append(page.Events, teamEventDTOFrom(&e.AgentTeamEventFields))
				}
			}
			events := page.Events
			return output.Write(f.IOStreams, f.JSON, page, func(w io.Writer) error {
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
					var task string
					if e.TaskID != nil {
						task = *e.TaskID
					} else {
						task = tasklessAlarmCell(e)
					}
					t.Row(output.SafeCell(e.ID), output.SafeCell(e.CreatedAt), output.SafeCell(e.Type), output.SafeCell(eventActorCell(e)), output.SafeCell(task), output.SafeCell(ack))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				// The page end is the server's (cor:api:120:04): a continuation
				// is named only when hasMore, with the server's nextCursor and
				// the flag that matches the direction read. A page that merely
				// happens to be full is the last page when hasMore is false.
				if page.HasMore {
					if page.NextCursor == nil || *page.NextCursor == "" {
						_, err := fmt.Fprintln(w, "more events exist, but the server named no continuation cursor")
						return err
					}
					flag := "--after"
					if newest {
						flag = "--newest --before"
					}
					_, err := fmt.Fprintf(w, "more events — continue with %s %s\n", flag, shellQuote(output.SafeCell(*page.NextCursor)))
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&codePrefix, "code-prefix", "", "server-side event code prefix (app.<slug>. for App-only)")
	cmd.Flags().BoolVar(&alarms, "alarms", false, "alarm events only")
	cmd.Flags().StringVar(&since, "since", "", "RFC 3339 time; events strictly after it")
	cmd.Flags().StringVar(&after, "after", "", "event id; the page continues strictly after it")
	cmd.Flags().BoolVar(&newest, "newest", false, "read from the newest end, newest events first")
	cmd.Flags().StringVar(&before, "before", "", "event id; with --newest, the page continues toward older events")
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
// A headless run is never a party (#854 over server #1834): its event's
// actor is null and actorRunId names it, as "run <id>" — the bare id the
// server hands out, which is what `hadron run get` takes.
func eventActorCell(e teamEventDTO) string {
	if e.Actor == nil && e.ActorRunID != nil && *e.ActorRunID != "" {
		return "run " + *e.ActorRunID
	}
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

// Task-less alarm metadata is server-owned. Only known reason words reach
// human output; evidence and arbitrary payload strings remain JSON-only.
func tasklessAlarmCell(e teamEventDTO) string {
	if e.SubjectWorker == nil {
		return "—"
	}
	switch e.Type {
	case "alarm.coordinator-cadence":
		// The reason repeats the type, and hidden evidence may be absent.
		// Render only the server's subject, never a payload-derived string.
		return "coordinator " + partyCell(e.SubjectWorker)
	case "alarm.dark-worker":
		// Only this alarm uses the fixed reason vocabulary below.
	default:
		return "—"
	}
	cell := "worker " + partyCell(e.SubjectWorker)
	var p struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(e.Payload, &p) == nil {
		switch p.Reason {
		case "overdue", "wake-unacked":
			cell += " (" + p.Reason + ")"
		}
	}
	return cell
}

// ---- alarm ack ------------------------------------------------------------

func newCmdTeamAlarm(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alarm <command>",
		Short: "A team App's task-stall, dark-worker and coordinator-cadence alarms",
		Long: `Task-stall, dark-worker and coordinator-cadence alarms — events that wake an owner chain
(cor:agt:050:19). Task stall kinds include stall.not-started, stall.no-progress,
stall.owner-unreactive, assignment.unassignable and stall.finish-by. The finish-by alarm
uses the task deadline instead of ordinary stall windows. See them with ` + "`team events --alarms`" + `.`,
	}
	cmd.AddCommand(newCmdTeamAlarmAck(f))
	return cmd
}

func newCmdTeamAlarmAck(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ack <eventId>",
		Short: "Acknowledge an alarm",
		Long: `Acknowledge an alarm (cor:agt:050:20): it stops waking higher
owners; it does not end the alarm episode. A coordinator-cadence alarm names
its coordinator, without printing payload reasons or evidence. A finish-by episode ends when the
task finishes or is cancelled, or its finish-by value changes or is cleared;
starting or assigning the task does not end that episode. A dark-worker
episode ends when the worker is checking again and has no unacknowledged
wake older than the alarm window. Any owner in
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
				if event.TaskID == nil && (event.Type == "alarm.dark-worker" || event.Type == "alarm.coordinator-cadence") && event.SubjectWorker != nil {
					if _, err := fmt.Fprintf(w, " on %s", output.SafeCell(tasklessAlarmCell(event))); err != nil {
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
