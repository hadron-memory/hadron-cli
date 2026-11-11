package cmd

// cli#1919 — the agent-team-events reads move onto agentTeamEventsPage
// (cor:api:120:04: every page says whether more rows exist). These pins
// answer the gate's binding scope: the page end comes from the server, never
// from a row count; --newest/--before reach the newest end; an open-alarm walk
// that stops early says so in text AND JSON.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const pagedOp1919 = "AgentTeamEventsPage"

func eventRow1919(id string) string {
	return `{"id":"` + id + `","appId":"capp100000000000000000000","taskId":"task1","type":"task.created","severity":null,"createdAt":"2026-10-12T00:00:00Z","actor":null,"subjectWorker":null,"payload":{},"acknowledgedAt":null,"acknowledgedBy":null,"episodeStart":null,"chain":null,"wakes":null}`
}

func alarmRow1919(id, taskID string, acked bool) string {
	ack := "null"
	if acked {
		ack = `"2026-10-01T01:00:00Z"`
	}
	return `{"id":"` + id + `","appId":"capp100000000000000000000","taskId":"` + taskID + `","type":"stall.no-progress","severity":"ALARM","createdAt":"2026-10-01T00:00:00Z","actor":null,"subjectWorker":null,"payload":{},"acknowledgedAt":` + ack + `,"acknowledgedBy":null,"episodeStart":null,"chain":null,"wakes":null}`
}

// page1919 is one agentTeamEventsPage answer. next "" is a null cursor.
func page1919(rows []string, hasMore bool, next string) string {
	cursor := "null"
	if next != "" {
		cursor = `"` + next + `"`
	}
	return fmt.Sprintf(`{"data":{"agentTeamEventsPage":{"events":[%s],"hasMore":%t,"nextCursor":%s}}}`, strings.Join(rows, ","), hasMore, cursor)
}

func rows1919(n int, mk func(i int) string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = mk(i)
	}
	return out
}

// pagesServer1919 answers the i-th AgentTeamEventsPage call with pages(i).
func pagesServer1919(t *testing.T, responses map[string]string, pages func(call int) string, argv ...string) (out, errOut string, calls []attnCall, err error) {
	t.Helper()
	writeTeamBinding(t)
	n := 0
	setAttnResponse(responses, pagedOp1919, pages(0))
	srv, rec := attnServerHook(t, responses, func(op string) {
		if op == pagedOp1919 {
			setAttnResponse(responses, pagedOp1919, pages(n))
			n++
		}
	})
	setTeamBindingServer(t, srv.URL)
	f, o := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err = root.Execute()
	return o.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), *rec, err
}

func pageCalls1919(calls []attnCall) []attnCall {
	var out []attnCall
	for _, c := range calls {
		if c.Op == pagedOp1919 {
			out = append(out, c)
		}
	}
	return out
}

func static1919(page string) func(int) string { return func(int) string { return page } }

// ---- team events ----------------------------------------------------------

func TestTeamEvents1919FullFinalPageNamesNoContinuation(t *testing.T) {
	page := page1919(rows1919(3, func(i int) string { return eventRow1919(fmt.Sprintf("e%d", i)) }), false, "")
	out, _, calls, err := pagesServer1919(t, taskResponses(), static1919(page), "team", "events", "--limit", "3")
	if err != nil {
		t.Fatal(err)
	}
	if len(pageCalls1919(calls)) != 1 {
		t.Fatalf("calls %+v", calls)
	}
	if strings.Contains(out, "continue with") {
		t.Fatalf("a full last page (hasMore:false) read as more: %q", out)
	}
}

func TestTeamEvents1919HasMoreNamesTheServersCursor(t *testing.T) {
	// The cursor is NOT the last row's id: it must come from nextCursor.
	page := page1919(rows1919(2, func(i int) string { return eventRow1919(fmt.Sprintf("e%d", i)) }), true, "srv-cursor-7")
	out, _, _, err := pagesServer1919(t, taskResponses(), static1919(page), "team", "events", "--limit", "5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--after srv-cursor-7") || strings.Contains(out, "--after e1") {
		t.Fatalf("continuation not the server's cursor: %q", out)
	}
	out, _, _, err = pagesServer1919(t, taskResponses(), static1919(page), "team", "events", "--limit", "5", "--newest")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--newest --before srv-cursor-7") || strings.Contains(out, "--after") {
		t.Fatalf("newest continuation names the wrong flag: %q", out)
	}
}

func TestTeamEvents1919EmptyPagePrintsNoContinuation(t *testing.T) {
	out, _, _, err := pagesServer1919(t, taskResponses(), static1919(page1919(nil, false, "")), "team", "events")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "continue with") {
		t.Fatalf("empty page: %q", out)
	}
}

