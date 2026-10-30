package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func runCalendarWrite(t *testing.T, op, wire string, args ...string) (map[string]any, int, string, int) {
	t.Helper()
	srv, ops := captureGraphQL(t, map[string]string{op: wire})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs(append(append([]string{"calendar", "event"}, args...), "--connection", "conn", "--json", "--server", srv.URL))
	err := r.Execute()
	var vars map[string]any
	if raw, ok := ops[op]; ok {
		_ = json.Unmarshal(raw, &vars)
	}
	return vars, len(ops), out.String(), exitCodeFor(err)
}

func calendarWire(field string) string {
	return `{"data":{"` + field + `":` + calendarEvent230 + `}}`
}

func TestCalendar230CreateRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"timed offset", []string{"--title", "T", "--from", "2026-10-30T09:00:00-07:00", "--to", "2026-10-30T10:00:00-07:00"},
			map[string]any{"title": "T", "start": map[string]any{"dateTime": "2026-10-30T09:00:00-07:00"}, "end": map[string]any{"dateTime": "2026-10-30T10:00:00-07:00"}}},
		{"zone-less with tz", []string{"--title", "T", "--from", "2026-10-30T09:00:00", "--to", "2026-10-30T10:00:00", "--tz", "Europe/Paris"},
			map[string]any{"title": "T", "start": map[string]any{"dateTime": "2026-10-30T09:00:00", "timeZone": "Europe/Paris"}, "end": map[string]any{"dateTime": "2026-10-30T10:00:00", "timeZone": "Europe/Paris"}}},
		{"all day", []string{"--title", "T", "--all-day", "--from", "2026-11-03", "--to", "2026-11-04"},
			map[string]any{"title": "T", "start": map[string]any{"date": "2026-11-03"}, "end": map[string]any{"date": "2026-11-04"}}},
		{"every option", []string{"--title", "T", "--from", "2026-10-30T09:00:00Z", "--to", "2026-10-30T10:00:00Z", "--description", "", "--location", "Room", "--attendee", "a@x.io", "--attendee", "b@x.io", "--no-notify", "--idempotency-key", "k1", "--calendar", "cal"},
			map[string]any{"title": "T", "start": map[string]any{"dateTime": "2026-10-30T09:00:00Z"}, "end": map[string]any{"dateTime": "2026-10-30T10:00:00Z"}, "description": "", "location": "Room", "attendees": []any{"a@x.io", "b@x.io"}, "notifyAttendees": false, "idempotencyKey": "k1", "calendarId": "cal"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars, n, out, code := runCalendarWrite(t, "CalendarCreateEvent", calendarWire("calendarCreateEvent"), append([]string{"create"}, tc.args...)...)
			tc.want["connectionRef"] = "conn"
			if code != 0 || n != 1 || !reflect.DeepEqual(vars, tc.want) {
				t.Fatal(code, n, vars, tc.want)
			}
			if !strings.Contains(out, `"attendees": []`) {
				t.Fatal(out)
			}
		})
	}
}

func TestCalendar230WriteRefusedLocally(t *testing.T) {
	for _, args := range [][]string{
		{"create", "--from", "2026-10-30T09:00:00Z", "--to", "2026-10-30T10:00:00Z"},
		{"create", "--title", "T", "--to", "2026-10-30T10:00:00Z"},
		{"create", "--title", "T", "--from", "2026-10-30T09:00:00", "--to", "2026-10-30T10:00:00"},
		{"create", "--title", "T", "--from", "tomorrow", "--to", "2026-10-30T10:00:00Z"},
		{"create", "--title", "T", "--from", "2026-10-30T09:00:00", "--to", "2026-10-30T10:00:00", "--tz", "Mars/Base"},
		{"create", "--title", "T", "--from", "2026-10-30T10:00:00Z", "--to", "2026-10-30T09:00:00Z"},
		{"create", "--title", "T", "--all-day", "--from", "2026-10-30T09:00:00Z", "--to", "2026-10-31"},
		{"create", "--title", "T", "--all-day", "--from", "2026-10-30", "--to", "2026-10-31", "--tz", "Europe/Paris"},
		{"create", "--title", "T", "--from", "2026-10-30T09:00:00Z", "--to", "2026-10-30T10:00:00Z", "--attendee", ""},
		{"create", "--title", "T", "--from", "2026-10-30T09:00:00Z", "--to", "2026-10-30T10:00:00Z", "--idempotency-key", " "},
		{"update", "e"},
		{"update", "e", "--attendee", "a@x.io", "--clear-attendees"},
		{"update", "e", "--tz", "Europe/Paris"},
		{"update", " ", "--title", "T"},
		{"delete", "e"},
		{"respond", "e"},
		{"respond", "e", "--response", "none"},
		{"respond", "e", "--response", "maybe"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			_, n, _, code := runCalendarWrite(t, "Unused", `{}`, args...)
			if code != 2 || n != 0 {
				t.Fatal(code, n)
			}
		})
	}
}

