package calendar

import (
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

// writeHelp is the owner-only contract for the four commands that change a
// calendar (#230); the read pages keep detailsHelp.
const writeHelp = `Details:
  Only the owner of the connection may change a calendar: a grant, even
  calendar.read, does not allow it. Nothing is retried. The provider emails
  attendees about a change unless you pass --no-notify. Pass --idempotency-key
  to make a repeat safe: the same key does not do the work twice. If the command
  fails with a network error or a timeout, the change may or may not have been
  made: read the event back with "calendar events" or "calendar event get"
  before running it again, or repeat it with the same --idempotency-key.
  A timed event takes RFC3339 instants (the UTC offset is the zone), or a
  date-time without an offset plus --tz; with --all-day, --from and --to are
  dates (YYYY-MM-DD; the end date is the day after the last day, as providers
  store it). A time is never guessed from this machine's zone. The output is
  the event as "calendar event get" shows it; --json prints only that JSON.`

type writeOptions struct {
	options
	title, description, location, tz, idem, response string
	attendees                                        []string
	allDay, noNotify, yes, clearAttendees            bool
}

func (o *writeOptions) common(c *cobra.Command) {
	scope(c, &o.options)
	calendarFlag(c, &o.options)
	c.Flags().StringVar(&o.idem, "idempotency-key", "", "make a repeat safe: the same key does not repeat the change")
	c.Flags().BoolVar(&o.noNotify, "no-notify", false, "do not email attendees")
}

func (o *writeOptions) bodyFlags(c *cobra.Command) {
	timeFlags(c, &o.options)
	c.Flags().StringVar(&o.title, "title", "", "event title")
	c.Flags().StringVar(&o.description, "description", "", "event description")
	c.Flags().StringVar(&o.location, "location", "", "event location")
	c.Flags().StringVar(&o.tz, "tz", "", "IANA time zone for a --from/--to without a UTC offset (for example Europe/Paris)")
	c.Flags().StringArrayVar(&o.attendees, "attendee", nil, "attendee email address (repeat; on update a non-empty list replaces the whole set)")
	c.Flags().BoolVar(&o.allDay, "all-day", false, "--from and --to are dates (YYYY-MM-DD), not instants")
}

// boundary turns one --from/--to value into the wire boundary: a date for an
// all-day event, otherwise a date-time carrying its own offset or the --tz.
func (o *writeOptions) boundary(name, v string) (*gen.CalendarEventTimeInput, error) {
	v = strings.TrimSpace(v)
	if o.allDay {
		if _, e := time.Parse("2006-01-02", v); e != nil {
			return nil, exitcode.Newf(exitcode.Usage, "--%s with --all-day must be a date, YYYY-MM-DD", name)
		}
		return &gen.CalendarEventTimeInput{Date: &v}, nil
	}
	b := &gen.CalendarEventTimeInput{DateTime: &v}
	if o.tz != "" {
		tz := o.tz
		b.TimeZone = &tz
	}
	if _, e := time.Parse(time.RFC3339, v); e == nil {
		return b, nil
	}
	if o.tz == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--%s needs a UTC offset (2026-10-30T09:00:00-07:00), or a date-time with --tz", name)
	}
	if _, e := time.Parse("2006-01-02T15:04:05", v); e != nil {
		return nil, exitcode.Newf(exitcode.Usage, "--%s is not a date-time (2026-10-30T09:00:00 or with an offset)", name)
	}
	return b, nil
}

func (o *writeOptions) bounds(c *cobra.Command, required bool) (*gen.CalendarEventTimeInput, *gen.CalendarEventTimeInput, error) {
	if c.Flags().Changed("tz") {
		if _, e := time.LoadLocation(o.tz); e != nil || strings.TrimSpace(o.tz) == "" {
			return nil, nil, exitcode.Newf(exitcode.Usage, "--tz %q is not an IANA time zone", o.tz)
		}
		if o.allDay {
			return nil, nil, exitcode.Newf(exitcode.Usage, "--tz does not apply to an --all-day event")
		}
	}
	var s, e *gen.CalendarEventTimeInput
	var err error
	if c.Flags().Changed("from") {
		if s, err = o.boundary("from", o.from); err != nil {
			return nil, nil, err
		}
	}
	if c.Flags().Changed("to") {
		if e, err = o.boundary("to", o.to); err != nil {
			return nil, nil, err
		}
	}
	if required && (s == nil || e == nil) {
		return nil, nil, exitcode.Newf(exitcode.Usage, "--from and --to are required")
	}
	if o.allDay && s == nil && e == nil && c.Flags().Changed("all-day") {
		return nil, nil, exitcode.Newf(exitcode.Usage, "--all-day needs --from or --to")
	}
	if s != nil && e != nil {
		x, okx := orderable(s)
		y, oky := orderable(e)
		if okx && oky && !x.Before(y) {
			return nil, nil, exitcode.Newf(exitcode.Usage, "--from must be before --to")
		}
	}
	return s, e, nil
}

