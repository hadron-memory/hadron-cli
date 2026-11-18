package cmd

// hadron-cli#2349: the alarm walks' stop markers and filter slots, five P3s
// deferred from #2335. An alarmWalkStop claimed to carry the last event id
// but the page-limit stop (and the count walk's repeated-cursor stop) stored
// a page CURSOR there instead — an id-shaped lie in --json and in the
// INCOMPLETE line. A cursor CYCLE (A→B→A) paged to the walk's own page bound
// instead of stalling at the revisit. The pins that already hold on main are
// here so they stay held: the App scope omits coordinatorSource, a hostile
// coordinator name is sanitised in the text header (--json raw), an
// unreadable task timestamp omits IDLE rather than zeroing it, and ACKED
// counts only the alarms this command's walk read.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func sortedKeys2349(m map[string]any) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

type alarmCounts2349 struct {
	AlarmCounts *struct {
		Complete     bool   `json:"complete"`
		Reason       string `json:"reason"`
		LastEventID  string `json:"lastEventId"`
		ResumeCursor string `json:"resumeCursor"`
		Pages        int    `json:"pages"`
	} `json:"alarmCounts"`
}

// A page-limit stop must name an EVENT as the last thing read and keep the
// cursor it stopped at as resumeCursor — not store the cursor in lastEventId
// (#2349 item 1: the field carried both meanings).
func TestTeamAlarms2349PageLimitStopNamesTheEventAndKeepsTheCursor(t *testing.T) {
	pages := func(i int) string {
		return page1919([]string{alarmEvent1829(fmt.Sprintf("e%03d", i), "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", "")}, true, fmt.Sprintf("c%03d", i))
	}
	out, _, calls, err := runAlarms1829(t, alarmsTasks1829(), pages, "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pageCalls1919(calls)); n != 100 {
		t.Fatalf("%d page reads: the stop must be the walk's own page bound", n)
	}
	inc := decodeAlarms1829(t, out).Incomplete
	if inc == nil || inc["reason"] != "page-limit" || inc["pages"] != float64(100) {
		t.Fatalf("incomplete %v", inc)
	}
	if inc["lastEventId"] != "e099" || inc["resumeCursor"] != "c099" {
		t.Fatalf("lastEventId %v resumeCursor %v — an event id, with the cursor named separately", inc["lastEventId"], inc["resumeCursor"])
	}
	text, _, _, err := runAlarms1829(t, alarmsTasks1829(), pages, "--team=worker:Marco")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "paging stopped: page-limit at e099 (resume cursor c099) after 100 pages") {
		t.Fatalf("the INCOMPLETE line must name the event and the cursor:\n%s", text)
	}
}

