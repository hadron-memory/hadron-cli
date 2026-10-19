package delivery

// Tick and task delivery (cli#906 row 1): a due wake's TICK and TASK
// reasons, typed as one own-prefixed line through the same terminal path as
// a batch. The server decides what is due and when (dueWakes, S1 slice (c)
// timing); this decides only what the terminal shows and when C1 may hand
// the wake's proof back. A MENTION reason is never typed here and never
// acknowledged as delivery: its text arrives as an S1 batch.
//
// Gated at hadron-cli#906 issuecomment-6257655782 (R53/A′, R63). The
// GraphQL adapter arrives with S1's schema; nothing here depends on it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Own prefixes of a typed wake line. With Prefix they are what a deliverer
// recognizes as its own leftover in a composer.
const (
	TickPrefix = "[hadron tick]"
	TaskPrefix = "[hadron task]"
)

// OwnPrefixes are every prefix C1 types: a leftover opening with any of them
// is ours (submitted or cleared), never a foreign draft.
var OwnPrefixes = []string{Prefix, TickPrefix, TaskPrefix}

// MaxWakeRunes bounds a wake line. A longer one is cut at a reason
// boundary and says how many reasons it left out.
const MaxWakeRunes = 1000

// WakeKind is a wake reason's kind, as dueWakes names it.
type WakeKind string

const (
	WakeMention WakeKind = "MENTION"
	WakeTick    WakeKind = "TICK"
	WakeTask    WakeKind = "TASK"
)

// TaskReason is the task a TASK reason is for (DueTaskReason).
type TaskReason struct {
	EventID    string
	ChainIndex int
	// Event is the line's wording: "assigned", "child finished", "alarm" or
	// "end session".
	Event string
	ID    string
	Title string
}

// WakeReason is one reason a wake is due.
type WakeReason struct {
	Kind       WakeKind
	TickerID   string
	TickerName string
	// LastTickAt names the tick (k) a TICK reason is for: it changes per
	// tick, so with the ticker it is the reason's stable identity.
	LastTickAt time.Time
	Task       *TaskReason
}

// Key is the reason's stable submitted identity: tick:<tickerId>:<lastTickAt>
// or task:<eventId>:<chainIndex>. "" for a MENTION, which C1 never records.
func (r WakeReason) Key() string {
	switch {
	case r.Kind == WakeTick:
		return "tick:" + r.TickerID + ":" + r.LastTickAt.UTC().Format(time.RFC3339Nano)
	case r.Kind == WakeTask && r.Task != nil:
		return fmt.Sprintf("task:%s:%d", r.Task.EventID, r.Task.ChainIndex)
	}
	return ""
}

// Wake is one due wake as dueWakes returns it.
type Wake struct {
	// Proof is the signed proof handed back to acknowledgeWakes. Never
	// logged, stored or put in argv.
	Proof    string
	WorkerID string
	Reasons  []WakeReason
	// NamesChannels: the proof carries channel ranges (DueWake.channels is
	// non-empty). Before A′ is deployed, acknowledging such a proof could
	// mark unseen text delivered, so C1 never does.
	NamesChannels bool
	// DeliverAfter and BusyDeadline are S1 slice (c)'s timing (R63): the
	// busy deadline counts from the oldest waiting reason's due time. Zero
	// means the server did not say, and a busy agent holds visibly as
	// timing-unavailable rather than on a client-invented deadline.
	DeliverAfter time.Time
	BusyDeadline time.Time
}

// Span is the wake for logs: reason counts only, never keys, names or
// titles.
func (w Wake) Span() string {
	n := map[WakeKind]int{}
	for _, r := range w.Reasons {
		n[r.Kind]++
	}
	return fmt.Sprintf("wake:tick=%d,task=%d,mention=%d", n[WakeTick], n[WakeTask], n[WakeMention])
}

// components are w's TICK and TASK reasons: what C1 types.
func (w Wake) components() []WakeReason {
	out := []WakeReason{}
	for _, r := range w.Reasons {
		if r.Kind == WakeTick || r.Kind == WakeTask {
			out = append(out, r)
		}
	}
	return out
}

func (w Wake) hasMention() bool {
	for _, r := range w.Reasons {
		if r.Kind == WakeMention {
			return true
		}
	}
	return false
}

// ErrMalformedWake is a wake that breaks dueWakes' contract: neither typed
// nor acknowledged, logged by span and left due.
var ErrMalformedWake = errors.New("malformed wake")

