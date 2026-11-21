package delivery

import (
	"errors"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"testing"
	"time"
)

// cli#2678 (slice c of #2675): a worker whose terminal gives no screen is held
// every cycle as screen-unknown. After NoScreenAfter of those holds with a wake
// pending, the tracker fires ONCE per episode so a person can be told. It never
// types: it only watches the events delivery already emits.

func held(at time.Time, worker, span string) Event {
	return Event{Time: at, Worker: worker, WorkerID: "id-" + worker, Span: span, Kind: Held, Reason: HoldScreenUnknown, Terminal: "term_1", Wake: true}
}

// cycles feeds one screen-unknown hold per minute for worker w, minutes from..to after t0.
func cycles(tr *NoScreenTracker, t0 time.Time, from, to int, spans ...string) {
	for m := from; m <= to; m++ {
		tr.Observe(held(t0.Add(time.Duration(m)*time.Minute), "w", spans[m%len(spans)]))
	}
}

func newTracker(fires *[]NoScreenEpisode) *NoScreenTracker {
	return &NoScreenTracker{Fire: func(e NoScreenEpisode) error { *fires = append(*fires, e); return nil }}
}

func TestNoScreenFiresOncePerEpisodeAfterTheThreshold2678(t *testing.T) {
	var fires []NoScreenEpisode
	tr := newTracker(&fires)
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	for m := 0; m < 15; m++ { // 0..14 minutes: under the threshold
		tr.Observe(held(t0.Add(time.Duration(m)*time.Minute), "wolfgang", "ticks:1"))
	}
	if len(fires) != 0 {
		t.Fatalf("fired before %v: %d", NoScreenAfter, len(fires))
	}
	for m := 15; m < 66; m++ { // the whole incident: 66 minutes of holds
		tr.Observe(held(t0.Add(time.Duration(m)*time.Minute), "wolfgang", "ticks:1"))
	}
	if len(fires) != 1 {
		t.Fatalf("want exactly one event for the episode, got %d", len(fires))
	}
	got := fires[0]
	if got.WorkerID != "id-wolfgang" || got.Terminal != "term_1" || !got.HeldSince.Equal(t0) || got.Pending != 1 {
		t.Fatalf("episode = %+v, want worker id-wolfgang, terminal term_1, held since %v, 1 pending", got, t0)
	}
}

func TestNoScreenCountsDistinctPendingWakes2678(t *testing.T) {
	var fires []NoScreenEpisode
	tr := newTracker(&fires)
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	cycles(tr, t0, 0, 15, "ticks:1", "tasks:2") // two different wakes, alternating
	if len(fires) != 1 || fires[0].Pending != 2 {
		t.Fatalf("fires = %+v, want one with 2 pending", fires)
	}
}

func TestNoScreenScreenComingBackEndsTheEpisode2678(t *testing.T) {
	for _, back := range []Event{
		{Kind: Delivered, WorkerID: "id-w"},
		{Kind: Reconciled, WorkerID: "id-w"},
		{Kind: Deferred, WorkerID: "id-w"},
		{Kind: Held, Reason: HoldDraft, WorkerID: "id-w"}, // a screen was read: today's rules apply
	} {
		var fires []NoScreenEpisode
		tr := newTracker(&fires)
		t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
		cycles(tr, t0, 0, 10, "ticks:1")
		back.Time = t0.Add(10 * time.Minute)
		tr.Observe(back)
		// A new screen-unknown run starts a new clock: the old 10 minutes do not count.
		cycles(tr, t0, 11, 11+14, "ticks:1")
		if len(fires) != 0 {
			t.Fatalf("%s %s: the episode did not end, fired %d", back.Kind, back.Reason, len(fires))
		}
		cycles(tr, t0, 11+15, 11+15, "ticks:1")
		if len(fires) != 1 {
			t.Fatalf("%s: the new episode never fired", back.Kind)
		}
	}
}

func TestNoScreenWorkersAreSeparateAndAGapEndsAnEpisode2678(t *testing.T) {
	var fires []NoScreenEpisode
	tr := newTracker(&fires)
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	tr.Observe(held(t0, "a", "ticks:1"))
	tr.Observe(held(t0.Add(NoScreenAfter), "b", "ticks:1")) // b's clock starts here, not a's
	if len(fires) != 0 {
		t.Fatalf("one worker's hold started another's clock: %+v", fires)
	}
	// a was silent for longer than the gap: its wake is gone, so no stale episode fires.
	tr.Observe(held(t0.Add(NoScreenAfter+2*NoScreenGap), "a", "ticks:1"))
	if len(fires) != 0 {
		t.Fatalf("a stale episode fired after a silent gap: %+v", fires)
	}
}

func TestNoScreenFailedReportRetriesNextHoldThenStops2678(t *testing.T) {
	calls := 0
	tr := &NoScreenTracker{Fire: func(NoScreenEpisode) error {
		calls++
		if calls == 1 {
			return errors.New("server down")
		}
		return nil
	}}
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	cycles(tr, t0, 0, 20, "ticks:1") // fails at 15, retried at 16, then done
	if calls != 2 {
		t.Fatalf("report attempts = %d, want 2 (one failure, one success, then done)", calls)
	}
}

func TestNoScreenIgnoresEventsWithoutAWorker2678(t *testing.T) {
	var fires []NoScreenEpisode
	tr := newTracker(&fires)
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	tr.Observe(Event{Time: t0, Kind: Held, Reason: HoldScreenUnknown})
	tr.Observe(Event{Time: t0.Add(time.Hour), Kind: Held, Reason: HoldScreenUnknown})
	if len(fires) != 0 {
		t.Fatalf("an event with no worker id fired: %+v", fires)
	}
}

// The incident, end to end through the real Deliverer: Orca gives no screen
// (screen-unavailable), a wake stays pending for 66 one-minute cycles. Nothing
// is ever typed (the guard this slice must not weaken), every cycle holds as
// screen-unknown, and the tracker fed from the same Emit seam fires exactly once.
func TestNoScreenIncidentNeverTypesAndFiresOnce2678(t *testing.T) {
	h := newHarness(t)
	var fires []NoScreenEpisode
	tr := &NoScreenTracker{Fire: func(e NoScreenEpisode) error { fires = append(fires, e); return nil }}
	h.d.Emit = func(e Event) { h.events = append(h.events, e); tr.Observe(e) }
	h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: orcaterm.SourceScreenUnavailable}}
	for m := 0; m < 66; m++ {
		o := h.deliverWake(h.wake(tick("tk1", tickAt)))
		if o.Kind != Held || o.Reason != HoldScreenUnknown {
			t.Fatalf("cycle %d: outcome %+v, want held screen-unknown", m, o)
		}
		h.wantNoSend()
		h.now = h.now.Add(time.Minute)
	}
	if len(fires) != 1 {
		t.Fatalf("episode events = %d, want exactly 1", len(fires))
	}
	if fires[0].Terminal != "term_j" || fires[0].WorkerID != workerID || fires[0].Pending < 1 {
		t.Fatalf("episode = %+v", fires[0])
	}
	// The screen comes back: the held wake is delivered under today's rules.
	h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeIdle)}
	if o := h.deliverWake(h.wake(tick("tk1", tickAt))); o.Kind != Delivered {
		t.Fatalf("after the screen returned: %+v", o)
	}
}
