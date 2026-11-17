package cmd

// hadron-cli#1829: `team alarms` — the per-task join of a team's alarms. The
// CLI composes reads that already exist (the roster, the App's tasks, the
// alarm stream) and never derives "in effect" itself (cor:agt:050:20): the
// server's alarmsInEffect filter does, and these pins hold the request to it.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// alarmsRosterRow1829 is one TeamAlarmRoster entry.
func alarmsRosterRow1829(id, name, boss, state string, bound, live bool, check string) string {
	reportsTo := "null"
	if boss != "" {
		reportsTo = fmt.Sprintf(`{"id":"w-%s","kind":"WORKER","name":%q,"urn":null}`, boss, boss)
	}
	return fmt.Sprintf(`{"worker":{"id":%q,"kind":"WORKER","name":%q,"urn":null},"reportsTo":%s,"state":%q,"bound":%t,"liveSession":%t,"checkStatus":%s}`,
		id, name, reportsTo, state, bound, live, check)
}

const (
	checkOK1829        = `{"status":"OK","noSessionReason":null,"pendingReason":null,"lastCheckedAt":"2026-11-17T20:00:00Z"}`
	checkNoSession1829 = `{"status":"NO_SESSION","noSessionReason":"NEVER_BOUND","pendingReason":null,"lastCheckedAt":null}`
)

// Marco leads Jonas (bound, live) and Cedar (never bound: NOT_LAUNCHABLE);
// Ada leads Cameron, a separate subtree of the same App.
func alarmsRoster1829() []string {
	return []string{
		alarmsRosterRow1829("w-Marco", "Marco", "", "AVAILABLE", true, true, checkOK1829),
		alarmsRosterRow1829("w-Jonas", "Jonas", "Marco", "WAITING", true, true, checkOK1829),
		alarmsRosterRow1829("w-Cedar", "Cedar", "Marco", "NOT_LAUNCHABLE", false, false, checkNoSession1829),
		alarmsRosterRow1829("w-Ada", "Ada", "", "AVAILABLE", true, true, checkOK1829),
		alarmsRosterRow1829("w-Cameron", "Cameron", "Ada", "AVAILABLE", true, true, checkOK1829),
	}
}

func alarmsTasks1829() []task1044 {
	return []task1044{
		{id: "t1", handle: "AT0.1", owner: worker1044("Marco"), assignee: worker1044("Jonas"), updatedAt: "2026-11-16T00:00:00Z"},
		{id: "t2", handle: "AT0.2", owner: worker1044("Marco"), assignee: worker1044("Cedar")},
		{id: "t3", handle: "AT0.3", status: "FINISHED", owner: worker1044("Marco"), assignee: worker1044("Jonas")},
		{id: "t4", handle: "AT0.4", owner: worker1044("Ada"), assignee: worker1044("Cameron")},
		{id: "t5", handle: "AT0.5", owner: worker1044("Marco"), assignee: user1044},
	}
}

// alarmEvent1829 is one alarm row. ackAt "" is unacknowledged; endedAt "" is
// no recorded escalation end.
func alarmEvent1829(id, task, typ, createdAt, ackAt, ackBy, endedAt string) string {
	ack, by, ended := "null", "null", "null"
	if ackAt != "" {
		ack = fmt.Sprintf("%q", ackAt)
		by = fmt.Sprintf(`{"id":"w-%s","kind":"WORKER","name":%q,"urn":null}`, ackBy, ackBy)
	}
	if endedAt != "" {
		ended = fmt.Sprintf("%q", endedAt)
	}
	return fmt.Sprintf(`{"id":%q,"appId":"capp100000000000000000000","taskId":%q,"taskHandle":null,"type":%q,"severity":"ALARM","createdAt":%q,"actor":null,"subjectWorker":null,"payload":{},"acknowledgedAt":%s,"acknowledgedBy":%s,"episodeStart":null,"escalationEndedAt":%s,"chain":null,"wakes":null}`,
		id, task, typ, createdAt, ack, by, ended)
}

