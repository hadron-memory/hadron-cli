package hostup

import (
	"strings"
	"testing"
)

// cli#2191: the planner's detail names a count that rests on terminals Orca
// does not identify, and says nothing extra when every terminal is known.
func TestUnidentifiedTerminalsAreNamedInTheDetail2191(t *testing.T) {
	stopped := Worker{WorkerID: "w", Name: "W", Desired: Stopped, Reason: "NO_OPEN_TASK", Setup: setup("w")}
	running := func(session string) Worker {
		return Worker{WorkerID: "w", Name: "W", Desired: Running, Reason: "LONG_LIVED", SessionID: session, Setup: setup("w")}
	}
	obs := func(terms, unknown int) Observation {
		return Observation{Registered: true, WorktreeExists: true, AgentTerminals: terms, UnidentifiedTerminals: unknown}
	}
	for _, r := range []struct {
		name   string
		w      Worker
		obs    Observation
		reason string
		detail string
	}{
		{"in sync, unidentified", running("s"), obs(1, 1), ReasonInSync, "its terminal has no agent identity in Orca and counts as present (a leftover shell reads the same)"},
		{"in sync, known", running("s"), obs(1, 0), ReasonInSync, ""},
		{"no session, unidentified", running(""), obs(1, 1), ReasonTerminalWithoutSession, "an agent terminal is open with no worker session; its terminal has no agent identity"},
		{"stopped, unidentified", stopped, obs(1, 1), ReasonStopPending, "an agent is running; the platform's worker.end stops it; its terminal has no agent identity"},
		{"agent beside a shell", running("s"), obs(2, 1), ReasonDuplicateAgents, "more than one agent terminal in its worktree; 1 of its 2 terminals has no agent identity"},
		{"two shells", running("s"), obs(2, 2), ReasonDuplicateAgents, "more than one agent terminal in its worktree; 2 of its 2 terminals have no agent identity"},
		{"two known agents", running("s"), obs(2, 0), ReasonDuplicateAgents, "more than one agent terminal in its worktree"},
	} {
		t.Run(r.name, func(t *testing.T) {
			e := Engine{Source: fakeSource{snap: Snapshot{HostID: "h1", SweepMode: SweepApply, Workers: []Worker{r.w}}}, Observer: fakeObserver{"w": r.obs}, Actuator: queueActuator()}
			res := run(t, e, Options{})
			if len(res.Skipped) != 1 || len(res.Planned) != 0 {
				t.Fatalf("planned %v, skipped %v", lines(res.Planned), lines(res.Skipped))
			}
			it := res.Skipped[0]
			if it.Reason != r.reason || !strings.HasPrefix(it.Detail, r.detail) || (r.detail == "" && it.Detail != "") || (!strings.Contains(r.detail, "identity") && strings.Contains(it.Detail, "identity")) {
				t.Fatalf("got %s %q, want %s %q…", it.Reason, it.Detail, r.reason, r.detail)
			}
		})
	}
}
