package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// #858 over server #1851 (cor:agt:060:02): a worker's availability mark
// whose until passed is rewritten to AVAILABLE, set by LAPSE at the instant
// it lapsed (until and setByParty null), and recorded by exactly one
// availability.changed event with no actor, carrying setBy "lapse" and
// lapsedAt. Both renderings must read it as a lapse.

const lapsedAvailabilityJSON = `{"workers":[
	{"worker":{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"},
	 "state":"AVAILABLE","reason":null,"until":null,
	 "setAt":"2026-10-13T18:00:00Z","setBy":"LAPSE","setByParty":null,
	 "inEffect":false}],
	"families":[]}`

const lapseEventJSON = `{"id":"evt9","appId":"capp100000000000000000000","taskId":null,
	"type":"availability.changed","severity":null,"createdAt":"2026-10-13T18:00:41Z",
	"actor":null,"subjectWorker":{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"},
	"payload":{"state":"available","reason":null,"until":null,"setBy":"lapse","lapsedAt":"2026-10-13T18:00:00.000Z"},
	"acknowledgedAt":null,"acknowledgedBy":null,"episodeStart":null,"chain":null,"wakes":null}`

// A platform-made availability change that is NOT a lapse (the stall rule
// clearing a mark): no actor either, and it must keep rendering as today.
const stallRuleEventJSON = `{"id":"evt10","appId":"capp100000000000000000000","taskId":null,
	"type":"availability.changed","severity":null,"createdAt":"2026-10-13T19:00:00Z",
	"actor":null,"subjectWorker":{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"},
	"payload":{"state":"available","reason":null,"until":null,"setBy":"stall-rule"},
	"acknowledgedAt":null,"acknowledgedBy":null,"episodeStart":null,"chain":null,"wakes":null}`

func lapseResponses() map[string]string {
	r := taskResponses()
	r["TeamAvailability"] = `{"data":{"teamAvailability":` + lapsedAvailabilityJSON + `}}`
	r["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[` + lapseEventJSON + `,` + stallRuleEventJSON + `]}}`
	return r
}

func tableLine(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no line starting %q in:\n%s", prefix, out)
	return ""
}

func TestTeamAvailabilityGetRendersLapse(t *testing.T) {
	_, out, err := runTeamTask(t, lapseResponses(), "team", "availability", "get")
	if err != nil {
		t.Fatal(err)
	}
	row := tableLine(t, out, "Rufus")
	if !strings.Contains(row, "AVAILABLE (lapsed at 2026-10-13T18:00:00Z)") {
		t.Fatalf("lapsed record not rendered as a lapse: %q", row)
	}
}

func TestTeamAvailabilityGetJSONCarriesSetBy(t *testing.T) {
	_, out, err := runTeamTask(t, lapseResponses(), "team", "availability", "get", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Workers []map[string]any `json:"workers"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(dto.Workers) != 1 {
		t.Fatalf("workers %s", out)
	}
	w := dto.Workers[0]
	if w["setBy"] != "LAPSE" || w["setAt"] != "2026-10-13T18:00:00Z" || w["state"] != "AVAILABLE" {
		t.Fatalf("lapsed record JSON %v", w)
	}
}

// The older-server control: a server without LAPSE sends one of the setter
// classes it always had. That record renders exactly as before — no lapse
// wording — and its JSON names the setter it sent.
func TestTeamAvailabilityGetNonLapseUnchanged(t *testing.T) {
	_, out, err := runTeamTask(t, taskResponses(), "team", "availability", "get")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "lapsed") {
		t.Fatalf("a non-lapse record reads as a lapse:\n%s", out)
	}
	if row := tableLine(t, out, "Rufus"); !strings.Contains(row, "UNAVAILABLE") || !strings.Contains(row, "OUT_OF_TOKENS") {
		t.Fatalf("row %q", row)
	}
	_, js, err := runTeamTask(t, taskResponses(), "team", "availability", "get", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, `"setBy": "WORKER"`) {
		t.Fatalf("JSON lacks the record's setter:\n%s", js)
	}
}

func TestTeamAvailabilitySetJSONCarriesSetBy(t *testing.T) {
	_, out, err := runTeamTask(t, taskResponses(), "team", "availability", "set",
		"--worker", "Rufus", "--state", "unavailable", "--reason", "OUT_OF_TOKENS", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"setBy": "APP_MANAGER"`) {
		t.Fatalf("set receipt JSON lacks the setter:\n%s", out)
	}
}

func TestTeamEventsRendersLapse(t *testing.T) {
	_, out, err := runTeamTask(t, lapseResponses(), "team", "events")
	if err != nil {
		t.Fatal(err)
	}
	if row := tableLine(t, out, "evt9"); !strings.Contains(row, "lapse at 2026-10-13T18:00:00.000Z") {
		t.Fatalf("lapse event not rendered as a lapse: %q", row)
	}
	// Control: the stall rule's actor-less change keeps today's blank actor.
	if row := tableLine(t, out, "evt10"); strings.Contains(row, "lapse") || !strings.Contains(row, "—") {
		t.Fatalf("non-lapse platform change misrendered: %q", row)
	}
}

// The event's JSON is the payload as the server sent it: setBy and lapsedAt
// already ride there, so no new field is added (the issue: a JSON field only
// if the human output gains one the JSON lacks).
func TestTeamEventsLapseJSONKeepsPayload(t *testing.T) {
	_, out, err := runTeamTask(t, lapseResponses(), "team", "events", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var events []struct {
		ID      string         `json:"id"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(events) != 2 || events[0].Payload["setBy"] != "lapse" || events[0].Payload["lapsedAt"] != "2026-10-13T18:00:00.000Z" {
		t.Fatalf("lapse event JSON %s", out)
	}
}