// CheckWake is the shape a wake must have before anything is typed: a
// proof, a worker, and reasons whose keys are complete.
func CheckWake(w Wake) error {
	if w.Proof == "" || w.WorkerID == "" || len(w.Reasons) == 0 {
		return fmt.Errorf("%w: no proof, worker or reasons", ErrMalformedWake)
	}
	for _, r := range w.Reasons {
		switch r.Kind {
		case WakeMention:
		case WakeTick:
			if r.TickerID == "" || r.LastTickAt.IsZero() {
				return fmt.Errorf("%w: a tick without its ticker or tick", ErrMalformedWake)
			}
		case WakeTask:
			if r.Task == nil || r.Task.EventID == "" || r.Task.ChainIndex < 0 {
				return fmt.Errorf("%w: a task without its event", ErrMalformedWake)
			}
		default:
			return fmt.Errorf("%w: an unknown reason kind", ErrMalformedWake)
		}
	}
	return nil
}

// RenderWake is the one line typed for reasons (TICK and TASK only).
// Server-free and count-free, so it holds for a tick with no channels and
// for every-tick: reasons joined by "; ", cut at a reason boundary to
// MaxWakeRunes with the count of reasons left out.
func RenderWake(reasons []WakeReason) string {
	parts := make([]string, 0, len(reasons))
	for _, r := range reasons {
		switch r.Kind {
		case WakeTick:
			parts = append(parts, TickPrefix+" "+oneLine(r.TickerName)+": scheduled check-in. Read your team chat and tasks.")
		case WakeTask:
			parts = append(parts, fmt.Sprintf("%s %s: %s (%s)", TaskPrefix, oneLine(r.Task.Event), oneLine(r.Task.Title), oneLine(r.Task.ID)))
		}
	}
	text := strings.Join(parts, "; ")
	for kept := len(parts) - 1; utf8.RuneCountInString(text) > MaxWakeRunes && kept >= 1; kept-- {
		text = strings.Join(parts[:kept], "; ") + fmt.Sprintf("; (+%d more reasons)", len(parts)-kept)
	}
	if utf8.RuneCountInString(text) > MaxWakeRunes {
		text = string([]rune(text)[:MaxWakeRunes-1]) + "…"
	}
	return text
}

// oneLine folds control characters (a newline would submit early) and runs
// of space into one space.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }), " ")
}

// WakeServer is acknowledgeWakes, for one proof C1 itself read. An error
// of any kind means nothing moved.
type WakeServer interface {
	AckWake(ctx context.Context, proof string) (AckResult, error)
}

// Hold reasons local to a wake. A wake hold is logged and shown in status,
// never reported to S5 (S5's hold is a batch event).
const (
	// HoldTimingUnavailable: the agent is busy or unproven and the server
	// gave no busy deadline.
	HoldTimingUnavailable = "timing-unavailable"
)

// DeliverWake types w's not-yet-recorded TICK and TASK reasons into r's
// terminal as one line, records them, and acknowledges the proof when the
// accepted set allows it.
//
// The order mirrors a batch's: reconcile records first (a reason already
// typed is never typed again), then the shared terminal path, then record
// every typed key before the ack. A proof is acknowledged only when ackable.
func (d *Deliverer) DeliverWake(ctx context.Context, w Wake, r Recipient) Outcome {
	var out Outcome
	err := WithWorkerLock(d.LockDir, r.WorkerID, func(stillOwner func() bool) error {
		out = d.deliverWakeLocked(ctx, w, r, stillOwner)
		return nil
	})
	if errors.Is(err, ErrBusy) {
		return d.emitWake(w, r, Outcome{Kind: Skipped, Reason: "locked"})
	}
	if err != nil {
		return d.emitWake(w, r, Outcome{Kind: Failed, Reason: "lock"})
	}
	return out
}

func (d *Deliverer) emitWake(w Wake, r Recipient, o Outcome) Outcome {
	if d.Emit != nil {
		d.Emit(Event{Time: d.Now().UTC(), Worker: r.Name, WorkerID: r.WorkerID, Span: w.Span(), Kind: o.Kind, Reason: o.Reason, Terminal: o.Terminal, ResolvedBy: r.resolvedBy, Wake: true})
	}
	return o
}

// ackable: the accepted set (6257655782). Every TICK and TASK reason is
// recorded, and either the proof names no channels and carries no MENTION,
// or A′ is deployed (AckMixed) and acknowledging moves no message mark.
func (d *Deliverer) ackable(w Wake, recs map[string]WakeRecord) bool {
	for _, c := range w.components() {
		if _, ok := recs[c.Key()]; !ok {
			return false
		}
	}
	return d.AckMixed || (!w.NamesChannels && !w.hasMention())
}

