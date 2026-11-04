package delivery

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// hadron-cli#1629 (consumer half of hadron-server#2169, cor:agt:090:04):
// the host poller types stop notices. Holger's option A (2026-10-24): a
// halt's notice is an ordinary line into a verified-empty composer, never an
// interrupt key; one that cannot be typed is logged and flagged, never
// acknowledged, and stays due.

func stopReason(eventID, level, notice string) WakeReason {
	return WakeReason{Kind: WakeStop, Stop: &StopReason{EventID: eventID, SignalID: "sig-" + eventID, Level: level, Notice: notice}}
}

const (
	haltLine   = StopPrefix + " stop: HALT (save your work and write a hand-off now)"
	pauseLine  = StopPrefix + " stop: PAUSE (finish or stop at your next sub-task)"
	resumeLine = StopPrefix + " resume: HALT lifted"
)

func (h *harness) stopDropped() int {
	n := 0
	for _, e := range h.events {
		if e.Reason == StopDropped {
			n++
		}
	}
	return n
}

func TestAStopKeyIsItsNoticeEvent(t *testing.T) {
	if got := stopReason("ev7", "HALT", "STOP").Key(); got != "stop:ev7" {
		t.Fatalf("key %q", got)
	}
}

func TestAStopWithoutItsNoticeIsMalformed(t *testing.T) {
	for name, r := range map[string]WakeReason{
		"no stop":   {Kind: WakeStop},
		"no event":  {Kind: WakeStop, Stop: &StopReason{SignalID: "s", Level: "HALT", Notice: "STOP"}},
		"no level":  {Kind: WakeStop, Stop: &StopReason{EventID: "e", SignalID: "s", Notice: "STOP"}},
		"no notice": {Kind: WakeStop, Stop: &StopReason{EventID: "e", SignalID: "s", Level: "HALT"}},
	} {
		if err := CheckWake(Wake{Proof: "p", WorkerID: workerID, Reasons: []WakeReason{r}}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := CheckWake(Wake{Proof: "p", WorkerID: workerID, Reasons: []WakeReason{stopReason("e", "HALT", "STOP")}}); err != nil {
		t.Fatalf("a complete stop is refused: %v", err)
	}
}

func TestAHaltIsTypedOnceAsAnOrdinaryLineAndAcked(t *testing.T) {
	h := newHarness(t)
	w := h.wake(stopReason("ev1", "HALT", "STOP"))
	o := h.deliverWake(w)
	if o.Kind != Delivered || len(h.orca.sends) != 1 {
		t.Fatalf("outcome %+v sends %+v", o, h.orca.sends)
	}
	s := h.orca.sends[0]
	if s.text != haltLine {
		t.Fatalf("typed %q, want %q", s.text, haltLine)
	}
	// Option A: no interrupt key, ever.
	if s.opts != (orcaterm.SendOptions{}) {
		t.Fatalf("send options %+v: a halt is an ordinary line", s.opts)
	}
	if ws := h.wakeServer(); len(ws.acks) != 1 {
		t.Fatalf("acks %+v", ws.acks)
	}
	h.wantWakeRecord("stop:ev1", WakeAcked)
	// The same notice offered again (a lost ack, a re-read): never retyped.
	h.deliverWake(w)
	if len(h.orca.sends) != 1 {
		t.Fatalf("retyped: %+v", h.orca.sends)
	}
}

func TestTheInterruptFlagChangesNothingSent(t *testing.T) {
	h := newHarness(t)
	r := stopReason("ev2", "HALT", "STOP")
	r.Stop.Interrupt = true
	h.deliverWake(h.wake(r))
	if len(h.orca.sends) != 1 || h.orca.sends[0].opts.Interrupt {
		t.Fatalf("sends %+v", h.orca.sends)
	}
}

func TestAPauseAndAResumeAreTypedOnce(t *testing.T) {
	h := newHarness(t)
	h.deliverWake(h.wake(stopReason("ev3", "PAUSE", "STOP")))
	h.deliverWake(h.wake(stopReason("ev4", "HALT", "RESUME")))
	h.deliverWake(h.wake(stopReason("ev4", "HALT", "RESUME")))
	if len(h.orca.sends) != 2 || h.orca.sends[0].text != pauseLine || h.orca.sends[1].text != resumeLine {
		t.Fatalf("sends %+v", h.orca.sends)
	}
}

func TestTheNoticeIsTypedFirstAheadOfTicksAndTasks(t *testing.T) {
	h := newHarness(t)
	h.deliverWake(h.wake(tick("tk1", tickAt), stopReason("ev5", "PAUSE", "STOP"), task("t9", 0, "Fix it")))
	if len(h.orca.sends) != 1 || !strings.HasPrefix(h.orca.sends[0].text, pauseLine+"; ") {
		t.Fatalf("sends %+v", h.orca.sends)
	}
}

func TestAHaltOverADraftIsNeverTypedNorAckedAndIsFlaggedOnce(t *testing.T) {
	h := newHarness(t)
	h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: claudeIdle, Draft: "a human's unsent words"}}
	w := h.wake(stopReason("ev6", "HALT", "STOP"))
	for range 3 {
		o := h.deliverWake(w)
		if o.Kind != Held || o.Reason != HoldDraft {
			t.Fatalf("outcome %+v", o)
		}
	}
	h.wantNoSend()
	if len(h.wakeServer().acks) != 0 || len(h.wakeRecords()) != 0 {
		t.Fatalf("acks %+v records %+v", h.wakeServer().acks, h.wakeRecords())
	}
	if n := h.stopDropped(); n != 1 {
		t.Fatalf("stop-dropped events %d, want 1 across repeated polls", n)
	}
	// The draft clears: the same notice is typed then.
	h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeIdle)}
	h.orca.reads = map[string]int{}
	if o := h.deliverWake(w); o.Kind != Delivered || len(h.orca.sends) != 1 || h.orca.sends[0].text != haltLine {
		t.Fatalf("outcome %+v sends %+v", o, h.orca.sends)
	}
}

func TestAStopOnlyWakeSpanCountsTheStop(t *testing.T) {
	h := newHarness(t)
	if got := h.wake(stopReason("ev8", "HALT", "STOP")).Span(); !strings.Contains(got, "stop=1") {
		t.Fatalf("span %q", got)
	}
}
