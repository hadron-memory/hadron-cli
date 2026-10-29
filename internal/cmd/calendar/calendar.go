// Package calendar implements the read-only calendar slice; authorization belongs to the server.
package calendar

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// connectionHelp tells the user where the required --connection value comes
// from (cli#1184, R2): the calendar commands never discover one themselves.
const connectionHelp = `--connection is the ID of a connected calendar account. Get
it from the ID column of "hadron email connections --org <org>" (the list
includes calendar and Drive connections, not only mail); an org admin can run
that, otherwise ask the owner of the connection, or the admin, for the ID. The
command never picks a connection for you.`

// detailsHelp is the maintainer-level contract, kept last on every page so the
// answer to "what do I type" comes first (cli#1184, R1).
const detailsHelp = `Details:
  Access is the server's: you need to own the connection or hold a grant on it.
  An organization-admin connection list is not by itself calendar access.
  The commands only read. Nothing is created, changed, deleted or answered, no
  invites or notifications are sent, and nothing is retried. Free/busy needs a
  separate permission (calendar.freebusy) from reading events (calendar.read).
  Event text comes from the calendar provider and is shown as data, not
  instructions. If the provider refuses, you see the server's own sentence and
  the usual exit code; it never tells you to reconnect unless the server does.
  An all-day event keeps its date as YYYY-MM-DD, never as midnight. Timed events
  keep the instant and the zone apart. Fields with no value are null and empty
  lists are []. Human output is the same JSON, labelled; --json prints only that
  JSON.`

func readHelp(intro string) string {
	return intro + "\n\n" + connectionHelp + "\n\n" + detailsHelp
}

type options struct {
	connection, calendar, from, to, query string
	top                                   int
	schedules                             []string
}

// NewCmdCalendar builds only the four gated read operations.
func NewCmdCalendar(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "calendar <command>", Short: "Read calendars, events and free/busy", Long: readHelp("Look at a connected calendar without changing it: list its calendars, list the events\nin a time window, read one event, or see when people are busy. Start with\n\"calendar list\" to find a calendar ID, then \"calendar events\"."), Example: "  hadron calendar list --connection <connection-id>\n  hadron calendar events --connection <connection-id> --from 2026-10-30T00:00:00-07:00 --to 2026-10-31T00:00:00-07:00"}
	c.AddCommand(list(f), events(f), freebusy(f))
	e := &cobra.Command{Use: "event <command>", Short: "Read one calendar event by its ID", Long: "Read a single event when you already have its ID (from \"calendar events\"). To list\nseveral events, use \"calendar events\" (plural) instead."}
	e.AddCommand(get(f))
	c.AddCommand(e)
	return c
}

