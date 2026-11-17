package cmd

// cli#1940 — `team events` exposes the server's alarmsInEffect filter and
// AgentTeamEvent.escalationEndedAt (hadron-server#3934; cor:agt:050:20, V77).
// The gate's three binding conditions are pinned here:
//
//	A  the CLI passes the server's filter through and never works out "in
//	   effect" itself (not from acknowledgedAt, not by dropping rows);
//	B  a null escalationEndedAt is absent: blank in text, null in --json,
//	   never a placeholder and never filled from the acknowledgement;
//	C  an alarm that left effect is not labelled acknowledged, resolved or
//	   hidden, and the help promises no real-time value.

import (
	"encoding/json"
	"strings"
	"testing"
)

// withEnded1940 gives a row from alarmRow1919 the escalation end the server
// recorded for it. A replace that does not take fails the test: a row whose
// shape moved would otherwise silently keep a null end (cli#2053 F1).
func withEnded1940(t *testing.T, row, endedAt string) string {
	t.Helper()
	const sentinel = `"wakes":null}`
	if strings.Count(row, sentinel) != 1 {
		t.Fatalf("withEnded1940: row lacks exactly one %s, so the end would not be stamped: %s", sentinel, row)
	}
	return strings.Replace(row, sentinel, `"escalationEndedAt":"`+endedAt+`",`+sentinel, 1)
}

func jsonEvents1940(t *testing.T, out string) []map[string]any {
	t.Helper()
	var got struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not the events envelope: %v\n%s", err, out)
	}
	return got.Events
}

func TestTeamEvents1940AlarmsInEffectIsPassedThroughAndOnlyWhenSet(t *testing.T) {
	page := static1919(page1919([]string{alarmRow1919("a1", "task1", false)}, false, ""))
	_, _, calls, err := pagesServer1919(t, taskResponses(), page, "team", "events", "--alarms-in-effect")
	if err != nil {
		t.Fatal(err)
	}
	v := pageCalls1919(calls)[0].Vars
	if v["alarmsInEffect"] != true {
		t.Fatalf("alarmsInEffect not sent as true: %v", v)
	}
	// Condition A: the server's filter implies alarms only, so the CLI adds
	// nothing of its own.
	if _, ok := v["alarmsOnly"]; ok {
		t.Fatalf("--alarms-in-effect must not send alarmsOnly on its own: %v", v)
	}
	// Omitted flag: a byte-identical request to before (the key is absent).
	_, _, calls, err = pagesServer1919(t, taskResponses(), page, "team", "events", "--alarms")
	if err != nil {
		t.Fatal(err)
	}
	v = pageCalls1919(calls)[0].Vars
	if _, ok := v["alarmsInEffect"]; ok {
		t.Fatalf("alarmsInEffect sent without the flag: %v", v)
	}
	if v["alarmsOnly"] != true {
		t.Fatalf("--alarms lost: %v", v)
	}
	// Both flags are sent as given, with every other filter kept.
	_, _, calls, err = pagesServer1919(t, taskResponses(), page, "team", "events", "--alarms", "--alarms-in-effect", "--task", "task1", "--newest")
	if err != nil {
		t.Fatal(err)
	}
	v = pageCalls1919(calls)[0].Vars
	if v["alarmsInEffect"] != true || v["alarmsOnly"] != true || v["taskId"] != "task1" || v["newestFirst"] != true {
		t.Fatalf("filters %v", v)
	}
}

// Condition A: the server's answer is the answer. A row the server returns is
// printed even when it carries an acknowledgement; the CLI never drops or adds
// rows from what it can see.
func TestTeamEvents1940NeverDerivesInEffectItself(t *testing.T) {
	page := static1919(page1919([]string{
		alarmRow1919("acked-but-served", "task1", true),
		alarmRow1919("open-served", "task2", false),
	}, false, ""))
	out, _, _, err := pagesServer1919(t, taskResponses(), page, "team", "events", "--alarms-in-effect")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "acked-but-served") || !strings.Contains(out, "open-served") {
		t.Fatalf("the CLI filtered the server's page: %q", out)
	}
}

