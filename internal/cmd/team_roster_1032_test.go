package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#1032: `team roster` renders the server's heldBy/heldAt (server#2223) —
// holder + since in text, additively in --json — and adds no masking of its own.

// rosterHeld1032 is a roster row with the holder fields spliced in. heldBy and
// heldAt are JSON literals so a fixture can spell null.
func rosterHeld1032(name, heldBy, heldAt string) string {
	row := rosterRow993(name, "AVAILABLE", true, `"m"`, `"high"`)
	return strings.TrimSuffix(row, "}") + `,"heldBy":` + heldBy + `,"heldAt":` + heldAt + `}`
}

func TestTeamRosterRendersHolderAndSince(t *testing.T) {
	_, out, err := runTeamTask(t, rosterResponses993(
		rosterHeld1032("Ann", `{"id":"u1","handle":"holger","urn":"hrn:user:holger","name":"Holger S"}`, `"2026-10-24T01:00:00Z"`),
	), "team", "roster")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HELD BY", "SINCE", "Holger S", "2026-10-24T01:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("text missing %q:\n%s", want, out)
		}
	}
}

// A withheld name and an unset name both arrive as name:null — the two must
// render byte-identically, in text and in --json.
func TestTeamRosterWithheldNameRendersLikeUnset(t *testing.T) {
	held := func(name string) string {
		return rosterHeld1032("Ann", `{"id":"u1","handle":"holger","urn":"hrn:user:holger","name":`+name+`}`, `"2026-10-24T01:00:00Z"`)
	}
	_, a, err := runTeamTask(t, rosterResponses993(held("null")), "team", "roster")
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := runTeamTask(t, rosterResponses993(held("null")), "team", "roster")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("null-name renders differ:\n%s\nvs\n%s", a, b)
	}
	if strings.Contains(a, "Holger") {
		t.Errorf("a null name must not be filled in locally:\n%s", a)
	}
	if !strings.Contains(a, "holger") {
		t.Errorf("handle should identify the holder when name is null:\n%s", a)
	}
}

func TestTeamRosterJSONHolderIsAdditive(t *testing.T) {
	_, out, err := runTeamTask(t, rosterResponses993(
		rosterHeld1032("Ann", `{"id":"u1","handle":"holger","urn":"hrn:user:holger","name":null}`, `"2026-10-24T01:00:00Z"`),
		rosterHeld1032("Bo", "null", "null"),
	), "team", "roster", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Workers []map[string]json.RawMessage `json:"workers"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Workers) != 2 {
		t.Fatalf("want 2 workers: %s", out)
	}
	var holder map[string]*string
	if err := json.Unmarshal(got.Workers[0]["heldBy"], &holder); err != nil {
		t.Fatalf("heldBy: %v in %s", err, out)
	}
	if v, ok := holder["name"]; !ok || v != nil {
		t.Errorf("name must be present and null, got %v (present=%v)", v, ok)
	}
	if v := holder["handle"]; v == nil || *v != "holger" {
		t.Errorf("handle: %v", v)
	}
	if string(got.Workers[0]["heldAt"]) != `"2026-10-24T01:00:00Z"` {
		t.Errorf("heldAt: %s", got.Workers[0]["heldAt"])
	}
	// unheld: both keys present as null, not omitted
	if string(got.Workers[1]["heldBy"]) != "null" || string(got.Workers[1]["heldAt"]) != "null" {
		t.Errorf("unheld worker: heldBy=%s heldAt=%s", got.Workers[1]["heldBy"], got.Workers[1]["heldAt"])
	}
	// existing keys unchanged
	for _, k := range []string{"worker", "state", "role", "model", "tier", "alarms", "reportsTo"} {
		if _, ok := got.Workers[0][k]; !ok {
			t.Errorf("existing key %q lost", k)
		}
	}
}
