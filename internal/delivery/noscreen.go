package delivery

import (
	"sync"
	"time"
)

// cli#2678 (slice c of #2675): the no-screen fallback SURFACES a worker whose
// terminal gives no screen; it never types into it. A wake held as screen-unknown
// is held for a reason that is not a mistake: a dialog leaves no trace in the
// transcript and Orca's tui-idle is satisfied by one, so no retry and no proof
// short of a rendered screen makes typing safe (ruled: Peter #122249, Hugo
// #122255). The tracker below only WATCHES the events delivery already emits; it
// holds no Orca handle, so it cannot type, resize, kill or relaunch.

const (
	// NoScreenAfter is how long a worker's wakes must be held as screen-unknown,
	// continuously, before one event is recorded for the episode.
	NoScreenAfter = 15 * time.Minute
	// NoScreenGap is how long without a screen-unknown hold ends an episode on
	// its own: the wake is no longer pending, so nothing is left to surface.
	NoScreenGap = 5 * time.Minute
)

// NoScreenEpisode is one worker's run of screen-unknown holds, as recorded when
// it crosses NoScreenAfter. It is codes, ids and counts only: never screen text.
type NoScreenEpisode struct {
	Worker, WorkerID string
	// Terminal is Orca's handle for the held terminal, so a person can open that
	// exact terminal. Empty when no hold named one.
	Terminal string
	// HeldSince is when the episode's first hold happened.
	HeldSince time.Time
	// Pending is how many distinct wakes or batches were held in the episode.
	Pending int
}

// NoScreenTracker turns delivery events into at most one NoScreenEpisode per
// worker per episode. An episode ends when a hold of another kind, a delivery, a
// reconcile or a deferral shows the worker's screen was read, or when no
// screen-unknown hold arrives for NoScreenGap. Fire returning an error leaves the
// episode unrecorded, so the next hold tries again; a nil return records it for
// good. Safe for concurrent Observe calls.
type NoScreenTracker struct {
	Fire func(NoScreenEpisode) error

	mu  sync.Mutex
	eps map[string]*noScreenState
}

type noScreenState struct {
	NoScreenEpisode
	lastHeld time.Time
	spans    map[string]bool
	fired    bool
}

// Observe feeds one delivery event.
func (t *NoScreenTracker) Observe(e Event) {
	if e.WorkerID == "" {
		return
	}
	t.mu.Lock()
	if t.eps == nil {
		t.eps = map[string]*noScreenState{}
	}
	if e.Kind != Held || e.Reason != HoldScreenUnknown {
		switch e.Kind {
		case Delivered, Reconciled, Deferred, Held:
			delete(t.eps, e.WorkerID) // a screen was read: today's rules apply
		}
		t.mu.Unlock()
		return
	}
	s := t.eps[e.WorkerID]
	if s != nil && e.Time.Sub(s.lastHeld) > NoScreenGap {
		s = nil // the wake that started it is gone
	}
	if s == nil {
		s = &noScreenState{spans: map[string]bool{}}
		s.Worker, s.WorkerID, s.HeldSince = e.Worker, e.WorkerID, e.Time
		t.eps[e.WorkerID] = s
	}
	s.lastHeld = e.Time
	if e.Terminal != "" {
		s.Terminal = e.Terminal
	}
	s.spans[e.Span] = true
	s.Pending = len(s.spans)
	if s.fired || e.Time.Sub(s.HeldSince) < NoScreenAfter || t.Fire == nil {
		t.mu.Unlock()
		return
	}
	ep := s.NoScreenEpisode
	t.mu.Unlock()
	if err := t.Fire(ep); err != nil {
		return // not recorded: the next hold tries again
	}
	t.mu.Lock()
	if cur := t.eps[e.WorkerID]; cur == s {
		s.fired = true
	}
	t.mu.Unlock()
}
