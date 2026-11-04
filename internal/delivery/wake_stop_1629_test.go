package delivery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestOnlyAHaltIsFlaggedAsDropped(t *testing.T) {
	h := newHarness(t)
	h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: claudeIdle, Draft: "draft"}}
	h.deliverWake(h.wake(stopReason("ev10", "PAUSE", "STOP")))
	h.deliverWake(h.wake(stopReason("ev11", "HALT", "RESUME")))
	if n := h.stopDropped(); n != 0 {
		t.Fatalf("stop-dropped %d: only a halt's stop notice is an interrupt", n)
	}
}

func TestADeferredHaltIsNotADrop(t *testing.T) {
	h := newHarness(t)
	w := h.wake(stopReason("ev12", "HALT", "STOP"))
	w.DeliverAfter = h.now.Add(time.Minute)
	if o := h.deliverWake(w); o.Kind != Deferred {
		t.Fatalf("outcome %+v", o)
	}
	if n := h.stopDropped(); n != 0 {
		t.Fatalf("stop-dropped %d for a wake not yet due", n)
	}
	// A busy agent before the busy deadline: the terminal path defers it,
	// and that is a wait, not a drop.
	h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeBusy)}
	w.DeliverAfter = h.now.Add(-time.Minute)
	if o := h.deliverWake(w); o.Kind != Deferred || o.Reason != "busy" {
		t.Fatalf("busy outcome %+v", o)
	}
	if n := h.stopDropped(); n != 0 {
		t.Fatalf("stop-dropped %d for a busy agent", n)
	}
}

func TestADroppedHaltEventNamesItsNoticeByIDOnly(t *testing.T) {
	h := newHarness(t)
	h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: claudeIdle, Draft: "SECRET draft"}}
	h.deliverWake(h.wake(stopReason("ev13", "HALT", "STOP")))
	for _, e := range h.events {
		if e.Reason == StopDropped {
			if e.Span != "stop:HALT:ev13" || e.Kind != Held || !e.Wake || e.Worker != "Joshua" || strings.Contains(e.Span+e.Terminal, "SECRET") {
				t.Fatalf("event %+v", e)
			}
			return
		}
	}
	t.Fatal("no stop-dropped event")
}

func (h *harness) stopDrops() []StopDrop {
	h.t.Helper()
	recs, err := h.d.StopDrops.List()
	if err != nil {
		h.t.Fatal(err)
	}
	return recs
}

func TestADroppedHaltIsRecordedOnceAcrossAPollerRestart(t *testing.T) {
	h := newHarness(t)
	h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: claudeIdle, Draft: "SECRET draft"}}
	w := h.wake(stopReason("ev20", "HALT", "STOP"))
	h.deliverWake(w)
	// A restarted poller: a new Deliverer over the same state directory.
	events := h.events
	h.d = New(h.server, h.orca, h.state)
	h.d.Now = func() time.Time { return h.now }
	h.d.Bound = func(w string) string { return h.bound[w] }
	h.d.Emit = func(e Event) { h.events = append(h.events, e) }
	h.deliverWake(w)
	h.deliverWake(w)
	if n := h.stopDropped(); n != 1 {
		t.Fatalf("stop-dropped %d across a restart (before restart: %d events)", n, len(events))
	}
	recs := h.stopDrops()
	if len(recs) != 1 {
		t.Fatalf("records %+v", recs)
	}
	got := recs[0]
	if got.EventID != "ev20" || got.SignalID != "sig-ev20" || got.WorkerID != workerID || got.Worker != "Joshua" ||
		got.Level != "HALT" || got.HoldReason != HoldDraft || !got.HeldAt.Equal(h.now) || !got.TypedAt.IsZero() {
		t.Fatalf("record %+v", got)
	}
	entries, err := os.ReadDir(h.d.StopDrops.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(h.d.StopDrops.Dir, e.Name()))
		if strings.Contains(string(data), "SECRET") || strings.Contains(string(data), "wake-proof") {
			t.Fatalf("record %s carries text: %s", e.Name(), data)
		}
		if info, _ := e.Info(); info.Mode().Perm() != 0o600 {
			t.Fatalf("record mode %v", info.Mode().Perm())
		}
	}
}

func TestADroppedHaltTypedLaterIsStampedAndStaysOneRecord(t *testing.T) {
	h := newHarness(t)
	h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: claudeIdle, Draft: "draft"}}
	w := h.wake(stopReason("ev21", "HALT", "STOP"))
	h.deliverWake(w)
	h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeIdle)}
	h.orca.reads = map[string]int{}
	h.now = h.now.Add(time.Minute)
	if o := h.deliverWake(w); o.Kind != Delivered {
		t.Fatalf("outcome %+v", o)
	}
	recs := h.stopDrops()
	if len(recs) != 1 || !recs[0].TypedAt.Equal(h.now) {
		t.Fatalf("records %+v", recs)
	}
}

