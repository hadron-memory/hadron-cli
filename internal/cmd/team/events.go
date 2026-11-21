package team

// `team events` and `team alarm ack` — the team App's append-only event
// stream (#830 over server #1705/#1706; cor:agt:050:11 for the stream,
// cor:agt:050:20 for the acknowledgement rule). Reads are plain; the ack is
// an authored write the middleware attributes when a session is bound.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
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

// alarmAckInspectHint tells the person where to look after an acknowledge
// answered null (one hint for both the plain and the instructed call, #2552).
const alarmAckInspectHint = "check the alarm in `team events --alarms`"

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
	var alarms, alarmsInEffect bool
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
assignment.unassignable and stall.finish-by), dark-worker and cadence
(alarm.lead-cadence; events raised before the rename say alarm.coordinator-cadence)
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

--alarms keeps alarm events only; --task and --type filter further.
--alarms-in-effect keeps only the alarms still in effect: unacknowledged and with
no recorded escalation end. The server decides that, never worked out here, and
it already means alarms only, so it returns the same rows with or without
--alarms. An alarm that has left effect is not acknowledged and not hidden:
read without this flag to see it, and its end time in the ESCALATION ENDED
column (cor:agt:050:20). That column is when the server recorded the end; it
is blank on every event that is not an alarm and on every alarm whose end is
not recorded yet: one in effect, and also one already acknowledged or whose
task already moved, until the server records the end. That record can lag the
actual end by up to one evaluator pass (about a minute), and the alarm reads
as in effect until it is written, so the column is not a real-time value. Task-less
cadence alarms (either string) show coordinator <name> in the TASK cell; no payload
reason or evidence is added. An alarm.start-failing (wake handle START: the
platform's starts of a long-lived worker with no task keep failing) is task-less
too and shows worker <name>; its failure count and window stay in --json. A missing subject stays —.
A task event's TASK cell is the task's handle, then the first 12 hex of its id
(AT0.7234 01a1d71218bb); when the server names no handle (the task is gone) the
cell stays the bare id. --json adds taskHandle beside taskId, exactly as the
server sent it, null for an event with no task. --task takes either form. The
handle needs a server that carries AgentTeamEvent.taskHandle (hadron-server
#4135); an older one refuses the read and nothing is printed.
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
			resp, err := gen.AgentTeamEventsPage(ctx, client, scope.Ref, optStr(after), optStr(before), optBool(newest), prefix, optBool(alarms), optBool(alarmsInEffect), &limit, optStr(since), optStr(task), types)
			if err != nil {
				return api.MapError(err)
			}
			// A success answer can still carry a null selected page (cli#2477):
			// the same guard walkAlarms has — a command error, never a panic.
			if resp.AgentTeamEventsPage == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no events page; the list cannot be trusted")
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
				t := output.NewTable(w, "ID", "AT", "TYPE", "ACTOR", "TASK", "ACK", "ESCALATION ENDED")
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
						task = eventTaskCell(e)
					} else {
						task = tasklessAlarmCell(e)
					}
					// Blank when the server recorded no end: no placeholder,
					// and never the acknowledgement time (cor:agt:050:20).
					ended := ""
					if e.EscalationEndedAt != nil {
						ended = output.SafeCell(*e.EscalationEndedAt)
					}
					t.Row(output.SafeCell(e.ID), output.SafeCell(e.CreatedAt), output.SafeCell(e.Type), output.SafeCell(eventActorCell(e)), output.SafeCell(task), output.SafeCell(ack), ended)
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
	cmd.Flags().BoolVar(&alarmsInEffect, "alarms-in-effect", false, "only alarms still in effect (the server's filter; implies alarms only)")
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

// eventTaskCell is a task event's TASK cell: the server's handle first, then
// the first 12 hex of the id (the form task list and task get print,
// cli#1131). When the server names no handle (the task is gone) it stays the
// bare id, as before; the handle is never derived here (cli#2005).
func eventTaskCell(e teamEventDTO) string {
	id := *e.TaskID
	if e.TaskHandle == nil || *e.TaskHandle == "" {
		return id
	}
	short := id
	if len(short) > 12 {
		short = short[:12]
	}
	return *e.TaskHandle + " " + short
}

// tasklessSubjectAlarmTypes is the one definition of the alarm types raised
// without a task that name a subject worker instead. A type added here must get
// a case in tasklessAlarmCell (TestEveryTasklessAlarmTypeRenders enforces it); a
// type missing here renders "—" in the list and is dropped from the ack receipt.
var tasklessSubjectAlarmTypes = []string{
	"alarm.dark-worker", "alarm.lead-cadence", "alarm.coordinator-cadence", "alarm.start-failing",
}

// isTasklessSubjectAlarm reports whether t is one of tasklessSubjectAlarmTypes.
// tasklessAlarmCell gates on it and the "team alarm ack" receipt filters on it.
func isTasklessSubjectAlarm(t string) bool {
	return slices.Contains(tasklessSubjectAlarmTypes, t)
}

// Task-less alarm metadata is server-owned. Only known reason words reach
// human output; evidence and arbitrary payload strings remain JSON-only.
func tasklessAlarmCell(e teamEventDTO) string {
	if e.SubjectWorker == nil || !isTasklessSubjectAlarm(e.Type) {
		return "—"
	}
	switch e.Type {
	case "alarm.lead-cadence", "alarm.coordinator-cadence": // the rename (server#4375); older events keep the old string
		// The reason repeats the type, and hidden evidence may be absent.
		// Render only the server's subject, never a payload-derived string.
		return "coordinator " + partyCell(e.SubjectWorker)
	case "alarm.start-failing":
		// #4231: platform starts of the worker keep failing. Its payload
		// (failure count, window) stays in --json only.
		return "worker " + partyCell(e.SubjectWorker)
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
		Short: "Acknowledge a team App's task-stall, dark-worker and cadence alarms",
		Long: `An alarm is a warning the platform raises when work stalls: a task that was not
started (stall.not-started), shows no progress (stall.no-progress), whose owner
has not reacted (stall.owner-unreactive), that cannot be assigned
(assignment.unassignable) or has passed its finish-by time (stall.finish-by);
or a dark-worker alarm (a worker that has gone dark), or a cadence alarm
(alarm.lead-cadence: a coordinator that has missed its cadence; events raised
before the rename say alarm.coordinator-cadence, and both are handled alike).
An alarm wakes its nearest owner first, and the owners above only while it stays unacknowledged.

To see alarms, use "hadron team alarms" (the alarms in effect now, one row per
alarm) or "hadron team events --alarms" (the raw event stream). Both show each
alarm's event id; "hadron team alarm ack <eventId>" acknowledges it.`,
		Example: `  hadron team alarms --team
  hadron team alarm ack 01a1f0c2d3e4f5a6b7c8d9e0f1a2b3c4`,
	}
	cmd.AddCommand(newCmdTeamAlarmAck(f))
	return cmd
}

func newCmdTeamAlarmAck(f *cmdutil.Factory) *cobra.Command {
	var rawInstruction string
	cmd := &cobra.Command{
		Use:   "ack <eventId>",
		Short: "Acknowledge an alarm so it stops waking higher owners",
		Long: `Acknowledge an alarm so it stops waking higher owners. <eventId> is the
alarm's event id: the ALARM column of "hadron team alarms", or the ID column of
"hadron team events --alarms". Any owner in the alarm's chain may. On success it
prints "✓ acknowledged alarm <id> (<type> on task <task>), at <time>" (a
task-less alarm names its worker or coordinator instead of the task; --json: the
event). The first acknowledgement stands: a second is refused with
ALARM_ALREADY_ACKNOWLEDGED.

An acknowledgement stops the climb; it does not end the alarm episode. The
episode ends by itself: a finish-by alarm when the task finishes or is
cancelled, or its finish-by value changes or is cleared (starting or assigning
it does not end it); a dark-worker alarm when the worker is checking again and
has no unacknowledged wake older than the alarm window. A cadence alarm
(alarm.lead-cadence, or alarm.coordinator-cadence on events raised before the
rename) names its coordinator, and a start-failing alarm its worker, without
printing payload reasons or evidence.

--instruction opens the App-manager's held-worker path: an AI worker whose
holder manages the App acknowledges on its holder's say-so; the ordinary
owner-in-chain path is unchanged. Instructed JSON adds warnings ([] when none);
text mode prints server warnings on stderr on either path, while a plain
--json stays a bare event with no warnings key.

` + taskInstructionHelpShort(taskInstructionFloor),
		Example: `  hadron team alarms --team
  hadron team alarm ack 01a1f0c2d3e4f5a6b7c8d9e0f1a2b3c4`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eventId := strings.TrimSpace(args[0])
			if eventId == "" {
				return exitcode.Newf(exitcode.Usage, "pass the alarm event id shown by `team events --alarms`")
			}
			claim, err := taskClaim(cmd, rawInstruction)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			var finishInstruction func(context.Context) (context.Context, bool, error)
			if claim != nil {
				// Snapshot before any lookup; suppress ambient authored attribution
				// so only the verified snapshot may attach this instructed session.
				finishInstruction = BeginServerTargetInstructedWrite(f)(ctx)
				ctx = withAsPerson(ctx)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var fields gen.AgentTeamEventFields
			var warnings []taskWarningDTO
			if claim == nil {
				// #2537 (from #2505 F4): warnings arrive on a plain ack too
				// (the server files task warnings for acknowledgeAlarm
				// whatever carried the call), so the plain path reads the same
				// sink. Text mode prints them below like the instructed path;
				// plain --json keeps its bare event shape (byte identity).
				mctx, sink := api.WithWarningSinkForFields(ctx, "acknowledgeAlarm")
				resp, callErr := gen.AlarmAcknowledge(mctx, client, eventId)
				if callErr != nil {
					return api.MapError(callErr)
				}
				if resp.AcknowledgeAlarm == nil {
					return nullMutationResult("acknowledgeAlarm", "event", alarmAckInspectHint)
				}
				fields = resp.AcknowledgeAlarm.AgentTeamEventFields
				warnings = taskWarningsFor(sink, "acknowledgeAlarm")
			} else {
				// The ServerTarget variant (instruction.go) takes no target:
				// the server resolves the App this event names. The discarded
				// bool is whether the worker session was attached — unused
				// here because admission is the server's either way, and a
				// fallback-to-person note was already printed if one applied
				// (#2505 F3).
				ctx, _, err = finishInstruction(ctx)
				if err != nil {
					return err
				}
				mctx, sink := api.WithWarningSinkForFields(ctx, "acknowledgeAlarm")
				resp, callErr := gen.AlarmAcknowledgeInstructed(mctx, client, eventId, claim)
				if callErr != nil {
					return api.MapErrorKeepingCode(callErr)
				}
				if resp.AcknowledgeAlarm == nil {
					return nullMutationResult("acknowledgeAlarm", "event", alarmAckInspectHint)
				}
				fields = resp.AcknowledgeAlarm.AgentTeamEventFields
				warnings = taskWarningsFor(sink, "acknowledgeAlarm")
			}
			event := teamEventDTOFrom(&fields)
			var result any = event
			if claim != nil {
				result = struct {
					teamEventDTO
					Warnings []taskWarningDTO `json:"warnings"`
				}{event, warnings}
			}
			err = output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ acknowledged alarm %s (%s", event.ID, event.Type)
				if err != nil {
					return err
				}
				if event.TaskID != nil {
					if _, err := fmt.Fprintf(w, " on task %s", *event.TaskID); err != nil {
						return err
					}
				}
				if event.TaskID == nil && isTasklessSubjectAlarm(event.Type) && event.SubjectWorker != nil {
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
			if err != nil {
				return err
			}
			if !f.JSON {
				for _, warning := range warnings {
					if _, err := fmt.Fprintf(f.IOStreams.ErrOut, "warning: [%s] %s\n", output.SafeCell(warning.Code), output.SafeCell(warning.Text)); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&rawInstruction, "instruction", "", taskInstructionFlagUsage)
	return cmd
}
