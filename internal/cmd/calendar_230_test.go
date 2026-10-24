package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
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

func TestCalendar230OptionalFiltersAndTimedOutput(t *testing.T) {
	timed := strings.ReplaceAll(calendarEvent230, `"date":"2026-10-23","dateTime":null,"timeZone":null`, `"date":null,"dateTime":"2026-10-23T09:00:00+02:00","timeZone":"Europe/Paris"`)
	timed = strings.ReplaceAll(timed, `"date":"2026-10-24","dateTime":null,"timeZone":null`, `"date":null,"dateTime":"2026-10-23T10:00:00+02:00","timeZone":"Europe/Paris"`)
	timed = strings.ReplaceAll(timed, `"isAllDay":true`, `"isAllDay":false`)
	timed = strings.ReplaceAll(timed, `"attendees":[]`, `"attendees":[{"address":"synthetic@example.test","name":null,"optional":true,"response":"NONE"}]`)
	timed = strings.ReplaceAll(timed, `"myResponse":null`, `"myResponse":"TENTATIVE"`)
	srv, ops := captureGraphQL(t, map[string]string{"CalendarEvents": `{"data":{"calendarEvents":[` + timed + `]}}`})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"calendar", "events", "--connection", " conn ", "--calendar", " cal ", "--query", " words ", "--from", "2026-10-23T09:00:00+02:00", "--to", "2026-10-23T10:00:00+02:00", "--top", "100", "--json", "--server", srv.URL})
	if e := r.Execute(); e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	_ = json.Unmarshal(ops["CalendarEvents"], &v)
	if v["start"] != "2026-10-23T09:00:00+02:00" || v["end"] != "2026-10-23T10:00:00+02:00" || v["calendarId"] != "cal" || v["query"] != "words" || v["top"] != float64(100) || v["connectionRef"] != "conn" || len(v) != 6 {
		t.Fatal(v)
	}
	for _, s := range []string{`"date": null`, `"dateTime": "2026-10-23T09:00:00+02:00"`, `"timeZone": "Europe/Paris"`, `"response": "NONE"`, `"myResponse": "TENTATIVE"`} {
		if !strings.Contains(out.String(), s) {
			t.Fatal(s, out.String())
		}
	}
}

func TestCalendar230FailuresAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		op, field string
	}{
		{[]string{"list"}, "CalendarList", "calendars"},
		{[]string{"events"}, "CalendarEvents", "calendarEvents"},
		{[]string{"event", "get", "e"}, "CalendarGetEvent", "calendarEvent"},
		{[]string{"freebusy", "--from", "2026-10-23T09:00:00Z", "--to", "2026-10-23T10:00:00Z"}, "CalendarFreeBusy", "calendarSchedule"},
	} {
		for _, mode := range []string{"null", "refused", "unknown", "empty"} {
			if mode == "empty" && tc.op == "CalendarGetEvent" {
				continue
			}
			t.Run(tc.op+mode, func(t *testing.T) {
				wire := `{"data":{"` + tc.field + `":null}}`
				want := 7
				if mode == "empty" {
					wire = `{"data":{"` + tc.field + `":[]}}`
					want = 0
				}
				if mode == "refused" {
					wire = `{"errors":[{"message":"Calendar access refused","extensions":{"code":"FORBIDDEN"}}]}`
					want = 8
				}
				if mode == "unknown" {
					wire = `{"errors":[{"message":"Calendar provider failed","extensions":{"code":"EMAIL_TOOL_ERROR"}}]}`
					want = 1
				}
				srv, ops := captureGraphQL(t, map[string]string{tc.op: wire})
				f, out := testFactory(t)
				r := NewRootCmd(f)
				args := append([]string{"calendar"}, tc.args...)
				r.SetArgs(append(args, "--connection", "c", "--json", "--server", srv.URL))
				if e := r.Execute(); exitCodeFor(e) != want {
					t.Fatal(e, out.String())
				}
				if len(ops) != 1 {
					t.Fatal(ops)
				}
				if want == 0 && strings.TrimSpace(out.String()) != "[]" {
					t.Fatal(out.String())
				}
				if tc.op == "CalendarFreeBusy" {
					var v map[string]any
					_ = json.Unmarshal(ops[tc.op], &v)
					if _, ok := v["schedules"]; ok {
						t.Fatal("absent schedules must be omitted", v)
					}
				}
			})
		}
	}
}

func TestCalendar230Documents(t *testing.T) {
	for _, tc := range []struct{ doc, op, field string }{
		{gen.CalendarList_Operation, "CalendarList", "calendars"}, {gen.CalendarEvents_Operation, "CalendarEvents", "calendarEvents"},
		{gen.CalendarGetEvent_Operation, "CalendarGetEvent", "calendarEvent"}, {gen.CalendarFreeBusy_Operation, "CalendarFreeBusy", "calendarSchedule"},
	} {
		doc, e := parser.ParseQuery(&ast.Source{Input: tc.doc})
		if e != nil {
			t.Fatal(e)
		}
		op := doc.Operations.ForName(tc.op)
		if op.Operation != ast.Query || len(op.SelectionSet) != 1 {
			t.Fatal("not a single read", op)
		}
		field := op.SelectionSet[0].(*ast.Field)
		if field.Name != tc.field {
			t.Fatal(field)
		}
		if tc.op == "CalendarFreeBusy" {
			if len(field.SelectionSet) != 3 {
				t.Fatal(field)
			}
			for i, want := range []string{"id", "error", "busy"} {
				if field.SelectionSet[i].(*ast.Field).Name != want {
					t.Fatal(field)
				}
			}
			busy := field.SelectionSet[2].(*ast.Field)
			if len(busy.SelectionSet) != 2 || busy.SelectionSet[0].(*ast.Field).Name != "start" || busy.SelectionSet[1].(*ast.Field).Name != "end" {
				t.Fatal("freebusy expanded to event contents", busy)
			}
		}
	}
}