func TestCalendar230UpdateSubsetAndAttendees(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"title only", []string{"--title", "New"}, map[string]any{"title": "New"}},
		{"empty title is not omission", []string{"--title", ""}, map[string]any{"title": ""}},
		{"move", []string{"--from", "2026-10-30T10:00:00Z", "--to", "2026-10-30T11:00:00Z", "--no-notify"}, map[string]any{"start": map[string]any{"dateTime": "2026-10-30T10:00:00Z"}, "end": map[string]any{"dateTime": "2026-10-30T11:00:00Z"}, "notifyAttendees": false}},
		{"replace attendees", []string{"--attendee", "a@x.io"}, map[string]any{"attendees": []any{"a@x.io"}}},
		{"clear attendees", []string{"--clear-attendees"}, map[string]any{"attendees": []any{}}},
		{"idempotency and calendar", []string{"--location", "R", "--idempotency-key", "k", "--calendar", "c"}, map[string]any{"location": "R", "idempotencyKey": "k", "calendarId": "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars, n, _, code := runCalendarWrite(t, "CalendarUpdateEvent", calendarWire("calendarUpdateEvent"), append([]string{"update", "e1"}, tc.args...)...)
			tc.want["connectionRef"], tc.want["eventId"] = "conn", "e1"
			if code != 0 || n != 1 || !reflect.DeepEqual(vars, tc.want) {
				t.Fatal(code, n, vars, tc.want)
			}
		})
	}
}

func TestCalendar230DeleteConfirmationAndResult(t *testing.T) {
	vars, n, out, code := runCalendarWrite(t, "CalendarDeleteEvent", `{"data":{"calendarDeleteEvent":true}}`, "delete", "e1", "--yes", "--no-notify", "--idempotency-key", "k")
	if code != 0 || n != 1 || !reflect.DeepEqual(vars, map[string]any{"connectionRef": "conn", "eventId": "e1", "notifyAttendees": false, "idempotencyKey": "k"}) {
		t.Fatal(code, n, vars)
	}
	var d map[string]any
	if e := json.Unmarshal([]byte(out), &d); e != nil || d["deleted"] != true || d["eventId"] != "e1" {
		t.Fatal(out, e)
	}
	// Without --yes in a non-interactive run nothing is sent (zero mutation).
	if _, n, _, code := runCalendarWrite(t, "CalendarDeleteEvent", `{"data":{"calendarDeleteEvent":true}}`, "delete", "e1"); code != 2 || n != 0 {
		t.Fatal(code, n)
	}
	// A server "false" is never reported as a delete.
	if _, n, out, code := runCalendarWrite(t, "CalendarDeleteEvent", `{"data":{"calendarDeleteEvent":false}}`, "delete", "e1", "--yes"); code != 7 || n != 1 || strings.Contains(out, `"deleted": true`) {
		t.Fatal(code, n, out)
	}
}

func TestCalendar230RespondArms(t *testing.T) {
	for arg, want := range map[string]string{"accepted": "ACCEPTED", "Declined": "DECLINED", "tentative": "TENTATIVE"} {
		vars, n, _, code := runCalendarWrite(t, "CalendarRespondToEvent", calendarWire("calendarRespondToEvent"), "respond", "e1", "--response", arg, "--calendar", "c")
		if code != 0 || n != 1 || vars["response"] != want || vars["calendarId"] != "c" || vars["eventId"] != "e1" {
			t.Fatal(arg, code, n, vars)
		}
	}
}

func TestCalendar230WriteErrorsAndNull(t *testing.T) {
	for _, tc := range []struct {
		op, field string
		args      []string
	}{
		{"CalendarCreateEvent", "calendarCreateEvent", []string{"create", "--title", "T", "--from", "2026-10-30T09:00:00Z", "--to", "2026-10-30T10:00:00Z"}},
		{"CalendarUpdateEvent", "calendarUpdateEvent", []string{"update", "e", "--title", "T"}},
		{"CalendarDeleteEvent", "calendarDeleteEvent", []string{"delete", "e", "--yes"}},
		{"CalendarRespondToEvent", "calendarRespondToEvent", []string{"respond", "e", "--response", "accepted"}},
	} {
		for mode, want := range map[string]int{"null": 7, "refused": 8} {
			t.Run(tc.op+mode, func(t *testing.T) {
				wire := `{"data":{"` + tc.field + `":null}}`
				if mode == "refused" {
					wire = `{"errors":[{"message":"Only the owner may change this calendar","extensions":{"code":"FORBIDDEN"}}]}`
				}
				_, n, out, code := runCalendarWrite(t, tc.op, wire, tc.args...)
				if code != want || n != 1 || strings.Contains(out, `"deleted": true`) {
					t.Fatal(code, n, out)
				}
			})
		}
	}
}

func TestCalendar230WriteHelpDoesNotClaimReadOnly(t *testing.T) {
	srv, _ := captureGraphQL(t, map[string]string{})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"calendar", "--help", "--server", srv.URL})
	_ = r.Execute()
	if strings.Contains(out.String(), "only read.") || strings.Contains(out.String(), "without changing it") || !strings.Contains(out.String(), "owner-only") {
		t.Fatal(out.String())
	}
}

func TestCalendar230WriteDocumentsAreMutations(t *testing.T) {
	for _, d := range []string{gen.CalendarCreateEvent_Operation, gen.CalendarUpdateEvent_Operation, gen.CalendarDeleteEvent_Operation, gen.CalendarRespondToEvent_Operation} {
		if !strings.HasPrefix(strings.TrimSpace(d), "mutation") {
			t.Fatal(d)
		}
	}
}
