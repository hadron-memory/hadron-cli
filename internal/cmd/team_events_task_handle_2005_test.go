package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#2005 (S6c, server#2553 item 6 over server#4135): `team events` prints a
// task event's handle first, then the short hex, in its TASK cell, and --json
// carries taskHandle beside taskId exactly as the server sent it. Task-less
// rows and rows whose handle the server could not name keep today's cells.
const (
	handleEventTaskID = "01a1d71218bb78b3ae92d8cdcc49c962"
	handleEventShort  = "01a1d71218bb"
)

func teamEventsWithHandles() map[string]string {
	ev := func(id, typ, taskID, taskHandle, subject, payload string) string {
		return `{"id":"` + id + `","appId":"capp100000000000000000000","taskId":` + taskID + `,"taskHandle":` + taskHandle + `,
	"type":"` + typ + `","severity":null,"createdAt":"2026-11-13T22:40:49.852Z",
	"actor":null,"actorRunId":null,"subjectWorker":` + subject + `,
	"payload":` + payload + `,"acknowledgedAt":null,"acknowledgedBy":null,"episodeStart":null,"chain":null,"wakes":null}`
	}
	rufus := `{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"}`
	r := taskResponses()
	r["AgentTeamEventsPage"] = `{"data":{"agentTeamEventsPage":{"events":[` + strings.Join([]string{
		ev("evtHandle", "task.created", `"`+handleEventTaskID+`"`, `"AT0.7234"`, "null", `{}`),
		// The task no longer exists: the server sends a null handle, never an invented one.
		ev("evtGone", "task.finished", `"`+handleEventTaskID+`"`, "null", "null", `{}`),
		ev("evtCadence", "alarm.coordinator-cadence", "null", "null", rufus, `{}`),
		ev("evtDark", "alarm.dark-worker", "null", "null", rufus, `{"reason":"overdue"}`),
		ev("evtNone", "availability.changed", "null", "null", "null", `{}`),
	}, ",") + `],"hasMore":false,"nextCursor":null}}}`
	return r
}

func TestTeamEventsTaskCellIsHandleFirst2005(t *testing.T) {
	calls, out, err := runTeamTask(t, teamEventsWithHandles(), "team", "events")
	if err != nil {
		t.Fatal(err)
	}
	selected := false
	for _, c := range *calls {
		if c.Op == "AgentTeamEventsPage" && strings.Contains(c.Query, "taskHandle") {
			selected = true
		}
	}
	if !selected {
		t.Fatalf("no AgentTeamEventsPage call selects taskHandle: %+v", *calls)
	}
	// ID AT TYPE ACTOR TASK ACK: take the task cell as everything after the
	// actor, which is "—" for these rows.
	cell := func(id string) string {
		f := strings.Fields(tableLine(t, out, id))
		if len(f) < 5 {
			t.Fatalf("%s: row too short: %v", id, f)
		}
		return strings.Join(f[4:], " ")
	}
	for id, want := range map[string]string{
		"evtHandle":  "AT0.7234 " + handleEventShort,
		"evtGone":    handleEventTaskID, // no handle from the server: today's bare id
		"evtCadence": "coordinator Rufus",
		"evtDark":    "worker Rufus (overdue)",
		"evtNone":    "—",
	} {
		if got := cell(id); got != want {
			t.Fatalf("%s: TASK %q, want %q\n%s", id, got, want, out)
		}
	}
}

func TestTeamEventsJSONCarriesTaskHandle2005(t *testing.T) {
	_, out, err := runTeamTask(t, teamEventsWithHandles(), "team", "events", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Events []map[string]json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := map[string][2]string{
		"evtHandle":  {`"AT0.7234"`, `"` + handleEventTaskID + `"`},
		"evtGone":    {"null", `"` + handleEventTaskID + `"`},
		"evtCadence": {"null", "null"},
		"evtDark":    {"null", "null"},
		"evtNone":    {"null", "null"},
	}
	if len(page.Events) != len(want) {
		t.Fatalf("got %d events, want %d:\n%s", len(page.Events), len(want), out)
	}
	for _, e := range page.Events {
		var id string
		_ = json.Unmarshal(e["id"], &id)
		handle, ok := e["taskHandle"]
		if !ok {
			t.Fatalf("%s: no taskHandle key (present, null when absent):\n%s", id, out)
		}
		if string(handle) != want[id][0] || string(e["taskId"]) != want[id][1] {
			t.Fatalf("%s: taskHandle %s taskId %s, want %v", id, handle, e["taskId"], want[id])
		}
	}
}

func TestTeamEventsTaskFilterKeepsSendingTheValueAsGiven2005(t *testing.T) {
	for _, value := range []string{"AT0.7234", handleEventTaskID} {
		calls, _, err := runTeamTask(t, teamEventsWithHandles(), "team", "events", "--task", value)
		if err != nil {
			t.Fatal(err)
		}
		var got any
		for _, c := range *calls {
			if c.Op == "AgentTeamEventsPage" {
				got = c.Vars["taskId"]
			}
		}
		if got != value {
			t.Fatalf("--task %s: taskId variable %v, want it unchanged", value, got)
		}
	}
}

// A server that does not have the field refuses the whole query: the command
// fails with the existing schema-mismatch error and never prints a half table.
func TestTeamEventsAgainstAServerWithoutTaskHandleFailsWithoutATable2005(t *testing.T) {
	r := taskResponses()
	r["AgentTeamEventsPage"] = `{"errors":[{"message":"Cannot query field \"taskHandle\" on type \"AgentTeamEvent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	_, out, err := runTeamTask(t, r, "team", "events")
	if err == nil {
		t.Fatalf("a refused query must fail the command:\n%s", out)
	}
	if strings.Contains(out, "TASK") || strings.Contains(out, "evt") {
		t.Fatalf("half a table was printed:\n%s", out)
	}
}