func TestEveryHoldOfAHaltIsADrop(t *testing.T) {
	cases := map[string]struct {
		screen []string
		draft  string
		reason string
	}{
		"draft":    {screen: claudeIdle, draft: "words", reason: HoldDraft},
		"question": {screen: []string{"Do you want to proceed?", "❯ 1. Yes", "  2. No"}, reason: HoldQuestion},
		"unknown":  {screen: []string{"nothing recognizable"}, reason: HoldScreenUnknown},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: c.screen, Draft: c.draft}}
			h.deliverWake(h.wake(stopReason("ev-"+name, "HALT", "STOP")))
			if recs := h.stopDrops(); len(recs) != 1 || recs[0].HoldReason != c.reason {
				t.Fatalf("records %+v", recs)
			}
		})
	}
	t.Run("not submitted", func(t *testing.T) {
		h := newHarness(t)
		h.orca.receipts = []orcaterm.Receipt{{Accepted: true, TurnStarted: false, Stages: []string{"input_accepted"}}}
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeIdle), screen(claudeTyped(haltLine))}
		h.deliverWake(h.wake(stopReason("ev-ns", "HALT", "STOP")))
		if recs := h.stopDrops(); len(recs) != 1 || recs[0].HoldReason != HoldNotSubmitted {
			t.Fatalf("records %+v events %+v", recs, h.events)
		}
	})
	t.Run("no terminal", func(t *testing.T) {
		h := newHarness(t)
		h.orca.terms = nil
		h.deliverWake(h.wake(stopReason("ev-nt", "HALT", "STOP")))
		if recs := h.stopDrops(); len(recs) != 1 || recs[0].HoldReason != "no-terminal" {
			t.Fatalf("records %+v", recs)
		}
	})
}

func TestAnUnwritableOutboxIsLoggedEveryPoll(t *testing.T) {
	h := newHarness(t)
	h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: claudeIdle, Draft: "draft"}}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.d.StopDrops = &StopOutbox{Dir: filepath.Join(blocker, "under-a-file")}
	w := h.wake(stopReason("ev22", "HALT", "STOP"))
	h.deliverWake(w)
	h.deliverWake(w)
	n := 0
	for _, e := range h.events {
		if e.Reason == StopDropUnrecorded {
			n++
		}
	}
	if n != 2 || h.stopDropped() != 0 {
		t.Fatalf("unrecorded %d, dropped %d", n, h.stopDropped())
	}
}

// Second review P2 (b38108c9): a wake refused as malformed is never typed,
// so a well-formed HALT riding in it is flagged, not left silent.
func TestAHaltInAMalformedWakeIsFlagged(t *testing.T) {
	h := newHarness(t)
	bad := WakeReason{Kind: WakeTick} // a tick without its ticker
	o := h.deliverWake(h.wake(stopReason("ev30", "HALT", "STOP"), bad))
	if o.Kind != Failed || o.Reason != "malformed-wake" {
		t.Fatalf("outcome %+v", o)
	}
	h.wantNoSend()
	if drops := h.stopDrops(); len(drops) != 1 || drops[0].EventID != "ev30" || drops[0].HoldReason != "malformed-wake" {
		t.Fatalf("drops %+v", drops)
	}
}

// Second review P2 (b38108c9): with the typed records unreadable nothing is
// typed, and a HALT is flagged.
func TestAHaltWithUnreadableRecordsIsFlagged(t *testing.T) {
	h := newHarness(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.d.WakeRecords = &WakeRecords{Dir: filepath.Join(blocker, "under-a-file")}
	o := h.deliverWake(h.wake(stopReason("ev31", "HALT", "STOP")))
	if o.Kind != Failed || o.Reason != "records-unreadable" {
		t.Fatalf("outcome %+v", o)
	}
	h.wantNoSend()
	if drops := h.stopDrops(); len(drops) != 1 || drops[0].EventID != "ev31" || drops[0].HoldReason != "records-unreadable" {
		t.Fatalf("drops %+v", drops)
	}
}

// A stop with no event id cannot be named in the outbox; it is not recorded
// under an empty key (the malformed-wake failure itself is still logged).
func TestAStopWithoutAnEventIDIsNotRecorded(t *testing.T) {
	h := newHarness(t)
	h.deliverWake(h.wake(stopReason("", "HALT", "STOP")))
	if drops := h.stopDrops(); len(drops) != 0 {
		t.Fatalf("drops %+v", drops)
	}
}

// Second review P3 (b38108c9): the poller and report-stops take the outbox
// from one helper, so they cannot drift apart.
func TestThePollersOutboxIsStopOutboxDir(t *testing.T) {
	dir := t.TempDir()
	if got := New(nil, nil, dir).StopDrops.Dir; got != StopOutboxDir(dir) {
		t.Fatalf("poller outbox %q, helper %q", got, StopOutboxDir(dir))
	}
}