func TestTeamEvents1940JSONCarriesEscalationEndedAtOrNull(t *testing.T) {
	page := static1919(page1919([]string{
		withEnded1940(t, alarmRow1919("ended", "task1", false), "2026-10-01T00:30:00Z"),
		alarmRow1919("in-effect", "task2", false),
		eventRow1919("plain"),
	}, false, ""))
	out, _, _, err := pagesServer1919(t, taskResponses(), page, "team", "events", "--json")
	if err != nil {
		t.Fatal(err)
	}
	ev := jsonEvents1940(t, out)
	if len(ev) != 3 {
		t.Fatalf("events: %s", out)
	}
	for _, e := range ev {
		if _, ok := e["escalationEndedAt"]; !ok {
			t.Fatalf("escalationEndedAt key missing on %v", e["id"])
		}
	}
	if ev[0]["escalationEndedAt"] != "2026-10-01T00:30:00Z" {
		t.Fatalf("recorded end not carried: %v", ev[0]["escalationEndedAt"])
	}
	if ev[1]["escalationEndedAt"] != nil || ev[2]["escalationEndedAt"] != nil {
		t.Fatalf("null must stay null: %v / %v", ev[1]["escalationEndedAt"], ev[2]["escalationEndedAt"])
	}
	// Condition B: an acknowledgement never fills the end.
	acked := static1919(page1919([]string{alarmRow1919("acked", "task1", true)}, false, ""))
	out, _, _, err = pagesServer1919(t, taskResponses(), acked, "team", "events", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonEvents1940(t, out)[0]; got["acknowledgedAt"] == nil || got["escalationEndedAt"] != nil {
		t.Fatalf("escalationEndedAt filled from the acknowledgement: %v", got)
	}
}

func TestTeamEvents1940TextShowsTheEndOrBlank(t *testing.T) {
	page := static1919(page1919([]string{
		withEnded1940(t, alarmRow1919("ended", "task1", false), "2026-10-01T00:30:00Z"),
		alarmRow1919("in-effect", "task2", false),
		alarmRow1919("acked", "task3", true),
	}, false, ""))
	out, _, _, err := pagesServer1919(t, taskResponses(), page, "team", "events", "--alarms")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var header string
	rows := map[string]string{}
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		if f[0] == "ID" {
			header = l
		}
		rows[f[0]] = l
	}
	if !strings.Contains(header, "ESCALATION ENDED") {
		t.Fatalf("no ESCALATION ENDED column: %q", out)
	}
	// The ESCALATION ENDED cell is whatever sits under its header.
	cell := func(row string) string {
		at := strings.Index(header, "ESCALATION ENDED")
		if at < 0 || at >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[at:])
	}
	// Condition C: an alarm that left effect is NOT labelled acknowledged:
	// ACK still says open, and the end is its own column.
	if l := rows["ended"]; cell(l) != "2026-10-01T00:30:00Z" || !strings.Contains(l, "open") {
		t.Fatalf("ended row: %q", l)
	}
	// Condition B: absent is blank: no placeholder, no acknowledgement time.
	for _, id := range []string{"in-effect", "acked"} {
		if c := cell(rows[id]); c != "" {
			t.Fatalf("%s: the ESCALATION ENDED cell must be blank, got %q (row %q)", id, c, rows[id])
		}
	}
}

func TestTeamEvents1940HelpStatesTheContractAndPromisesNoRealTime(t *testing.T) {
	f, o := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "events", "--help"})
	root.SetOut(o)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := o.String()
	for _, want := range []string{
		"--alarms-in-effect",
		"ESCALATION ENDED",
		"same rows",             // implies --alarms: no suggestion that it differs
		"evaluator pass",        // the recorded end can lag
		"not acknowledged",      // an ended alarm is not acknowledged ...
		"without this flag",     // ... and is read, not hidden
		"never worked out here", // the CLI does not derive "in effect"
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help is missing %q:\n%s", want, help)
		}
	}
}

// cli#2053 F2: `alarm ack --json` carries escalationEndedAt, the recorded end
// or null — the key agentic-usage promises, pinned rather than only observed.
func TestTeamAlarmAck2053JSONCarriesEscalationEndedAt(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  string
		want any
	}{
		{"recorded", strings.Replace(ackedAlarmEventFieldsJSON, `"wakes":[]}`, `"escalationEndedAt":"2026-10-12T03:05:00Z","wakes":[]}`, 1), "2026-10-12T03:05:00Z"},
		{"not-yet-recorded", ackedAlarmEventFieldsJSON, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.want != nil && !strings.Contains(tc.row, "escalationEndedAt") {
				t.Fatal("fixture stamp did not take")
			}
			r := taskResponses()
			r["AlarmAcknowledge"] = `{"data":{"acknowledgeAlarm":` + tc.row + `}}`
			_, out, err := runTeamTask(t, r, "team", "alarm", "ack", "evt2", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out)
			}
			v, ok := got["escalationEndedAt"]
			if !ok {
				t.Fatalf("escalationEndedAt key missing: %s", out)
			}
			if v != tc.want {
				t.Fatalf("escalationEndedAt = %v, want %v", v, tc.want)
			}
		})
	}
}

// cli#2053 F5: the blank-cell sentence also covers an alarm that is no longer
// in effect but whose end the server has not recorded yet — an acknowledged
// one included (the evaluator lag in cor:agt:050:20).
func TestTeamEvents2053HelpBlankCellCoversUnrecordedEnd(t *testing.T) {
	f, o := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "events", "--help"})
	root.SetOut(o)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(o.String()), " ")
	for _, want := range []string{
		"whose end is not recorded yet",
		"already acknowledged",
		"until the server records the end",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help is missing %q:\n%s", want, help)
		}
	}
}
