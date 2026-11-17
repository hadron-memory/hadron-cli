package hostup

import (
	"strings"
	"testing"
)

// cli#2201 C10: a short-lived worker (NO_OPEN_TASK) whose session is still
// open with no agent terminal is not "in sync": its session should end. host
// up reports it and acts on nothing; the host poller owns the proof and the
// end, because the proof is the strict process census and the end is a host-key
// call (the manager credential host up runs under cannot make it).
func TestAgentlessShortLivedSessionIsReportedNotInSync2201(t *testing.T) {
	for _, mode := range []string{SweepApply, "OBSERVE"} {
		w := Worker{WorkerID: "w", Name: "W", Desired: Stopped, Reason: "NO_OPEN_TASK", SessionID: "s1", Setup: setup("w")}
		e := Engine{Source: fakeSource{snap: Snapshot{HostID: "h1", SweepMode: mode, Workers: []Worker{w}}}, Observer: fakeObserver{"w": {Registered: true, WorktreeExists: true}}, Actuator: queueActuator()}
		res := run(t, e, Options{Apply: true})
		if len(res.Planned) != 0 || len(res.Applied) != 0 || len(res.Failed) != 0 || len(res.Skipped) != 1 {
			t.Fatalf("%s: planned %v applied %v skipped %v", mode, lines(res.Planned), lines(res.Applied), lines(res.Skipped))
		}
		it := res.Skipped[0]
		if it.Reason != ReasonAgentlessSession || it.Action != ActionSessionEnd || !strings.Contains(it.Detail, "host poller") {
			t.Fatalf("%s: %+v", mode, it)
		}
	}
}

func TestAgentlessReportOnlyForTheShapeThatIsOne2201(t *testing.T) {
	obs := func(terms int) Observation {
		return Observation{Registered: true, WorktreeExists: true, AgentTerminals: terms}
	}
	for _, r := range []struct {
		name   string
		w      Worker
		obs    Observation
		reason string
	}{
		{"a running agent still stops through worker.end", Worker{Desired: Stopped, Reason: "NO_OPEN_TASK", SessionID: "s1"}, obs(1), ReasonStopPending},
		{"no open session is in sync", Worker{Desired: Stopped, Reason: "NO_OPEN_TASK"}, obs(0), ReasonInSync},
		{"a stop reason this engine was not taught is not agentless", Worker{Desired: Stopped, Reason: "A_FUTURE_REASON", SessionID: "s1"}, obs(0), ReasonInSync},
		{"leave-alone is reported as such", Worker{Desired: "", Reason: "LEAVE_ALONE", SessionID: "s1"}, obs(0), "LEAVE_ALONE"},
		{"paused is reported as such", Worker{Desired: Stopped, Reason: "PAUSED", SessionID: "s1"}, obs(0), "PAUSED"},
		{"a long-lived worker is a relaunch matter", Worker{Desired: Running, Reason: "LONG_LIVED", SessionID: "s1"}, obs(0), ReasonRelaunchUnproven},
	} {
		t.Run(r.name, func(t *testing.T) {
			r.w.WorkerID, r.w.Name, r.w.Setup = "w", "W", setup("w")
			e := Engine{Source: fakeSource{snap: Snapshot{HostID: "h1", SweepMode: SweepApply, Workers: []Worker{r.w}}}, Observer: fakeObserver{"w": r.obs}, Actuator: queueActuator()}
			res := run(t, e, Options{})
			if len(res.Skipped) != 1 || res.Skipped[0].Reason != r.reason || len(res.Planned) != 0 {
				t.Fatalf("planned %v skipped %v, want reason %s", lines(res.Planned), lines(res.Skipped), r.reason)
			}
		})
	}
}