func (d *Deliverer) deliverWakeLocked(ctx context.Context, w Wake, r Recipient, stillOwner func() bool) Outcome {
	if w.WorkerID != r.WorkerID {
		return d.emitWake(w, r, Outcome{Kind: Failed, Reason: "foreign-worker"})
	}
	if err := CheckWake(w); err != nil {
		return d.emitWake(w, r, Outcome{Kind: Failed, Reason: "malformed-wake"})
	}
	comps := w.components()
	if len(comps) == 0 {
		// Its text arrives as an S1 batch; C1 never types or acks it.
		return d.emitWake(w, r, Outcome{Kind: Skipped, Reason: "mention-only"})
	}
	recs, err := d.WakeRecords.List(r.WorkerID)
	if err != nil {
		return d.emitWake(w, r, Outcome{Kind: Failed, Reason: "records-unreadable"})
	}
	untyped := []WakeReason{}
	for _, c := range comps {
		if _, ok := recs[c.Key()]; !ok {
			untyped = append(untyped, c)
		}
	}
	if len(untyped) == 0 {
		// Everything was typed before: ack only, never retype.
		if !d.ackable(w, recs) {
			return d.emitWake(w, r, Outcome{Kind: Deferred, Reason: "ack-held"})
		}
		if !d.ackWake(ctx, w) {
			return d.emitWake(w, r, Outcome{Kind: Failed, Reason: "reconcile-ack-failed"})
		}
		return d.emitWake(w, r, Outcome{Kind: Reconciled})
	}
	if d.Now().Before(w.DeliverAfter) {
		return d.emitWake(w, r, Outcome{Kind: Deferred, Reason: "not-yet-due"})
	}
	if ctx.Err() != nil {
		return d.emitWake(w, r, Outcome{Kind: Deferred, Reason: "stopping"})
	}

	l := line{
		text: RenderWake(untyped), busyDeadline: w.BusyDeadline, deadlineRequired: true,
		emit: func(o Outcome) Outcome { return d.emitWake(w, r, o) },
		// A wake hold is local: S5's hold is a batch event.
		hold: func(reason, terminal string) Outcome {
			return d.emitWake(w, r, Outcome{Kind: Held, Reason: reason, Terminal: terminal})
		},
	}
	handle, o, ok := d.typeLine(ctx, l, &r, stillOwner)
	if !ok {
		return o
	}
	fin := context.WithoutCancel(ctx)
	// Confirmed: record every typed key before any ack. Without the record
	// there is no ack, so a later read retypes nothing it can prove and acks
	// nothing it cannot.
	if err := d.WakeRecords.Typed(r.WorkerID, untyped, d.Now()); err != nil {
		return d.emitWake(w, r, Outcome{Kind: Delivered, Reason: "record-failed", Terminal: handle, AckPending: true})
	}
	for _, c := range untyped {
		recs[c.Key()] = WakeRecord{State: WakeTyped}
	}
	if !d.ackable(w, recs) {
		return d.emitWake(w, r, Outcome{Kind: Delivered, Reason: "ack-held", Terminal: handle})
	}
	if !d.ackWake(fin, w) {
		return d.emitWake(w, r, Outcome{Kind: Delivered, Reason: "ack-pending", Terminal: handle, AckPending: true})
	}
	return d.emitWake(w, r, Outcome{Kind: Delivered, Terminal: handle})
}

// ackWake acknowledges w's proof, bounded, and on success moves its
// reasons' records to acked, with every older typed tick of the same
// tickers (#2069's monotone k: a landed ack for tick k covers the ticker's
// earlier ticks). A task is acked only by its own ack. True when it landed.
func (d *Deliverer) ackWake(ctx context.Context, w Wake) bool {
	if d.Wakes == nil {
		return false
	}
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), AckTimeout)
	defer cancel()
	res, err := d.Wakes.AckWake(actx, w.Proof)
	if err != nil || (res != Acked && res != Unchanged) {
		return false
	}
	keys := []string{}
	newest := map[string]time.Time{}
	for _, c := range w.components() {
		keys = append(keys, c.Key())
		if c.Kind == WakeTick && c.LastTickAt.After(newest[c.TickerID]) {
			newest[c.TickerID] = c.LastTickAt
		}
	}
	if recs, err := d.WakeRecords.List(w.WorkerID); err == nil {
		for k, rec := range recs {
			if at, ok := newest[rec.TickerID]; ok && rec.State == WakeTyped && rec.TickAt.Before(at) {
				keys = append(keys, k)
			}
		}
	}
	// A failed archive write leaves the reasons typed: the next read acks
	// them again, harmlessly (unchanged).
	_ = d.WakeRecords.Acked(w.WorkerID, keys, d.Now())
	return true
}
