package hostpoller

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"
)

// hadron-cli#2142: a group held past drainHeldAfter is announced ONCE with a
// code, the end of the hold is announced once, and the watcher never reaps or
// signals on account of the announcement. The loop is driven with a fake clock,
// a fake tick source and a fake membership oracle: no process, no sleep.

type drainRig struct {
	events  []Event
	reaps   int
	members func(call int) (bool, drainWhy) // call counts membership answers
	calls   int
	clock   time.Time
	step    time.Duration
	reapOK  bool // the reap is ours (a stop did not get there first)
	// afterCall runs inside the oracle, after the clock moved (a stop reaping the group).
	afterCall func(call int)
}

func (r *drainRig) run(t *testing.T, a *execAgent, ticks int) {
	t.Helper()
	a.onDrain = func(e Event) { r.events = append(r.events, e) }
	ch := make(chan time.Time, ticks)
	for i := 0; i < ticks; i++ {
		ch <- time.Time{}
	}
	close(ch)
	// The clock advances once per TICK (when the oracle is asked), so extra clock
	// reads inside a tick never change the simulated elapsed time.
	a.drainLoop(ch,
		func(int, int) (bool, drainWhy) {
			r.calls++
			r.clock = r.clock.Add(r.step)
			if r.afterCall != nil {
				r.afterCall(r.calls)
			}
			return r.members(r.calls)
		},
		func() time.Time { return r.clock },
		func() bool { r.reaps++; return r.reapOK })
}

func newDrainRig(members func(call int) (bool, drainWhy)) *drainRig {
	return &drainRig{members: members, clock: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), step: time.Minute, reapOK: true}
}

func kinds(es []Event) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Kind+"/"+e.Outcome)
	}
	return out
}

func TestAHoldPastTheThresholdIsAnnouncedExactlyOnceAndNeverReaped2142(t *testing.T) {
	r := newDrainRig(func(int) (bool, drainWhy) { return true, whyUnseenPid })
	a := &execAgent{pid: 4242}
	r.run(t, a, 60) // 60 ticks, one simulated minute each: an hour, uncertain throughout
	got := kinds(r.events)
	if len(got) != 1 || got[0] != "drain-held/unseen-pid" {
		t.Fatalf("events = %v, want exactly one drain-held with the code", got)
	}
	if r.reaps != 0 {
		t.Fatalf("an announcement must never reap: %d reaps", r.reaps)
	}
	e := r.events[0]
	if !regexp.MustCompile(`^pgid=4242 held=\d+m\d+s$|^pgid=4242 held=\d+h\d+m\d+s$`).MatchString(e.Detail) {
		t.Fatalf("detail must be codes and numbers only: %q", e.Detail)
	}
	// The loop's one clock stamps both: the 10th tick is 10 simulated minutes in.
	if want := time.Date(2026, 11, 15, 12, 10, 0, 0, time.UTC); !e.Time.Equal(want) || e.Detail != "pgid=4242 held=10m0s" {
		t.Fatalf("event time %v detail %q: the timestamp and the held duration must come from the same reading (want %v, 10m0s)", e.Time, e.Detail, want)
	}
}

func TestNothingIsAnnouncedBeforeTheThreshold2142(t *testing.T) {
	r := newDrainRig(func(int) (bool, drainWhy) { return true, whyLateFork })
	r.run(t, &execAgent{pid: 1}, 9) // nine minutes of an uncertain hold: under the 10 minute threshold
	if len(r.events) != 0 {
		t.Fatalf("events before the threshold: %v", kinds(r.events))
	}
}

func TestAHoldThatDrainsBeforeTheThresholdSaysNothingAtAll2142(t *testing.T) {
	r := newDrainRig(func(call int) (bool, drainWhy) { return call < 4, whyLiveMember })
	r.run(t, &execAgent{pid: 1}, 20)
	if len(r.events) != 0 || r.reaps != 1 {
		t.Fatalf("events=%v reaps=%d: a short hold is silent and still reaps once", kinds(r.events), r.reaps)
	}
}