// runAlarms1829 serves the roster, the App's tasks and the i-th alarm page.
func runAlarms1829(t *testing.T, tasks []task1044, pages func(int) string, argv ...string) (string, string, []attnCall, error) {
	t.Helper()
	responses := taskResponses()
	responses["TeamAlarmRoster"] = `{"data":{"teamRoster":{"appId":"capp100000000000000000000","workers":[` + strings.Join(alarmsRoster1829(), ",") + `]}}}`
	responses["AgentTaskList"] = taskPage1044(tasks)
	return pagesServer1919(t, responses, pages, append([]string{"team", "alarms"}, argv...)...)
}

func alarmsPage1829(rows ...string) func(int) string {
	return static1919(page1919(rows, false, ""))
}

type alarmsJSON1829 struct {
	AppID       string           `json:"appId"`
	Scope       string           `json:"scope"`
	Coordinator map[string]any   `json:"coordinator"`
	Alarms      []map[string]any `json:"alarms"`
	Incomplete  map[string]any   `json:"incomplete"`
}

func decodeAlarms1829(t *testing.T, out string) alarmsJSON1829 {
	t.Helper()
	var d alarmsJSON1829
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return d
}

func alarmIDs1829(d alarmsJSON1829) []string {
	ids := []string{}
	for _, a := range d.Alarms {
		ids = append(ids, a["id"].(string))
	}
	return ids
}