// orderable returns a comparable instant only when both boundaries are of the
// same kind with an offset or a date; a zone-less pair is left to the server.
func orderable(b *gen.CalendarEventTimeInput) (time.Time, bool) {
	if b.Date != nil {
		t, e := time.Parse("2006-01-02", *b.Date)
		return t, e == nil
	}
	t, e := time.Parse(time.RFC3339, *b.DateTime)
	return t, e == nil
}

// text reads a string flag that, when given, may legitimately be empty (the
// server validates a title, description or location; an empty value is not
// the same as leaving the flag off).
func text(c *cobra.Command, name, v string) *string {
	if !c.Flags().Changed(name) {
		return nil
	}
	return &v
}

func (o *writeOptions) attendeeList(c *cobra.Command) ([]string, bool, error) {
	if o.clearAttendees && c.Flags().Changed("attendee") {
		return nil, false, exitcode.Newf(exitcode.Usage, "--clear-attendees and --attendee cannot be used together")
	}
	if o.clearAttendees {
		return []string{}, true, nil
	}
	if !c.Flags().Changed("attendee") {
		return nil, false, nil
	}
	out := []string{}
	for _, a := range o.attendees {
		a = strings.TrimSpace(a)
		if a == "" {
			return nil, false, exitcode.Newf(exitcode.Usage, "--attendee cannot be empty (check for an unset variable)")
		}
		out = append(out, a)
	}
	return out, true, nil
}

func (o *writeOptions) notify() *bool {
	if !o.noNotify {
		return nil
	}
	f := false
	return &f
}

func create(f *cmdutil.Factory) *cobra.Command {
	o := &writeOptions{}
	c := &cobra.Command{Use: "create", Short: "Create an event (owner only)", Long: "Create an event on a connected calendar. --title, --from and --to are required.\n\n" + connectionHelp + "\n\n" + writeHelp, Example: "  hadron calendar event create --connection <connection-id> --title Standup \\\n    --from 2026-10-30T09:00:00-07:00 --to 2026-10-30T09:15:00-07:00 --attendee a@example.com\n  hadron calendar event create --connection <connection-id> --title Offsite --all-day --from 2026-11-03 --to 2026-11-04", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if e := o.validate(); e != nil {
			return e
		}
		cal, e := optional(c, "calendar", o.calendar)
		if e != nil {
			return e
		}
		idem, e := optional(c, "idempotency-key", o.idem)
		if e != nil {
			return e
		}
		s, en, e := o.bounds(c, true)
		if e != nil {
			return e
		}
		att, _, e := o.attendeeList(c)
		if e != nil {
			return e
		}
		client, e := f.GraphQLClient()
		if e != nil {
			return e
		}
		r, e := gen.CalendarCreateEvent(c.Context(), client, o.connection, cal, o.title, s, en, text(c, "description", o.description), text(c, "location", o.location), att, o.notify(), idem)
		if e != nil {
			return api.MapError(e)
		}
		if r == nil || r.CalendarCreateEvent == nil {
			return protocol()
		}
		row, e := projectEvent(r.CalendarCreateEvent.CalendarReadEvent)
		if e != nil {
			return e
		}
		return render(f, o.connection, "Created event (data):", row)
	}}
	o.common(c)
	o.bodyFlags(c)
	c.Flags().Lookup("title").Usage = "event title (required)"
	c.Flags().Lookup("from").Usage = "start: RFC3339 with an offset, a date-time with --tz, or a date with --all-day (required)"
	c.Flags().Lookup("to").Usage = "end, same forms as --from (required)"
	_ = c.MarkFlagRequired("title")
	_ = c.MarkFlagRequired("from")
	_ = c.MarkFlagRequired("to")
	return c
}

func update(f *cmdutil.Factory) *cobra.Command {
	o := &writeOptions{}
	c := &cobra.Command{Use: "update <eventId>", Short: "Change an event (owner only)", Long: "Change an event: only the flags you pass change; everything else stays as it is.\nA non-empty --attendee list replaces the WHOLE attendee set (repeat the people\nyou keep); --clear-attendees removes every attendee. Name at least one change.\n\n" + connectionHelp + "\n\n" + writeHelp, Example: "  hadron calendar event update <event-id> --connection <connection-id> --title \"Standup (moved)\" \\\n    --from 2026-10-30T10:00:00-07:00 --to 2026-10-30T10:15:00-07:00 --no-notify", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if e := o.validate(); e != nil {
			return e
		}
		id, e := eventID(args[0])
		if e != nil {
			return e
		}
		cal, e := optional(c, "calendar", o.calendar)
		if e != nil {
			return e
		}
		idem, e := optional(c, "idempotency-key", o.idem)
		if e != nil {
			return e
		}
		s, en, e := o.bounds(c, false)
		if e != nil {
			return e
		}
		att, hasAtt, e := o.attendeeList(c)
		if e != nil {
			return e
		}
		title, desc, loc := text(c, "title", o.title), text(c, "description", o.description), text(c, "location", o.location)
		if title == nil && desc == nil && loc == nil && s == nil && en == nil && !hasAtt {
			return exitcode.Newf(exitcode.Usage, "nothing to change: pass at least one of --title, --description, --location, --from, --to, --attendee, --clear-attendees")
		}
		var attp *[]string
		if hasAtt {
			attp = &att
		}
		client, e := f.GraphQLClient()
		if e != nil {
			return e
		}
		r, e := gen.CalendarUpdateEvent(c.Context(), client, o.connection, id, cal, title, s, en, desc, loc, attp, o.notify(), idem)
		if e != nil {
			return api.MapError(e)
		}
		if r == nil || r.CalendarUpdateEvent == nil {
			return protocol()
		}
		row, e := projectEvent(r.CalendarUpdateEvent.CalendarReadEvent)
		if e != nil {
			return e
		}
		return render(f, o.connection, "Updated event (data):", row)
	}}
	o.common(c)
	o.bodyFlags(c)
	c.Flags().BoolVar(&o.clearAttendees, "clear-attendees", false, "remove every attendee (cannot be combined with --attendee)")
	return c
}