func TestTeamEvents1919JSONEnvelope(t *testing.T) {
	page := page1919([]string{eventRow1919("e1"), eventRow1919("e2")}, true, "cur")
	out, _, _, err := pagesServer1919(t, taskResponses(), static1919(page), "team", "events", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Events     []map[string]any `json:"events"`
		HasMore    *bool            `json:"hasMore"`
		NextCursor *string          `json:"nextCursor"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not the {events,hasMore,nextCursor} envelope: %v\n%s", err, out)
	}
	if len(got.Events) != 2 || got.Events[0]["id"] != "e1" || got.Events[1]["id"] != "e2" {
		t.Fatalf("events lost server order: %+v", got.Events)
	}
	if got.HasMore == nil || !*got.HasMore || got.NextCursor == nil || *got.NextCursor != "cur" {
		t.Fatalf("page end: %s", out)
	}
	// Empty page: events is [], nextCursor null — never a null array.
	out, _, _, err = pagesServer1919(t, taskResponses(), static1919(page1919(nil, false, "")), "team", "events", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"events": []`) && !strings.Contains(out, `"events":[]`) {
		t.Fatalf("empty events not []: %s", out)
	}
	if !strings.Contains(out, `"nextCursor": null`) && !strings.Contains(out, `"nextCursor":null`) {
		t.Fatalf("nextCursor not null: %s", out)
	}
}

func TestTeamEvents1919DirectionAndFilterWire(t *testing.T) {
	page := static1919(page1919([]string{eventRow1919("e1")}, false, ""))
	// Oldest-first default: neither newestFirst nor before is sent.
	_, _, calls, err := pagesServer1919(t, taskResponses(), page, "team", "events", "--after", "c1", "--alarms", "--since", "2026-10-12T00:00:00Z", "--task", "task1", "--type", "task.created", "--limit", "20")
	if err != nil {
		t.Fatal(err)
	}
	v := pageCalls1919(calls)[0].Vars
	if v["after"] != "c1" || v["alarmsOnly"] != true || v["since"] != "2026-10-12T00:00:00Z" || v["taskId"] != "task1" || v["limit"] != float64(20) {
		t.Fatalf("filters %v", v)
	}
	if _, ok := v["newestFirst"]; ok {
		t.Fatalf("newestFirst sent by default: %v", v)
	}
	if _, ok := v["before"]; ok {
		t.Fatalf("before sent by default: %v", v)
	}
	if _, ok := v["codePrefix"]; ok {
		t.Fatalf("codePrefix sent when the flag is absent: %v", v)
	}
	// Newest end: newestFirst true, the cursor in before, every filter kept.
	_, _, calls, err = pagesServer1919(t, taskResponses(), page, "team", "events", "--newest", "--before", "c9", "--alarms", "--code-prefix", "app.", "--task", "task1")
	if err != nil {
		t.Fatal(err)
	}
	v = pageCalls1919(calls)[0].Vars
	if v["newestFirst"] != true || v["before"] != "c9" || v["alarmsOnly"] != true || v["codePrefix"] != "app." || v["taskId"] != "task1" {
		t.Fatalf("newest wire %v", v)
	}
	if _, ok := v["after"]; ok {
		t.Fatalf("after sent with --newest: %v", v)
	}
}

func TestTeamEvents1919NewestReachesTheNewestEnd(t *testing.T) {
	// Oldest-first page one cannot show the newest alarm; --newest --alarms does.
	oldest := page1919([]string{alarmRow1919("old-1", "task1", true)}, true, "old-1")
	newest := page1919([]string{alarmRow1919("new-9", "task1", false)}, true, "new-9")
	for _, tc := range []struct {
		argv []string
		resp string
		want string
	}{
		{[]string{"team", "events", "--alarms"}, oldest, "old-1"},
		{[]string{"team", "events", "--alarms", "--newest"}, newest, "new-9"},
	} {
		out, _, calls, err := pagesServer1919(t, taskResponses(), static1919(tc.resp), tc.argv...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%v: %q", tc.argv, out)
		}
		if got := pageCalls1919(calls)[0].Vars["newestFirst"] == true; got != (len(tc.argv) == 4) {
			t.Fatalf("%v newestFirst=%v", tc.argv, got)
		}
	}
}

