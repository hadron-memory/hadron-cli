package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const calendarEvent230 = `{"id":"e","calendarId":"cal","title":null,"description":null,"location":null,"start":{"date":"2026-10-23","dateTime":null,"timeZone":null},"end":{"date":"2026-10-24","dateTime":null,"timeZone":null},"isAllDay":true,"status":"confirmed","organizer":null,"attendees":[],"webLink":null,"seriesId":null,"isOrganizer":false,"myResponse":null,"createdAt":null,"updatedAt":null}`

func TestCalendar230Reads(t *testing.T) {
	for _, tc := range []struct {
		name, op, wire string
		args           []string
		vars           map[string]any
	}{
		{"list", "CalendarList", `{"data":{"calendars":[{"id":"cal","name":"Synthetic","description":null,"timeZone":null,"isPrimary":true,"canEdit":false}]}}`, []string{"list"}, map[string]any{"connectionRef": "conn"}},
		{"events", "CalendarEvents", `{"data":{"calendarEvents":[` + calendarEvent230 + `]}}`, []string{"events"}, map[string]any{"connectionRef": "conn", "top": float64(25)}},
		{"get", "CalendarGetEvent", `{"data":{"calendarEvent":` + calendarEvent230 + `}}`, []string{"event", "get", "e"}, map[string]any{"connectionRef": "conn", "eventId": "e"}},
		{"freebusy", "CalendarFreeBusy", `{"data":{"calendarSchedule":[{"id":"cal","error":null,"busy":[]}]}}`, []string{"freebusy", "--from", "2026-10-23T09:00:00+02:00", "--to", "2026-10-23T10:00:00+02:00", "--schedule", "cal"}, map[string]any{"connectionRef": "conn", "start": "2026-10-23T09:00:00+02:00", "end": "2026-10-23T10:00:00+02:00", "schedules": []any{"cal"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, ops := captureGraphQL(t, map[string]string{tc.op: tc.wire})
			f, out := testFactory(t)
			r := NewRootCmd(f)
			args := append([]string{"calendar"}, tc.args...)
			args = append(args, "--connection", "conn", "--json", "--server", srv.URL)
			r.SetArgs(args)
			if e := r.Execute(); e != nil {
				t.Fatal(e)
			}
			var vars map[string]any
			_ = json.Unmarshal(ops[tc.op], &vars)
			a, _ := json.Marshal(vars)
			b, _ := json.Marshal(tc.vars)
			if string(a) != string(b) || len(ops) != 1 {
				t.Fatal(ops, string(a), string(b))
			}
			if tc.name == "events" || tc.name == "get" {
				if !strings.Contains(out.String(), `"date": "2026-10-23"`) || !strings.Contains(out.String(), `"dateTime": null`) || !strings.Contains(out.String(), `"attendees": []`) || !strings.Contains(out.String(), `"organizer": null`) {
					t.Fatal(out.String())
				}
			}
			if tc.name == "freebusy" && (strings.Contains(out.String(), "title") || !strings.Contains(out.String(), `"busy": []`)) {
				t.Fatal(out.String())
			}
		})
	}
}

func TestCalendar230Invalid(t *testing.T) {
	for _, args := range [][]string{
		{"list"}, {"list", "--connection", " "}, {"events", "--connection", "c", "--top", "0"}, {"events", "--connection", "c", "--top", "101"},
		{"events", "--connection", "c", "--calendar", ""}, {"events", "--connection", "c", "--query", " "}, {"events", "--connection", "c", "--from", "2026-10-23"},
		{"events", "--connection", "c", "--from", "2026-10-23T10:00:00Z", "--to", "2026-10-23T09:00:00Z"},
		{"freebusy", "--connection", "c"}, {"freebusy", "--connection", "c", "--from", "2026-10-23T10:00:00Z", "--to", "2026-10-23T09:00:00Z"},
		{"freebusy", "--connection", "c", "--from", "2026-10-23T09:00:00Z", "--to", "2026-10-23T10:00:00Z", "--schedule", ""},
		{"event", "get", " ", "--connection", "c"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			srv, ops := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs(append(append([]string{"calendar"}, args...), "--server", srv.URL))
			if e := r.Execute(); exitCodeFor(e) != 2 {
				t.Fatal(e)
			}
			if len(ops) != 0 {
				t.Fatal(ops)
			}
		})
	}
}
