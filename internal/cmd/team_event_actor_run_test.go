package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// #854 (server #1833/#1834): a headless run that acts has no party, so its
// event's actor is null and actorRunId names the run. The events table says
// "run <id>" for it; the JSON carries actorRunId as a nullable field. A
// person, worker, lapse or unnamed platform event renders as before.
func teamEventsWithActorRun() map[string]string {
	ev := func(id, typ, actor, runID, payload string) string {
		return `{"id":"` + id + `","appId":"capp100000000000000000000","taskId":null,
	"type":"` + typ + `","severity":null,"createdAt":"2026-10-18T08:00:00Z",
	"actor":` + actor + `,"actorRunId":` + runID + `,"subjectWorker":null,
	"payload":` + payload + `,"acknowledgedAt":null,"acknowledgedBy":null,"episodeStart":null,"chain":null,"wakes":null}`
	}
	holger := `{"id":"u1","kind":"USER","name":"holger","urn":null}`
	rufus := `{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"}`
	r := taskResponses()
	r["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[` + strings.Join([]string{
		ev("evtRun", "task.created", "null", `"run_42"`, `{}`),
		ev("evtUser", "task.created", holger, "null", `{}`),
		ev("evtWorker", "task.started", rufus, "null", `{}`),
		ev("evtLapse", "availability.changed", "null", "null", `{"setBy":"lapse"}`),
		ev("evtNone", "task.created", "null", "null", `{}`),
	}, ",") + `]}}`
	return r
}

func TestTeamEventsNamesTheActingRun(t *testing.T) {
	calls, out, err := runTeamTask(t, teamEventsWithActorRun(), "team", "events")
	if err != nil {
		t.Fatal(err)
	}
	selected := false
	for _, c := range *calls {
		if c.Op == "AgentTeamEvents" && strings.Contains(c.Query, "actorRunId") {
			selected = true
		}
	}
	if !selected {
		t.Fatalf("no AgentTeamEvents call selects actorRunId: %+v", *calls)
	}
	// ACTOR is the fourth column (ID AT TYPE ACTOR TASK ACK); ACK is blank
	// for a non-alarm and the run cell is two words, so take everything
	// between the type and the task cell.
	actor := func(id string) string {
		f := strings.Fields(tableLine(t, out, id))
		if len(f) < 5 {
			t.Fatalf("%s: row too short: %v", id, f)
		}
		return strings.Join(f[3:len(f)-1], " ")
	}
	for id, want := range map[string]string{
		"evtRun":    "run run_42",
		"evtUser":   "holger",
		"evtWorker": "Rufus",
		"evtLapse":  "lapse",
		"evtNone":   "—",
	} {
		if got := actor(id); got != want {
			t.Fatalf("%s: ACTOR %q, want %q\n%s", id, got, want, out)
		}
	}
}

func TestTeamEventsJSONCarriesNullableActorRunID(t *testing.T) {
	_, out, err := runTeamTask(t, teamEventsWithActorRun(), "team", "events", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var events []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := map[string]string{"evtRun": `"run_42"`, "evtUser": "null", "evtWorker": "null", "evtLapse": "null", "evtNone": "null"}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d:\n%s", len(events), len(want), out)
	}
	for _, e := range events {
		var id string
		_ = json.Unmarshal(e["id"], &id)
		raw, ok := e["actorRunId"]
		if !ok {
			t.Fatalf("%s: no actorRunId key (it must be present, null when absent):\n%s", id, out)
		}
		if string(raw) != want[id] {
			t.Fatalf("%s: actorRunId %s, want %s", id, raw, want[id])
		}
	}
	// A run is never a party: the actor stays null beside the run id.
	if string(events[0]["actor"]) != "null" {
		t.Fatalf("evtRun: actor %s, want null", events[0]["actor"])
	}
}