func TestTeamEvents1919RefusedDirectionsMakeNoRequest(t *testing.T) {
	for _, argv := range [][]string{
		{"team", "events", "--newest", "--after", "c1"},
		{"team", "events", "--before", "c1"},
	} {
		_, _, calls, err := pagesServer1919(t, taskResponses(), static1919(page1919(nil, false, "")), argv...)
		if err == nil || exitOf(err) != 2 {
			t.Fatalf("%v: err %v exit %d", argv, err, exitOf(err))
		}
		for _, c := range calls {
			if c.Op == pagedOp1919 || c.Op == "AgentTeamEvents" {
				t.Fatalf("%v made a request before refusing: %+v", argv, calls)
			}
		}
	}
}

// ---- open-alarm walk (task tree, task list --team) -------------------------

func alarmWalk1919(t *testing.T, pages func(int) string, argv ...string) (out, errOut string, calls []attnCall, err error) {
	t.Helper()
	return pagesServer1919(t, taskResponses(), pages, argv...)
}

func TestAlarmWalk1919FullFinalPageEndsWithoutAnotherRead(t *testing.T) {
	// Exactly 200 rows and hasMore:false is the LAST page. Red on main, which
	// read one page too many on a full final page.
	page := page1919(rows1919(200, func(i int) string { return alarmRow1919(fmt.Sprintf("a%d", i), "task9", true) }), false, "")
	out, errOut, calls, err := alarmWalk1919(t, static1919(page), "team", "task", "tree", "task1")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pageCalls1919(calls)); n != 1 {
		t.Fatalf("%d page reads for a one-page walk", n)
	}
	if strings.Contains(out, "INCOMPLETE") || strings.Contains(errOut, "incomplete") {
		t.Fatalf("complete walk flagged: out %q err %q", out, errOut)
	}
}

func TestAlarmWalk1919TraversesTwoHundredAndOneAlarms(t *testing.T) {
	pages := func(i int) string {
		if i == 0 {
			return page1919(rows1919(200, func(i int) string { return alarmRow1919(fmt.Sprintf("a%d", i), "task1", false) }), true, "a199")
		}
		return page1919([]string{alarmRow1919("a200", "task1", false)}, false, "")
	}
	out, _, calls, err := alarmWalk1919(t, pages, "team", "task", "tree", "task1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	pc := pageCalls1919(calls)
	if len(pc) != 2 || pc[1].Vars["after"] != "a199" || pc[0].Vars["limit"] != float64(200) {
		t.Fatalf("walk %+v", pc)
	}
	if _, ok := pc[0].Vars["newestFirst"]; ok {
		t.Fatal("the count walk went newest-first")
	}
	var tasks []map[string]any
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatal(err)
	}
	if tasks[0]["openAlarms"] != float64(201) {
		t.Fatalf("count %v", tasks[0]["openAlarms"])
	}
	if _, ok := tasks[0]["openAlarmsIncomplete"]; ok {
		t.Fatalf("complete walk carries the marker: %s", out)
	}
}

// incompleteWalk1919 stalls the walk three ways.
var incompleteWalks1919 = map[string]struct {
	pages  func(int) string
	reason string
}{
	"page-limit": {func(i int) string {
		return page1919(rows1919(200, func(j int) string { return alarmRow1919(fmt.Sprintf("a%d-%d", i, j), "task1", false) }), true, fmt.Sprintf("cur%d", i))
	}, "page-limit"},
	"null-cursor":     {static1919(page1919(rows1919(200, func(i int) string { return alarmRow1919(fmt.Sprintf("n%d", i), "task9", true) }), true, "")), "cursor-stalled"},
	"repeated-cursor": {static1919(page1919(rows1919(200, func(i int) string { return alarmRow1919(fmt.Sprintf("r%d", i), "task9", true) }), true, "same")), "cursor-stalled"},
}

func TestTaskTree1919IncompleteWalkIsMarkedInJSONAndText(t *testing.T) {
	for name, w := range incompleteWalks1919 {
		t.Run(name, func(t *testing.T) {
			out, errOut, calls, err := alarmWalk1919(t, w.pages, "team", "task", "tree", "task1", "--json")
			if err != nil {
				t.Fatal(err)
			}
			if n := len(pageCalls1919(calls)); n > 102 {
				t.Fatalf("%d reads: unbounded", n)
			}
			var tasks []map[string]any
			if err := json.Unmarshal([]byte(out), &tasks); err != nil {
				t.Fatal(err)
			}
			if tasks[0]["openAlarmsIncomplete"] != true {
				t.Fatalf("JSON reads as complete: %s", out)
			}
			if !strings.Contains(errOut, "incomplete") {
				t.Fatalf("stderr note lost: %q", errOut)
			}
			text, _, _, err := alarmWalk1919(t, w.pages, "team", "task", "tree", "task1")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "INCOMPLETE") || !strings.Contains(text, w.reason) {
				t.Fatalf("no INCOMPLETE line naming %q: %q", w.reason, text)
			}
		})
	}
}

