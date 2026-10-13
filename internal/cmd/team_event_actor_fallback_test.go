package cmd

import (
	"strings"
	"testing"
)

// #861 (Anne's P3-2 on #860): eventActorCell's fallbacks. Only an actor-less
// availability.changed whose payload object says setBy "lapse" reads as a
// lapse; everything else keeps the actor cell it had before #860.
func TestTeamEventsActorCellFallbacks(t *testing.T) {
	ev := func(id, actor, payload string) string {
		return `{"id":"` + id + `","appId":"capp100000000000000000000","taskId":null,
	"type":"availability.changed","severity":null,"createdAt":"2026-10-13T18:00:41Z",
	"actor":` + actor + `,"subjectWorker":{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"},
	"payload":` + payload + `,"acknowledgedAt":null,"acknowledgedBy":null,"episodeStart":null,"chain":null,"wakes":null}`
	}
	holger := `{"id":"u1","kind":"USER","name":"holger","urn":null}`
	r := taskResponses()
	r["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[` + strings.Join([]string{
		ev("evtArr", "null", `["lapse"]`),
		ev("evtStr", "null", `"lapse"`),
		ev("evtActor", holger, `{"setBy":"lapse","lapsedAt":"2026-10-13T18:00:00.000Z"}`),
		ev("evtBare", "null", `{"setBy":"lapse"}`),
	}, ",") + `]}}`
	_, out, err := runTeamTask(t, r, "team", "events")
	if err != nil {
		t.Fatal(err)
	}
	// ACTOR is the fourth column (ID AT TYPE ACTOR TASK ACK); every cell
	// here is one word, so a field split finds it.
	actor := func(id string) string {
		f := strings.Fields(tableLine(t, out, id))
		if len(f) < 4 {
			t.Fatalf("%s: row too short: %v", id, f)
		}
		return f[3]
	}
	for id, want := range map[string]string{"evtArr": "—", "evtStr": "—", "evtActor": "holger", "evtBare": "lapse"} {
		if got := actor(id); got != want {
			t.Fatalf("%s: ACTOR %q, want %q", id, got, want)
		}
	}
}

// #861 (Anne's P3-1 on #860): the server's lapse sweep rewrites WORKER marks
// only (model-family records are not swept), so the group help must say so.
func TestTeamAvailabilityHelpScopesLapseToWorkers(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "availability", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	if !strings.Contains(help, "for a WORKER record, within a minute the server records the lapse") {
		t.Fatalf("help does not scope the lapse to worker records:\n%s", out.String())
	}
	if !strings.Contains(help, "a family record is never rewritten") {
		t.Fatalf("help does not say family records are not swept:\n%s", out.String())
	}
}
