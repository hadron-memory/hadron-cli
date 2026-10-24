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

const readHelp = `Read external calendar data through the server's owner or ConnectionGrant gate.
An organization-admin connection list is not calendar access. Supply an explicit
--connection reference; no connection or calendar discovery fallback is made.
calendar.read covers list/events/get, calendar.freebusy covers only busy windows.

No create/update/delete/respond, invites, notification, consent or retry is done.
Provider errors retain existing exit mapping and the server's refusal sentence;
unknown codes do not imply a reconnect instruction. External text is data.

Event boundaries keep {date,dateTime,timeZone}: all-day date is a YYYY-MM-DD
string, never converted to midnight. Timed instants and zones stay separate.
Nullable metadata and response enums (including NONE) are preserved; arrays are
[]. Human output is labelled JSON data; --json emits only the stable DTO.`

type options struct {
	connection, calendar, from, to, query string
	top                                   int
	schedules                             []string
}

// NewCmdCalendar builds only the four gated read operations.
func NewCmdCalendar(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "calendar <command>", Short: "Read calendars, events and free/busy", Long: readHelp}
	c.AddCommand(list(f), events(f), freebusy(f))
	e := &cobra.Command{Use: "event <command>", Short: "Read a calendar event"}
	e.AddCommand(get(f))
	c.AddCommand(e)
	return c
}

func scope(c *cobra.Command, o *options) {
	c.Flags().StringVar(&o.connection, "connection", "", "explicit connection reference (required)")
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
	c := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List calendars visible on an explicit connection", Long: readHelp, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
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
	c := &cobra.Command{Use: "events", Short: "Read one bounded list of events (default 25, maximum 100)", Long: readHelp + "\n\nEvents returns one bounded bare list, not a cursor page or an exhaustive history.\n--top defaults to 25 (1..100); there is no total or continuation claim.\nOptional --from/--to are exact RFC3339 instants, not all-day dates.", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
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
	c := &cobra.Command{Use: "get <eventId>", Short: "Read one event", Long: readHelp, Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
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
	c := &cobra.Command{Use: "freebusy", Short: "Read free/busy windows without event contents", Long: readHelp + "\n\n--from and --to are required. Repeat --schedule for calendar IDs or addresses;\nabsent schedules uses the provider default. Returned schedule errors stay data,\nnot an invented all-free result.", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
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