func TestTaskTree1919IncompleteCountsAreLowerBounds(t *testing.T) {
	pages := func(i int) string {
		return page1919([]string{alarmRow1919(fmt.Sprintf("p%d", i), "task1", false)}, true, fmt.Sprintf("c%d", i))
	}
	// Open alarm on task1 on every page: the cap trips with a positive count.
	text, _, _, err := alarmWalk1919(t, pages, "team", "task", "tree", "task1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "≥") || !strings.Contains(text, "INCOMPLETE") {
		t.Fatalf("partial count rendered as complete: %q", text)
	}
}

func TestTaskTree1919ReadErrorMidWalkIsFatalWithNoStdout(t *testing.T) {
	pages := func(i int) string {
		if i == 0 {
			return page1919(rows1919(200, func(i int) string { return alarmRow1919(fmt.Sprintf("a%d", i), "task9", true) }), true, "a199")
		}
		return gqlErrorJSON("INTERNAL_SERVER_ERROR")
	}
	out, _, _, err := alarmWalk1919(t, pages, "team", "task", "tree", "task1")
	if err == nil {
		t.Fatal("mid-walk read error swallowed")
	}
	if out != "" {
		t.Fatalf("stdout on a failed walk: %q", out)
	}
}

func list1919(t *testing.T, pages func(int) string, argv ...string) (out, errOut string, err error) {
	t.Helper()
	responses := rosterResponses993(roster1044()...)
	responses["TeamReportingLines"] = responses["TeamRoster"]
	responses["AgentTaskList"] = taskPage1044(tasks1044())
	out, errOut, _, err = pagesServer1919(t, responses, pages, append([]string{"team", "task", "list", "--team=worker:Marco"}, argv...)...)
	return out, errOut, err
}

func TestTaskListTeam1919CompleteWalkCarriesNoMarkers(t *testing.T) {
	page := page1919([]string{alarmRow1919("a1", "t1", false)}, false, "")
	out, errOut, err := list1919(t, static1919(page), "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "openAlarmsIncomplete") || strings.Contains(out, "alarmCounts") || errOut != "" {
		t.Fatalf("complete walk marked: %s / %q", out, errOut)
	}
	text, _, err := list1919(t, static1919(page))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "INCOMPLETE") || strings.Contains(text, "≥") || strings.Contains(text, "?") {
		t.Fatalf("complete text marked: %q", text)
	}
}

func TestTaskListTeam1919IncompleteWalkMarksJSONAndText(t *testing.T) {
	for name, w := range incompleteWalks1919 {
		t.Run(name, func(t *testing.T) {
			out, _, err := list1919(t, w.pages, "--json")
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				AlarmCounts *struct {
					Complete    bool   `json:"complete"`
					Reason      string `json:"reason"`
					LastEventID string `json:"lastEventId"`
					Pages       int    `json:"pages"`
				} `json:"alarmCounts"`
				Tasks []map[string]any `json:"tasks"`
			}
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			if got.AlarmCounts == nil || got.AlarmCounts.Complete || got.AlarmCounts.Reason != w.reason || got.AlarmCounts.LastEventID == "" || got.AlarmCounts.Pages < 1 {
				t.Fatalf("alarmCounts %+v in %s", got.AlarmCounts, out)
			}
			if len(got.Tasks) == 0 {
				t.Fatal("no tasks")
			}
			for _, task := range got.Tasks {
				if task["openAlarmsIncomplete"] != true {
					t.Fatalf("task %v not marked incomplete", task["id"])
				}
			}
			text, _, err := list1919(t, w.pages)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "INCOMPLETE") {
				t.Fatalf("table has no INCOMPLETE line: %q", text)
			}
			// No task may show the none-dash in the ALARMS column.
			for _, line := range strings.Split(text, "\n") {
				if strings.Contains(line, "title of") && strings.HasSuffix(strings.TrimSpace(line), "—") {
					t.Fatalf("uncounted task renders as none: %q", line)
				}
			}
			tree, _, err := list1919(t, w.pages, "--tree")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(tree, "INCOMPLETE") {
				t.Fatalf("--tree has no INCOMPLETE line: %q", tree)
			}
		})
	}
}

func TestTaskListTeam1919IncompleteCountedAndUnknownCells(t *testing.T) {
	pages := func(i int) string {
		return page1919([]string{alarmRow1919(fmt.Sprintf("p%d", i), "t1", false)}, true, fmt.Sprintf("c%d", i))
	}
	text, _, err := list1919(t, pages)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "≥") || !strings.Contains(text, "?") {
		t.Fatalf("counted ≥N and unknown ? cells missing: %q", text)
	}
}