// The default reads alarms IN EFFECT from the server's filter, newest first,
// alarms only; every row names its task, assignee and the assignee's roster
// state as the server returned it.
func TestTeamAlarms1829DefaultAsksForInEffectAndJoinsTaskAndRoster(t *testing.T) {
	out, _, calls, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(
		alarmEvent1829("a1", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", ""),
		alarmEvent1829("a2", "t2", "stall.not-started", "2026-11-17T17:00:00Z", "", "", ""),
	), "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	pc := pageCalls1919(calls)
	if len(pc) != 1 {
		t.Fatalf("%d alarm reads", len(pc))
	}
	v := pc[0].Vars
	if v["alarmsInEffect"] != true || v["alarmsOnly"] != true || v["newestFirst"] != true {
		t.Fatalf("the read must be newest-first alarms in effect: %v", v)
	}
	d := decodeAlarms1829(t, out)
	if d.Scope != "team" || len(d.Alarms) != 2 {
		t.Fatalf("%+v", d)
	}
	a1, a2 := d.Alarms[0], d.Alarms[1]
	task := a1["task"].(map[string]any)
	if task["handle"] != "AT0.1" || task["title"] != "title of t1" || task["status"] != "STARTED" {
		t.Fatalf("task join: %v", task)
	}
	roster := a1["assigneeRoster"].(map[string]any)
	if roster["state"] != "WAITING" || roster["liveSession"] != true {
		t.Fatalf("roster state is the server's, unrelabelled: %v", roster)
	}
	// An assignee no host lists: NOT_LAUNCHABLE, and the check status says why
	// there is no session (the pin names the field it asserts).
	roster2 := a2["assigneeRoster"].(map[string]any)
	if roster2["state"] != "NOT_LAUNCHABLE" || roster2["bound"] != false || roster2["liveSession"] != false {
		t.Fatalf("unbound assignee: %v", roster2)
	}
	check := roster2["checkStatus"].(map[string]any)
	if check["status"] != "NO_SESSION" || check["noSessionReason"] != "NEVER_BOUND" {
		t.Fatalf("checkStatus.status/noSessionReason: %v", check)
	}
	if a1["acknowledgedAt"] != nil || a1["acknowledgedBy"] != nil {
		t.Fatalf("unacknowledged alarm shows an ack: %v", a1)
	}
}

// A finished task is excluded by default, --status widens it; a task outside
// the team view and a task with no worker assignee behave as the team view
// does.
func TestTeamAlarms1829TaskScopeAndStatus(t *testing.T) {
	rows := []string{
		alarmEvent1829("a-t1", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", ""),
		alarmEvent1829("a-t3", "t3", "stall.no-progress", "2026-11-17T17:50:00Z", "", "", ""),
		alarmEvent1829("a-t4", "t4", "stall.no-progress", "2026-11-17T17:40:00Z", "", "", ""),
		alarmEvent1829("a-t5", "t5", "stall.no-progress", "2026-11-17T17:30:00Z", "", "", ""),
	}
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"team default: finished and other subtrees excluded", []string{"--team=worker:Marco"}, "a-t1,a-t5"},
		{"team with FINISHED", []string{"--team=worker:Marco", "--status", "FINISHED"}, "a-t3"},
		{"no --team is every task of the App", nil, "a-t1,a-t4,a-t5"},
		{"another coordinator's team", []string{"--team=worker:Ada"}, "a-t4"},
	}
	for _, c := range cases {
		out, _, _, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(rows...), append(c.argv, "--json")...)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		ids := alarmIDs1829(decodeAlarms1829(t, out))
		if got := strings.Join(ids, ","); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// --all stops asking for in-effect, shows acknowledged and past-escalation
// alarms with who acknowledged and when, and adds per task the acknowledged
// count and the task's last activity age.
func TestTeamAlarms1829AllIncludesAcknowledgedAndEndedWithTaskTotals(t *testing.T) {
	out, _, calls, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(
		alarmEvent1829("open", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", ""),
		alarmEvent1829("acked", "t1", "stall.not-started", "2026-11-17T17:00:00Z", "2026-11-17T17:10:00Z", "Marco", ""),
		alarmEvent1829("ended", "t1", "stall.owner-unreactive", "2026-11-17T16:00:00Z", "", "", "2026-11-17T16:30:00Z"),
	), "--team=worker:Marco", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if v := pageCalls1919(calls)[0].Vars; v["alarmsInEffect"] != false {
		t.Fatalf("--all must not ask for in-effect only: %v", v)
	}
	d := decodeAlarms1829(t, out)
	if got := strings.Join(alarmIDs1829(d), ","); got != "open,acked,ended" {
		t.Fatalf("ids %s", got)
	}
	acked := d.Alarms[1]
	by := acked["acknowledgedBy"].(map[string]any)
	if acked["acknowledgedAt"] != "2026-11-17T17:10:00Z" || by["name"] != "Marco" {
		t.Fatalf("who and when: %v", acked)
	}
	if d.Alarms[2]["escalationEndedAt"] != "2026-11-17T16:30:00Z" {
		t.Fatalf("ended alarm: %v", d.Alarms[2])
	}
	for _, a := range d.Alarms {
		if a["taskAcknowledgedAlarms"] != float64(1) {
			t.Fatalf("per-task acknowledged count: %v", a)
		}
		if _, ok := a["taskActivityAgeSeconds"].(float64); !ok {
			t.Fatalf("per-task activity age: %v", a)
		}
	}
	// Without --all those two fields are absent: the default shape is smaller.
	out, _, _, err = runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(alarmEvent1829("open", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", "")), "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "taskAcknowledgedAlarms") || strings.Contains(out, "taskActivityAgeSeconds") {
		t.Fatalf("--all-only fields leaked into the default: %s", out)
	}
}

// A task with 20+ alarms shows its newest in-effect one: the walk pages to the
// server's end across the boundary, sending the server's cursor as before.
func TestTeamAlarms1829PagesAcrossABoundary(t *testing.T) {
	pages := func(i int) string {
		if i == 0 {
			return page1919(rows1919(20, func(n int) string {
				return alarmEvent1829(fmt.Sprintf("a%02d", n), "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", "")
			}), true, "cursor-a19")
		}
		return page1919(rows1919(5, func(n int) string {
			return alarmEvent1829(fmt.Sprintf("b%02d", n), "t1", "stall.no-progress", "2026-11-17T12:00:00Z", "", "", "")
		}), false, "")
	}
	out, _, calls, err := runAlarms1829(t, alarmsTasks1829(), pages, "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	pc := pageCalls1919(calls)
	if len(pc) != 2 || pc[1].Vars["before"] != "cursor-a19" {
		t.Fatalf("second page must continue from the server's cursor: %+v", pc)
	}
	d := decodeAlarms1829(t, out)
	if len(d.Alarms) != 25 || d.Alarms[0]["id"] != "a00" || d.Incomplete != nil {
		t.Fatalf("%d alarms, first %v, incomplete %v", len(d.Alarms), d.Alarms[0]["id"], d.Incomplete)
	}
}

// hasMore with no cursor cannot be continued: say so rather than present a
// truncated list as complete.
func TestTeamAlarms1829StalledWalkIsMarkedIncomplete(t *testing.T) {
	page := page1919([]string{alarmEvent1829("a1", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", "")}, true, "")
	out, errOut, _, err := runAlarms1829(t, alarmsTasks1829(), static1919(page), "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	d := decodeAlarms1829(t, out)
	if d.Incomplete == nil || d.Incomplete["complete"] != false || !strings.Contains(errOut, "incomplete") {
		t.Fatalf("incomplete=%v stderr=%q", d.Incomplete, errOut)
	}
	text, _, _, err := runAlarms1829(t, alarmsTasks1829(), static1919(page), "--team=worker:Marco")
	if err != nil || !strings.Contains(text, "INCOMPLETE") {
		t.Fatalf("text output must say so: %v\n%s", err, text)
	}
	// An incomplete walk that found nothing must not claim "none in effect".
	empty := page1919(nil, true, "")
	text, _, _, err = runAlarms1829(t, alarmsTasks1829(), static1919(empty), "--team=worker:Marco")
	if err != nil || !strings.Contains(text, "INCOMPLETE") || strings.Contains(text, "no alarms in effect") {
		t.Fatalf("an incomplete empty read must not claim none in effect: %v\n%s", err, text)
	}
}

// --type and --since go to the server as filters.
func TestTeamAlarms1829TypeAndSinceAreServerFilters(t *testing.T) {
	_, _, calls, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(), "--team=worker:Marco",
		"--type", "stall.not-started,stall.no-progress", "--since", "2026-11-17T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	v := pageCalls1919(calls)[0].Vars
	types, _ := v["types"].([]any)
	got := []string{}
	for _, x := range types {
		got = append(got, x.(string))
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "stall.no-progress,stall.not-started" || v["since"] != "2026-11-17T00:00:00Z" {
		t.Fatalf("vars: %v", v)
	}
}

func TestTeamAlarms1829EmptyIsAnEmptyListAndSaysWhatItRead(t *testing.T) {
	out, _, _, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(), "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"alarms": []`) && !strings.Contains(out, `"alarms":[]`) {
		t.Fatalf("empty must render []: %s", out)
	}
	text, _, _, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(), "--team=worker:Marco")
	if err != nil || !strings.Contains(text, "no alarms in effect") || !strings.Contains(text, "team: Marco") {
		t.Fatalf("%v\n%s", err, text)
	}
}

// A bad flag exits 2 before any call.
func TestTeamAlarms1829BadInputExits2BeforeAnyRead(t *testing.T) {
	for _, argv := range [][]string{
		{"--team=Marco"},
		{"--team=user:holger"},
		{"--team=worker:Marco", "--status", "SOON"},
		{"--since", "yesterday"},
		{"--type=,"},
		{"unexpected"},
	} {
		_, _, calls, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(), argv...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: want exit 2, got %v", argv, err)
		}
		for _, c := range calls {
			if c.Op == "TeamAlarmRoster" || c.Op == pagedOp1919 || c.Op == "AgentTaskList" {
				t.Errorf("%v: must refuse before reading (%s)", argv, c.Op)
			}
		}
	}
}

// A coordinator not on the roster is not-found, not an empty view.
func TestTeamAlarms1829UnknownCoordinatorIsNotFound(t *testing.T) {
	_, _, _, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(), "--team=worker:Nobody")
	if exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("want exit 4, got %v", err)
	}
}

// The --json field set is the contract: pin the keys of an alarm row.
func TestTeamAlarms1829JSONFieldSetIsStable(t *testing.T) {
	out, _, _, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(
		alarmEvent1829("a1", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", "")), "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]any) string {
		ks := []string{}
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, ",")
	}
	if got := keys(d); got != "alarms,all,appId,coordinator,coordinatorSource,scope" {
		t.Fatalf("top-level keys: %s", got)
	}
	alarm := d["alarms"].([]any)[0].(map[string]any)
	want := "acknowledgedAt,acknowledgedBy,ageSeconds,assignee,assigneeRoster,createdAt,escalationEndedAt,id,severity,task,type"
	if got := keys(alarm); got != want {
		t.Fatalf("alarm keys: %s", got)
	}
	if got := keys(alarm["task"].(map[string]any)); got != "handle,id,status,title" {
		t.Fatalf("task keys: %s", got)
	}
}

// A bare --team is the bound worker's team (Iris here, wkr1 in the binding).
func TestTeamAlarms1829BareTeamIsTheBoundWorkersTeam(t *testing.T) {
	responses := taskResponses()
	responses["TeamAlarmRoster"] = `{"data":{"teamRoster":{"appId":"capp100000000000000000000","workers":[` +
		alarmsRosterRow1829("wkr1", "Iris", "", "AVAILABLE", true, true, checkOK1829) + `]}}}`
	responses["AgentTaskList"] = taskPage1044([]task1044{{id: "t1", handle: "AT0.1", owner: `{"id":"wkr1","kind":"WORKER","name":"Iris","urn":null}`}})
	out, _, _, err := pagesServer1919(t, responses, alarmsPage1829(
		alarmEvent1829("a1", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", "")), "team", "alarms", "--team", "--json")
	if err != nil {
		t.Fatal(err)
	}
	d := decodeAlarms1829(t, out)
	if d.Scope != "team" || d.Coordinator["name"] != "Iris" || len(d.Alarms) != 1 {
		t.Fatalf("%+v", d)
	}
}

// An alarm on a task no scope read returns is dropped, never shown task-less.
func TestTeamAlarms1829TasklessAndUnknownTaskAlarmsAreNotListed(t *testing.T) {
	taskless := strings.Replace(alarmEvent1829("dark", "x", "alarm.dark-worker", "2026-11-17T18:00:00Z", "", "", ""), `"taskId":"x"`, `"taskId":null`, 1)
	out, _, _, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(
		taskless, alarmEvent1829("ghost", "no-such-task", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", "")), "--json")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(decodeAlarms1829(t, out).Alarms); n != 0 {
		t.Fatalf("%d rows", n)
	}
}

// The text table carries the joined cells; hostile server text is sanitised.
func TestTeamAlarms1829TextRowAndSanitising(t *testing.T) {
	// The type carries a JSON-escaped ESC the fixture's %q cannot produce.
	row := strings.Replace(alarmEvent1829("a1", "t1", "stall.XXX", "2026-11-17T18:00:00Z", "2026-11-17T18:05:00Z", "Marco", ""), "XXX", `\u001b[31m`, 1)
	out, _, _, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(row), "--team=worker:Marco", "--all")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ALARM", "ROSTER", "ACKED", "IDLE", "a1", "WAITING", "title of t1", "✓ Marco"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.ContainsAny(out, "\x1b") {
		t.Fatalf("control bytes leaked: %q", out)
	}
}