type deletedDTO struct {
	EventID string `json:"eventId"`
	Deleted bool   `json:"deleted"`
}

func del(f *cmdutil.Factory) *cobra.Command {
	o := &writeOptions{}
	c := &cobra.Command{Use: "delete <eventId>", Aliases: []string{"rm"}, Short: "Delete (cancel) an event (owner only)", Long: "Delete an event. Attendees are told it is cancelled unless you pass --no-notify.\nAsks first on a terminal; without one, --yes is required.\n\n" + connectionHelp + "\n\n" + writeHelp, Example: "  hadron calendar event delete <event-id> --connection <connection-id> --yes", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if e := o.validate(); e != nil {
			return e
		}
		id, e := eventID(args[0])
		if e != nil {
			return e
		}
		cal, e := optional(c, "calendar", o.calendar)
		if e != nil {
			return e
		}
		idem, e := optional(c, "idempotency-key", o.idem)
		if e != nil {
			return e
		}
		if e := cmdutil.ConfirmDeletion(f.IOStreams, o.yes, "calendar event "+id); e != nil {
			return e
		}
		client, e := f.GraphQLClient()
		if e != nil {
			return e
		}
		r, e := gen.CalendarDeleteEvent(c.Context(), client, o.connection, id, cal, o.notify(), idem)
		if e != nil {
			return api.MapError(e)
		}
		if r == nil || !r.CalendarDeleteEvent {
			return exitcode.Newf(exitcode.Unavailable, "the server did not confirm the delete; read the event back to see whether it still exists")
		}
		return output.Write(f.IOStreams, f.JSON, deletedDTO{EventID: id, Deleted: true}, func(w io.Writer) error {
			_, e := fmt.Fprintf(w, "deleted event %s (server confirmed)\n", id)
			return e
		})
	}}
	o.common(c)
	c.Flags().BoolVarP(&o.yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

func respond(f *cmdutil.Factory) *cobra.Command {
	o := &writeOptions{}
	c := &cobra.Command{Use: "respond <eventId>", Short: "Answer an invitation (owner only)", Long: "Set the connected account's own answer to an invitation: accepted, declined or\ntentative.\n\n" + connectionHelp + "\n\n" + writeHelp, Example: "  hadron calendar event respond <event-id> --connection <connection-id> --response accepted", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if e := o.validate(); e != nil {
			return e
		}
		id, e := eventID(args[0])
		if e != nil {
			return e
		}
		cal, e := optional(c, "calendar", o.calendar)
		if e != nil {
			return e
		}
		var resp gen.CalendarEventResponse
		switch strings.ToLower(strings.TrimSpace(o.response)) {
		case "accepted":
			resp = gen.CalendarEventResponseAccepted
		case "declined":
			resp = gen.CalendarEventResponseDeclined
		case "tentative":
			resp = gen.CalendarEventResponseTentative
		default:
			return exitcode.Newf(exitcode.Usage, "--response must be accepted, declined or tentative")
		}
		client, e := f.GraphQLClient()
		if e != nil {
			return e
		}
		r, e := gen.CalendarRespondToEvent(c.Context(), client, o.connection, id, cal, resp)
		if e != nil {
			return api.MapError(e)
		}
		if r == nil || r.CalendarRespondToEvent == nil {
			return protocol()
		}
		row, e := projectEvent(r.CalendarRespondToEvent.CalendarReadEvent)
		if e != nil {
			return e
		}
		return render(f, o.connection, "Updated event (data):", row)
	}}
	scope(c, &o.options)
	calendarFlag(c, &o.options)
	c.Flags().StringVar(&o.response, "response", "", "accepted, declined or tentative (required)")
	_ = c.MarkFlagRequired("response")
	return c
}

func eventID(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", exitcode.Newf(exitcode.Usage, "eventId cannot be empty")
	}
	return v, nil
}