func TestAnAnnouncedHoldThatDrainsEmitsOneClosingEventAndNoSecondHeld2142(t *testing.T) {
	r := newDrainRig(func(call int) (bool, drainWhy) { return call < 30, whyVanishedMember })
	r.run(t, &execAgent{pid: 7}, 100)
	got := kinds(r.events)
	if len(got) != 2 || got[0] != "drain-held/vanished-member" || got[1] != "drain-ended/drained" {
		t.Fatalf("events = %v, want held then ended(drained)", got)
	}
	if r.reaps != 1 {
		t.Fatalf("the drained group reaps once: %d", r.reaps)
	}
}

func TestAnAnnouncedHoldThatAStopReleasedClosesAsReleased2142(t *testing.T) {
	r := newDrainRig(func(call int) (bool, drainWhy) { return call < 30, whyLiveMember })
	r.reapOK = false // a stop reaped first
	r.run(t, &execAgent{pid: 7}, 100)
	got := kinds(r.events)
	if len(got) != 2 || got[1] != "drain-ended/released" {
		t.Fatalf("events = %v, want held then ended(released)", got)
	}
}

func TestTheCodeIsTheLatestReasonNotTheFirst2142(t *testing.T) {
	r := newDrainRig(func(call int) (bool, drainWhy) {
		if call < 5 {
			return true, whyLateFork
		}
		return true, whyUnseenPid
	})
	r.run(t, &execAgent{pid: 7}, 60)
	if got := kinds(r.events); len(got) != 1 || got[0] != "drain-held/unseen-pid" {
		t.Fatalf("events = %v: the announcement carries the reason AT the moment it fires", got)
	}
}

func TestADrainOnTheProductionWatcherWithoutASinkIsSilent2142(t *testing.T) {
	r := newDrainRig(func(int) (bool, drainWhy) { return true, whyUnseenPid })
	a := &execAgent{pid: 1} // no onDrain
	ch := make(chan time.Time, 50)
	for i := 0; i < 50; i++ {
		ch <- time.Time{}
	}
	close(ch)
	a.drainLoop(ch, func(int, int) (bool, drainWhy) { return true, whyUnseenPid }, func() time.Time { r.clock = r.clock.Add(time.Minute); return r.clock }, func() bool { return true })
}

// The sink is the host's: ExecHost.OnDrain must reach the agent it launches.
func TestExecHostGivesItsDrainSinkToTheAgentsItLaunches2142(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	called := false
	h := ExecHost{OnDrain: func(Event) { called = true }}
	a, err := h.Launch(context.Background(), LaunchSpec{Argv: []string{sh, "-c", "true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not exit")
	}
	ea, ok := a.(*execAgent)
	if !ok || ea.onDrain == nil {
		t.Fatal("the launched agent must carry the host's drain sink")
	}
	ea.onDrain(Event{})
	if !called {
		t.Fatal("the agent's sink must be the host's")
	}
	<-ea.watcherDone
}

// The other release path: the agent is ALREADY reaped when a tick arrives (a stop
// got there between ticks), so the loop leaves through its early return, not
// through the reap. It must still close an announced hold, exactly once, as
// "released", and never call the reap.
func TestAnAnnouncedHoldReapedBetweenTicksClosesAsReleasedWithoutReaping2142(t *testing.T) {
	a := &execAgent{pid: 7}
	r := newDrainRig(func(int) (bool, drainWhy) { return true, whyLiveMember })
	r.afterCall = func(call int) {
		if call == 12 { // after the hold was announced (tick 10), a stop reaps it
			a.reaped.Store(true)
		}
	}
	r.run(t, a, 60)
	got := kinds(r.events)
	if len(got) != 2 || got[0] != "drain-held/live-member" || got[1] != "drain-ended/released" {
		t.Fatalf("events = %v, want held then ended(released) exactly once", got)
	}
	if r.reaps != 0 {
		t.Fatalf("the early return must not reap: %d", r.reaps)
	}
	// Reaped before ever being announced: silent.
	b := &execAgent{pid: 8}
	r2 := newDrainRig(func(int) (bool, drainWhy) { return true, whyLiveMember })
	r2.afterCall = func(call int) {
		if call == 3 {
			b.reaped.Store(true)
		}
	}
	r2.run(t, b, 60)
	if len(r2.events) != 0 {
		t.Fatalf("a hold reaped before the threshold is silent: %v", kinds(r2.events))
	}
}
