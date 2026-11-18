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
// (#2349 item 1: the field carried both meanings). The literals 100/e099/c099
// mirror alarmPageLimit and its per-page fixtures; a limit change fails here
// for an obvious cause.
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

// A stop before any event is read must not dangle an "at" with an empty id,
// in text or in the carried-across lastEventId (#2349 BLIND/LOCAL follow-ups).
func TestTeamAlarms2349PageLimitBeforeAnyEvent(t *testing.T) {
	// Every page empty: no event id exists to name.
	empty := func(i int) string {
		return page1919(nil, true, fmt.Sprintf("c%03d", i))
	}
	out, _, _, err := runAlarms1829(t, alarmsTasks1829(), empty, "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	inc := decodeAlarms1829(t, out).Incomplete
	if inc == nil || inc["reason"] != "page-limit" || inc["lastEventId"] != "" || inc["resumeCursor"] != "c099" {
		t.Fatalf("incomplete %v", inc)
	}
	text, _, _, err := runAlarms1829(t, alarmsTasks1829(), empty, "--team=worker:Marco")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "paging stopped: page-limit before reading any event (resume cursor c099) after 100 pages") {
		t.Fatalf("an event-less stop must say so, not dangle an at:\n%s", text)
	}
	// Events early, empty pages after: the last id read anywhere survives an
	// empty final page — it is not per-page state.
	late := func(i int) string {
		rows := []string{alarmEvent1829("e000", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", "")}
		if i > 0 {
			rows = nil
		}
		return page1919(rows, true, fmt.Sprintf("c%03d", i))
	}
	out, _, _, err = runAlarms1829(t, alarmsTasks1829(), late, "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if inc := decodeAlarms1829(t, out).Incomplete; inc["lastEventId"] != "e000" {
		t.Fatalf("an empty final page must not erase the last event read: %v", inc)
	}
}

// A null-cursor stall has no cursor to offer, so it carries none (#2349 LOCAL
// P3: the absence was unpinned).
func TestTeamAlarms2349StallWithNoCursorCarriesNoResumeCursor(t *testing.T) {
	page := page1919([]string{alarmEvent1829("a1", "t1", "stall.no-progress", "2026-11-17T18:00:00Z", "", "", "")}, true, "")
	out, _, _, err := runAlarms1829(t, alarmsTasks1829(), static1919(page), "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	inc := decodeAlarms1829(t, out).Incomplete
	if inc == nil || inc["reason"] != "cursor-stalled" || inc["lastEventId"] != "a1" {
		t.Fatalf("incomplete %v", inc)
	}
	if _, ok := inc["resumeCursor"]; ok {
		t.Fatalf("a stall offers no resume cursor — its cursor is known not to progress: %v", inc)
	}
}

// The count walk's repeated-cursor stop names the event and offers no cursor:
// the revisited cursor only re-reads pages, so handing it back as a resume
// point invites a loop (#2349 BLIND P2).
func TestTaskList2349RepeatedCursorStopNamesTheEventAndOffersNoCursor(t *testing.T) {
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
	if ac.LastEventID != "r1" || strings.Contains(out, "resumeCursor") {
		t.Fatalf("lastEventId %q — the event id, and no resume cursor offered: %s", ac.LastEventID, out)
	}
}

// A cursor cycle (A→B→A→…) is a stall the immediate-repeat check cannot see:
// both walks must stop as cursor-stalled at the revisit, not page to their
// own page bound (#2349 item 5c). The revisited cursor is not offered.
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
	if inc == nil || inc["reason"] != "cursor-stalled" || inc["pages"] != float64(3) || inc["lastEventId"] != "e002" {
		t.Fatalf("incomplete %v", inc)
	}
	if _, ok := inc["resumeCursor"]; ok {
		t.Fatalf("a stalled cycle offers no resume cursor: %v", inc)
	}
}

// The count walk's page-limit stop is pinned to the same shape as the alarms
// walk's — an event id, with the cursor named separately — so a revert to
// storing the cursor reddens here, not only where the id happens to be
// non-empty either way. Its INCOMPLETE text line (explain()) is pinned too.
// The literals mirror alarmPageLimit (see the alarms-walk test above).
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
	text, _, err := list1919(t, pages)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "paging stopped: page-limit at e099 (resume cursor c099) after 100 pages") {
		t.Fatalf("the count walk's INCOMPLETE line:\n%s", text)
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
	if ac.LastEventID != "e2" || strings.Contains(out, "resumeCursor") {
		t.Fatalf("lastEventId %q, and no resume cursor offered: %s", ac.LastEventID, out)
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
	// SafeCell neutralises the control rune to a space and keeps the rest, so
	// the name is still readable — not dropped — and carries no ESC.
	if !strings.Contains(out, "team: Mar [31mco") || strings.ContainsAny(out, "\x1b") {
		t.Fatalf("the header must sanitise the coordinator's name without dropping it:\n%q", out)
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

// ACKED is reported as the walked set's count, beside the incomplete marker —
// under an incomplete walk the two travel together, the count being of what
// was read, not of the server's total (#2349 item 4b).
func TestTeamAlarms2349AckedIsTheWalkedSetBesideTheIncompleteMarker(t *testing.T) {
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