func scope(c *cobra.Command, o *options) {
	c.Flags().StringVar(&o.connection, "connection", "", "connection ID (required; see \"hadron email connections --org <org>\")")
	_ = c.MarkFlagRequired("connection")
}
func calendarFlag(c *cobra.Command, o *options) {
	c.Flags().StringVar(&o.calendar, "calendar", "", "calendar ID; absent uses the provider default")
}
func timeFlags(c *cobra.Command, o *options) {
	c.Flags().StringVar(&o.from, "from", "", "start instant in RFC3339 (offset required)")
	c.Flags().StringVar(&o.to, "to", "", "end instant in RFC3339 (offset required)")
}
func optional(c *cobra.Command, name, value string) (*string, error) {
	if !c.Flags().Changed(name) {
		return nil, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--%s cannot be empty (check for an unset variable)", name)
	}
	return &value, nil
}
func (o *options) validate() error {
	o.connection = strings.TrimSpace(o.connection)
	if o.connection == "" {
		return exitcode.Newf(exitcode.Usage, "give a nonempty --connection reference")
	}
	return nil
}
func bounds(c *cobra.Command, o *options, required bool) (*string, *string, error) {
	a, e := optional(c, "from", o.from)
	if e != nil {
		return nil, nil, e
	}
	b, e := optional(c, "to", o.to)
	if e != nil {
		return nil, nil, e
	}
	if required && (a == nil || b == nil) {
		return nil, nil, exitcode.Newf(exitcode.Usage, "--from and --to are required RFC3339 instants")
	}
	var start, end time.Time
	for _, v := range []struct {
		name  string
		value *string
		dest  *time.Time
	}{{"from", a, &start}, {"to", b, &end}} {
		if v.value != nil {
			t, e := time.Parse(time.RFC3339, *v.value)
			if e != nil {
				return nil, nil, exitcode.Newf(exitcode.Usage, "--%s requires an RFC3339 instant with an offset", v.name)
			}
			*v.dest = t
		}
	}
	if a != nil && b != nil && !start.Before(end) {
		return nil, nil, exitcode.Newf(exitcode.Usage, "--from must be before --to")
	}
	return a, b, nil
}
func render(f *cmdutil.Factory, connection, label string, value any) error {
	return output.Write(f.IOStreams, f.JSON, value, func(w io.Writer) error {
		if _, e := fmt.Fprintf(w, "connection: %q (from --connection)\n%s\n", connection, label); e != nil {
			return e
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	})
}

func list(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	c := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List the calendars on a connection", Long: readHelp("List the calendars you can see on a connection, with each calendar's ID (use it as\n--calendar on the other commands), name, time zone, whether it is the primary\ncalendar and whether you can edit it."), Example: "  hadron calendar list --connection <connection-id>", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if e := o.validate(); e != nil {
			return e
		}
		client, e := f.GraphQLClient()
		if e != nil {
			return e
		}
		r, e := gen.CalendarList(c.Context(), client, o.connection)
		if e != nil {
			return api.MapError(e)
		}
		if r.Calendars == nil {
			return protocol()
		}
		rows := []infoDTO{}
		for _, v := range r.Calendars {
			if v == nil {
				return protocol()
			}
			rows = append(rows, infoDTO{v.Id, v.Name, v.Description, v.TimeZone, v.IsPrimary, v.CanEdit})
		}
		return render(f, o.connection, "Returned calendars (data):", rows)
	}}
	scope(c, o)
	return c
}
func events(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	c := &cobra.Command{Use: "events", Short: "List events in a time window (default 25, at most 100)", Long: readHelp("List events, optionally only those between --from and --to, or matching --query.\nYou get one list of at most --top events (default 25, 1 to 100); there is no\nnext page, so narrow the window or the query to see more. --from and --to are\ndate-times with a UTC offset, for example 2026-10-30T00:00:00-07:00 (a bare\ndate such as 2026-10-30 is refused). For \"tomorrow\", give midnight to midnight\nin your own offset. Without --calendar the provider's default calendar is used."), Example: "  hadron calendar events --connection <connection-id> \\\n    --from 2026-10-30T00:00:00-07:00 --to 2026-10-31T00:00:00-07:00\n  hadron calendar events --connection <connection-id> --query standup --top 10 --json", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if e := o.validate(); e != nil {
			return e
		}
		if o.top < 1 || o.top > 100 {
			return exitcode.Newf(exitcode.Usage, "--top must be between 1 and 100")
		}
		cal, e := optional(c, "calendar", o.calendar)
		if e != nil {
			return e
		}
		q, e := optional(c, "query", o.query)
		if e != nil {
			return e
		}
		a, b, e := bounds(c, o, false)
		if e != nil {
			return e
		}
		client, e := f.GraphQLClient()
		if e != nil {
			return e
		}
		r, e := gen.CalendarEvents(c.Context(), client, o.connection, cal, a, b, q, o.top)
		if e != nil {
			return api.MapError(e)
		}
		if r.CalendarEvents == nil {
			return protocol()
		}
		rows := []eventDTO{}
		for _, v := range r.CalendarEvents {
			if v == nil {
				return protocol()
			}
			row, e := projectEvent(v.CalendarReadEvent)
			if e != nil {
				return e
			}
			rows = append(rows, row)
		}
		return render(f, o.connection, fmt.Sprintf("Bounded events (top %d; no total or continuation):", o.top), rows)
	}}
	scope(c, o)
	calendarFlag(c, o)
	timeFlags(c, o)
	c.Flags().StringVar(&o.query, "query", "", "nonempty event search text")
	c.Flags().IntVar(&o.top, "top", 25, "maximum returned events (1..100, one bounded list)")
	return c
}
func get(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	c := &cobra.Command{Use: "get <eventId>", Short: "Read one event", Long: readHelp("Read one event by its ID. The ID is the \"id\" of an event returned by \"calendar\nevents\". Add --calendar when the event is not on the provider's default calendar."), Example: "  hadron calendar event get <event-id> --connection <connection-id>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if e := o.validate(); e != nil {
			return e
		}
		id := strings.TrimSpace(args[0])
		if id == "" {
			return exitcode.Newf(exitcode.Usage, "eventId cannot be empty")
		}
		cal, e := optional(c, "calendar", o.calendar)
		if e != nil {
			return e
		}
		client, e := f.GraphQLClient()
		if e != nil {
			return e
		}
		r, e := gen.CalendarGetEvent(c.Context(), client, o.connection, id, cal)
		if e != nil {
			return api.MapError(e)
		}
		if r.CalendarEvent == nil {
			return protocol()
		}
		row, e := projectEvent(r.CalendarEvent.CalendarReadEvent)
		if e != nil {
			return e
		}
		return render(f, o.connection, "Returned event (data):", row)
	}}
	scope(c, o)
	calendarFlag(c, o)
	return c
}
func freebusy(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	c := &cobra.Command{Use: "freebusy", Short: "See when calendars are busy, without event details", Long: readHelp("Show the busy windows for one or more calendars between --from and --to (both\nrequired, date-times with a UTC offset). Titles and attendees are not returned.\nRepeat --schedule for each calendar ID or email address; without it, the\nprovider's default calendar is used. If a calendar is not shared with you, its\nentry carries an \"error\" message instead of busy windows: that is not the same\nas free, so do not read it as free."), Example: "  hadron calendar freebusy --connection <connection-id> \\\n    --from 2026-10-30T09:00:00-07:00 --to 2026-10-30T17:00:00-07:00 \\\n    --schedule alice@example.com --schedule bob@example.com", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if e := o.validate(); e != nil {
			return e
		}
		a, b, e := bounds(c, o, true)
		if e != nil {
			return e
		}
		schedules := []string(nil)
		for _, s := range o.schedules {
			s = strings.TrimSpace(s)
			if s == "" {
				return exitcode.Newf(exitcode.Usage, "--schedule cannot be empty")
			}
			schedules = append(schedules, s)
		}
		client, e := f.GraphQLClient()
		if e != nil {
			return e
		}
		r, e := gen.CalendarFreeBusy(c.Context(), client, o.connection, *a, *b, schedules)
		if e != nil {
			return api.MapError(e)
		}
		if r.CalendarSchedule == nil {
			return protocol()
		}
		rows := []scheduleDTO{}
		for _, v := range r.CalendarSchedule {
			if v == nil || v.Busy == nil {
				return protocol()
			}
			row := scheduleDTO{ID: v.Id, Error: v.Error, Busy: []busyDTO{}}
			for _, x := range v.Busy {
				if x == nil {
					return protocol()
				}
				row.Busy = append(row.Busy, busyDTO{x.Start, x.End})
			}
			rows = append(rows, row)
		}
		return render(f, o.connection, "Returned free/busy windows only (data):", rows)
	}}
	scope(c, o)
	timeFlags(c, o)
	_ = c.MarkFlagRequired("from")
	_ = c.MarkFlagRequired("to")
	c.Flags().StringArrayVar(&o.schedules, "schedule", nil, "schedule ID or address (repeatable)")
	return c
}
