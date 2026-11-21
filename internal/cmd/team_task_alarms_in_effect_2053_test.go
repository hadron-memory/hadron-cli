package cmd

// cli#2053 F4 — the open-alarm count in `team task tree` and
// `team task list --team` follows "in effect" (cor:agt:050:20, V77) through
// the server's alarmsInEffect filter, the same answer `team events
// --alarms-in-effect` gives. The CLI derives nothing itself: it counts every
// alarm row the server returns for a task, acknowledged or not, so the count
// and the events filter can never disagree on the same alarm.

import (
	"encoding/json"
	"strings"
	"testing"
)

// The walk asks the server for alarms in effect, and only that: alarmsOnly
// would widen the server's answer back to every alarm.
func TestTaskCount2053AsksForAlarmsInEffect(t *testing.T) {
	page := static1919(page1919([]string{alarmRow1919("a1", "t1", false)}, false, ""))
	for name, run := range map[string]func() ([]attnCall, error){
		"tree": func() ([]attnCall, error) {
			_, _, calls, err := alarmWalk1919(t, static1919(page1919([]string{alarmRow1919("a1", "task1", false)}, false, "")), "team", "task", "tree", "task1")
			return calls, err
		},
		"list-team": func() ([]attnCall, error) {
			responses := rosterResponses993(roster1044()...)
			responses["TeamReportingLines"] = responses["TeamRoster"]
			responses["AgentTaskList"] = taskPage1044(tasks1044(), false)
			_, _, calls, err := pagesServer1919(t, responses, page, "team", "task", "list", "--team=worker:Marco")
			return calls, err
		},
	} {
		t.Run(name, func(t *testing.T) {
			calls, err := run()
			if err != nil {
				t.Fatal(err)
			}
			pc := pageCalls1919(calls)
			if len(pc) == 0 {
				t.Fatal("no alarm read")
			}
			for _, c := range pc {
				if c.Vars["alarmsInEffect"] != true {
					t.Fatalf("alarmsInEffect not sent: %v", c.Vars)
				}
				if _, ok := c.Vars["alarmsOnly"]; ok {
					t.Fatalf("alarmsOnly sent beside alarmsInEffect: %v", c.Vars)
				}
			}
		})
	}
}

// The server's answer is the count: a row it returns is counted even when it
// already carries an acknowledgement (the up-to-one-evaluator-pass lag in
// 050:20). The CLI never re-derives "in effect" from acknowledgedAt.
func TestTaskCount2053CountsEveryServedRow(t *testing.T) {
	page := static1919(page1919([]string{
		alarmRow1919("open", "task1", false),
		alarmRow1919("acked-still-served", "task1", true),
	}, false, ""))
	out, _, _, err := alarmWalk1919(t, page, "team", "task", "tree", "task1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var tasks []map[string]any
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatal(err)
	}
	if tasks[0]["openAlarms"] != float64(2) {
		t.Fatalf("the CLI filtered the server's in-effect answer: %s", out)
	}
}

// The label and help say what is counted: alarms in effect, from the server,
// with the evaluator-pass bound; never "unacknowledged".
func TestTaskCount2053HelpSaysInEffect(t *testing.T) {
	for _, argv := range [][]string{
		{"team", "task", "tree", "--help"},
		{"team", "task", "list", "--help"},
	} {
		f, o := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(argv)
		root.SetOut(o)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		help := strings.Join(strings.Fields(o.String()), " ") // help wraps
		for _, want := range []string{"in effect", "--alarms-in-effect", "evaluator pass"} {
			if !strings.Contains(help, want) {
				t.Errorf("%v: help is missing %q", argv, want)
			}
		}
		if strings.Contains(help, "unacknowledged alarm") {
			t.Errorf("%v: help still says it counts unacknowledged alarms", argv)
		}
	}
}