// The count walk's repeated-cursor stop has the same two-meanings bug: the
// marker claimed an event id and stored the repeated cursor.
func TestTaskList2349RepeatedCursorStopNamesTheEventAndKeepsTheCursor(t *testing.T) {
	page := page1919([]string{alarmRow1919("r1", "t1", false)}, true, "same")
	out, _, err := list1919(t, static1919(page), "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got alarmCounts2349
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	ac := got.AlarmCounts
	if ac == nil || ac.Reason != "cursor-stalled" || ac.Pages != 2 {
		t.Fatalf("alarmCounts %+v in %s", ac, out)
	}
	if ac.LastEventID != "r1" || ac.ResumeCursor != "same" {
		t.Fatalf("lastEventId %q resumeCursor %q — the event id, with the cursor named separately", ac.LastEventID, ac.ResumeCursor)
	}
}

// A cursor cycle (A→B→A→…) is a stall the immediate-repeat check cannot see:
// both walks must stop as cursor-stalled at the revisit, not page to their
// own page bound (#2349 item 5c).
func TestTeamAlarms2349CyclingCursorStallsWithinTheCycle(t *testing.T) {
	pages := func(i int) string {
		cursor := "A"
		if i%2 == 1 {
			cursor = "B"
		}
		return page1919([]string{alarmEvent1829(fmt.Sprintf("e%03d", i), "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", "")}, true, cursor)
	}
	out, _, calls, err := runAlarms1829(t, alarmsTasks1829(), pages, "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pageCalls1919(calls)); n != 3 {
		t.Fatalf("%d page reads: a revisit must stop the walk", n)
	}
	inc := decodeAlarms1829(t, out).Incomplete
	if inc == nil || inc["reason"] != "cursor-stalled" || inc["pages"] != float64(3) ||
		inc["lastEventId"] != "e002" || inc["resumeCursor"] != "A" {
		t.Fatalf("incomplete %v", inc)
	}
}

// The count walk's page-limit stop is pinned to the same shape as the alarms
// walk's — an event id, with the cursor named separately — so a revert to
// storing the cursor reddens here, not only where the id happens to be
// non-empty either way.
func TestTaskList2349PageLimitStopNamesTheEventAndKeepsTheCursor(t *testing.T) {
	pages := func(i int) string {
		return page1919([]string{alarmRow1919(fmt.Sprintf("e%03d", i), "t1", false)}, true, fmt.Sprintf("c%03d", i))
	}
	out, _, err := list1919(t, pages, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got alarmCounts2349
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	ac := got.AlarmCounts
	if ac == nil || ac.Reason != "page-limit" || ac.Pages != 100 {
		t.Fatalf("alarmCounts %+v in %s", ac, out)
	}
	if ac.LastEventID != "e099" || ac.ResumeCursor != "c099" {
		t.Fatalf("lastEventId %q resumeCursor %q — an event id, with the cursor named separately", ac.LastEventID, ac.ResumeCursor)
	}
}

func TestTaskList2349CyclingCursorStallsTheCountWalk(t *testing.T) {
	pages := func(i int) string {
		cursor := "A"
		if i%2 == 1 {
			cursor = "B"
		}
		return page1919([]string{alarmRow1919(fmt.Sprintf("e%d", i), "t1", false)}, true, cursor)
	}
	out, _, err := list1919(t, pages, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got alarmCounts2349
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	ac := got.AlarmCounts
	if ac == nil || ac.Reason != "cursor-stalled" || ac.Pages != 3 {
		t.Fatalf("alarmCounts %+v in %s", ac, out)
	}
	if ac.LastEventID != "e2" || ac.ResumeCursor != "A" {
		t.Fatalf("lastEventId %q resumeCursor %q", ac.LastEventID, ac.ResumeCursor)
	}
}

// The --all walk's three boolean slots are pinned by NAME: a call-site slot
// swap must redden here, not ship silently (#2349 item 5a).
func TestTeamAlarms2349AllPinsEveryWalkFlagByName(t *testing.T) {
	_, _, calls, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(
		alarmEvent1829("a1", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", "")), "--team=worker:Marco", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	v := pageCalls1919(calls)[0].Vars
	if v["newestFirst"] != true || v["alarmsOnly"] != true || v["alarmsInEffect"] != false {
		t.Fatalf("--all walks every alarm newest-first, alarms only: %v", v)
	}
}

// The count walk (task list --team, task tree) asks for alarms IN EFFECT,
// OLDEST-first, through the after cursor — its slots pinned by name too.
func TestTaskList2349CountWalkPinsItsAlarmFiltersByName(t *testing.T) {
	pages := func(i int) string {
		if i == 0 {
			return page1919([]string{alarmRow1919("a1", "t1", false)}, true, "cur-1")
		}
		return page1919([]string{alarmRow1919("a2", "t1", false)}, false, "")
	}
	responses := rosterResponses993(roster1044()...)
	responses["TeamReportingLines"] = responses["TeamRoster"]
	responses["AgentTaskList"] = taskPage1044(tasks1044())
	_, _, calls, err := pagesServer1919(t, responses, pages, "team", "task", "list", "--team=worker:Marco")
	if err != nil {
		t.Fatal(err)
	}
	pc := pageCalls1919(calls)
	if len(pc) != 2 || pc[1].Vars["after"] != "cur-1" || pc[0].Vars["limit"] != float64(200) {
		t.Fatalf("walk %+v", pc)
	}
	v := pc[0].Vars
	if v["alarmsInEffect"] != true {
		t.Fatalf("the count walk asks for alarms in effect: %v", v)
	}
	if _, ok := v["alarmsOnly"]; ok {
		t.Fatalf("alarmsOnly is not the count walk's filter: %v", v)
	}
	if _, ok := v["newestFirst"]; ok {
		t.Fatalf("the count walk is oldest-first: %v", v)
	}
}

// The App scope carries no coordinatorSource: the key is team-scope-only
// (#2349 item 3). coordinator itself is null, not omitted.
func TestTeamAlarms2349AppScopeHasNoCoordinatorSource(t *testing.T) {
	out, _, _, err := runAlarms1829(t, alarmsTasks1829(), alarmsPage1829(
		alarmEvent1829("a1", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", "")), "--json")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	if _, ok := d["coordinatorSource"]; ok {
		t.Fatalf("coordinatorSource leaked into the App scope: %s", out)
	}
	if d["scope"] != "app" || d["coordinator"] != nil {
		t.Fatalf("scope %v coordinator %v", d["scope"], d["coordinator"])
	}
	if got := sortedKeys2349(d); got != "alarms,all,appId,coordinator,scope" {
		t.Fatalf("top-level keys: %s", got)
	}
}

// The header's coordinator cell goes through SafeCell: a hostile worker name
// cannot put control bytes into the text output, and --json keeps the
// server's bytes raw (#2349 item 2).
func TestTeamAlarms2349HostileCoordinatorNameIsSanitised(t *testing.T) {
	row := strings.Replace(
		alarmsRosterRow1829("wkr1", "NAME2349", "", "AVAILABLE", true, true, checkOK1829),
		"NAME2349", `Mar\u001b[31mco`, 1)
	responses := taskResponses()
	responses["TeamAlarmRoster"] = `{"data":{"teamRoster":{"appId":"capp100000000000000000000","workers":[` + row + `]}}}`
	responses["AgentTaskList"] = taskPage1044([]task1044{{id: "t1", handle: "AT0.1", owner: `{"id":"wkr1","kind":"WORKER","name":"Iris","urn":null}`}})
	page := alarmsPage1829(alarmEvent1829("a1", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", ""))
	out, _, _, err := pagesServer1919(t, responses, page, "team", "alarms", "--team")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "team: ") || strings.ContainsAny(out, "\x1b") {
		t.Fatalf("the header must sanitise the coordinator's name:\n%q", out)
	}
	jout, _, _, err := pagesServer1919(t, responses, page, "team", "alarms", "--team", "--json")
	if err != nil {
		t.Fatal(err)
	}
	d := decodeAlarms1829(t, jout)
	name, _ := d.Coordinator["name"].(string)
	if !strings.Contains(name, "\x1b") {
		t.Fatalf("--json preserves the server's name bytes: %q", name)
	}
}

// IDLE is absent, not zero, when the task's own updatedAt cannot be read —
// the age is unknown, the roster's rule for a missing timestamp (#2349 item
// 4a). The rest of the row is unaffected.
func TestTeamAlarms2349AllWithUnreadableTaskTimestampOmitsIDLE(t *testing.T) {
	tasks := []task1044{
		{id: "t1", handle: "AT0.1", owner: worker1044("Marco"), assignee: worker1044("Jonas"), updatedAt: "not-a-timestamp"},
		{id: "t2", handle: "AT0.2", owner: worker1044("Marco"), assignee: worker1044("Jonas"), updatedAt: "2026-11-16T00:00:00Z"},
	}
	out, _, _, err := runAlarms1829(t, tasks, alarmsPage1829(
		alarmEvent1829("a1", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", ""),
		alarmEvent1829("a2", "t2", "stall.no-progress", "2026-11-17T17:00:00Z", "", "", ""),
	), "--team=worker:Marco", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	d := decodeAlarms1829(t, out)
	row1, row2 := d.Alarms[0], d.Alarms[1]
	if _, ok := row1["taskActivityAgeSeconds"]; ok {
		t.Fatalf("an unreadable timestamp has no IDLE, not a number: %v", row1)
	}
	if _, ok := row2["taskActivityAgeSeconds"].(float64); !ok {
		t.Fatalf("a readable timestamp shows IDLE: %v", row2)
	}
	for _, r := range []map[string]any{row1, row2} {
		if r["taskAcknowledgedAlarms"] != float64(0) {
			t.Fatalf("ACKED is unaffected: %v", r)
		}
	}
}

// ACKED counts the alarms THIS command's walk read — with an incomplete walk
// it is a lower bound, so the row carries the count and the marker together
// (#2349 item 4b).
func TestTeamAlarms2349AckedCountsCoverOnlyTheWalkedSet(t *testing.T) {
	page := page1919([]string{
		alarmEvent1829("open", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", ""),
		alarmEvent1829("acked", "t1", "stall.not-started", "2026-11-17T17:00:00Z", "2026-11-17T17:10:00Z", "Marco", ""),
	}, true, "")
	out, _, _, err := runAlarms1829(t, alarmsTasks1829(), static1919(page), "--team=worker:Marco", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	d := decodeAlarms1829(t, out)
	if d.Incomplete == nil {
		t.Fatal("the stalled walk must be marked incomplete")
	}
	if d.Alarms[0]["taskAcknowledgedAlarms"] != float64(1) {
		t.Fatalf("ACKED counts the walked set: %v", d.Alarms[0])
	}
}
