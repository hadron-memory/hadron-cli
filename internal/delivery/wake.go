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
	// StopPrefix opens a stop notice's line (hadron-cli#1629,
	// cor:agt:090:04).
	StopPrefix = "[hadron stop]"
)

// StopDropped is the event reason logged, once per notice, when a halt's
// notice cannot be typed (hadron-cli#1629; Holger's option A on
// hadron-server#2169: logged and flagged to the coordinator, never typed over
// a draft, never acknowledged).
const StopDropped = "stop-dropped"

// StopDropUnrecorded is StopDropped when the outbox record could not be
// written: logged on every poll until it can be.
const StopDropUnrecorded = "stop-drop-unrecorded"

// OwnPrefixes are every prefix C1 types: a leftover opening with any of them
// is ours (submitted or cleared), never a foreign draft.
var OwnPrefixes = []string{Prefix, TickPrefix, TaskPrefix, StopPrefix}

// MaxWakeRunes bounds a wake line. A longer one is cut at a reason
// boundary and says how many reasons it left out.
const MaxWakeRunes = 1000

// WakeKind is a wake reason's kind, as dueWakes names it.
type WakeKind string

const (
	WakeMention WakeKind = "MENTION"
	WakeTick    WakeKind = "TICK"
	WakeTask    WakeKind = "TASK"
	// WakeStop is a stop-signal notice (cor:agt:090:04), offered only to a
	// dueWakes call that lists STOP in accepts (hadron-server#2169).
	WakeStop WakeKind = "STOP"
)

// StopReason is the notice a STOP reason carries (DueStopReason), as the
// server gave it. Interrupt is carried and never acted on: under Holger's
// option A no notice sends an interrupt key (hadron-cli#1629).
type StopReason struct {
	EventID   string
	SignalID  string
	Level     string
	Notice    string
	Interrupt bool
}

// TaskReason is the task a TASK reason is for (DueTaskReason).
type TaskReason struct {
	EventID    string
	ChainIndex int
	// Event is the line's wording: "assigned", "child finished", "alarm" or
	// "end session".
	Event string
	// ID and Title are the task's. A task-less alarm (S5 #2021
	// comment-6259060238: taskId null) has neither, and none is invented.
	ID    string
	Title string
	// AlarmType is a task-less alarm's closed type code from the server
	// (e.g. delivery.stuck), shown as given.
	AlarmType string
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
	// Action is what the register says this tick asks of the woken worker
	// (TICK only). Carried for the dueWakes adapter and status; never
	// rendered, never part of Key().
	Action *TickAction
	// Stop is set for a STOP reason (hadron-cli#1629).
	Stop *StopReason
}

// TickAction is WakeReason.action: the server's answer, shown never composed.
type TickAction struct {
	Code    string
	State   string
	TaskLoc string
	TaskURN string
}

// Key is the reason's stable submitted identity: tick:<tickerId>:<lastTickAt>,
// task:<eventId>:<chainIndex> or stop:<eventId> (the notice's event,
// hadron-cli#1629). "" for a MENTION, which C1 never records.
func (r WakeReason) Key() string {
	switch {
	case r.Kind == WakeTick:
		return "tick:" + r.TickerID + ":" + r.LastTickAt.UTC().Format(time.RFC3339Nano)
	case r.Kind == WakeTask && r.Task != nil:
		return fmt.Sprintf("task:%s:%d", r.Task.EventID, r.Task.ChainIndex)
	case r.Kind == WakeStop && r.Stop != nil:
		return "stop:" + r.Stop.EventID
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
	// busy deadline counts from the oldest waiting reason's due time. A zero
	// one means the server did not say, and C1 invents none: no
	// DeliverAfter holds the wake as timing-unavailable even for an idle
	// agent, and no BusyDeadline holds it so for a busy or unproven one.
	DeliverAfter time.Time
	BusyDeadline time.Time
	// TickText is DueWake.tickText: the one line the server wrote for the
	// wake's ticks (cor:agt:080:26). Typed unchanged, never recorded.
	TickText string
}

// Span is the wake for logs: reason counts only, never keys, names or
// titles.
func (w Wake) Span() string {
	n := map[WakeKind]int{}
	for _, r := range w.Reasons {
		n[r.Kind]++
	}
	span := fmt.Sprintf("wake:tick=%d,task=%d,mention=%d", n[WakeTick], n[WakeTask], n[WakeMention])
	if n[WakeStop] > 0 {
		span += fmt.Sprintf(",stop=%d", n[WakeStop])
	}
	return span
}

// components are w's TICK, TASK and STOP reasons: what C1 types.
func (w Wake) components() []WakeReason {
	out := []WakeReason{}
	for _, r := range w.Reasons {
		if r.Kind == WakeTick || r.Kind == WakeTask || r.Kind == WakeStop {
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
			if r.Task.ID == "" && r.Task.Event != "alarm" {
				return fmt.Errorf("%w: only an alarm may come without its task", ErrMalformedWake)
			}
		case WakeStop:
			if r.Stop == nil || r.Stop.EventID == "" || r.Stop.SignalID == "" || r.Stop.Level == "" || r.Stop.Notice == "" {
				return fmt.Errorf("%w: a stop without its notice", ErrMalformedWake)
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
	parts := stopParts(reasons)
	for _, r := range reasons {
		switch r.Kind {
		case WakeTick:
			parts = append(parts, TickPrefix+" "+oneLine(r.TickerName)+": scheduled check-in. Read your team chat and tasks.")
		case WakeTask:
			parts = append(parts, renderTask(r.Task))
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

// RenderWakeWith is RenderWake for a wake that carries the server's tick
// text (DueWake.tickText). When reasons hold a TICK and the text is not
// empty, the text is typed once, whole and first, after TickPrefix, and the
// TICK reasons' own generic line is dropped; TASK parts follow. The tick text
// is never reworded, joined, repaired or cut (cor:agt:080:26): MaxWakeRunes
// bounds the TASK parts only, so a long tick text leaves fewer of them, or
// none, and a text longer than the bound is typed whole. A text the host
// cannot type as one line (a control character, a line break or an escape) is
// dropped whole, never repaired (R4): the wake then types what it would
// without tick text, so a tick-only wake still names its ticker. With no tick
// text, or no TICK reason, this is RenderWake.
func RenderWakeWith(reasons []WakeReason, tickText string) string {
	hasTick := false
	var tasks []string
	for _, r := range reasons {
		switch r.Kind {
		case WakeTick:
			hasTick = true
		case WakeTask:
			tasks = append(tasks, renderTask(r.Task))
		}
	}
	if tickText == "" || !hasTick || strings.ContainsFunc(tickText, unicode.IsControl) {
		return RenderWake(reasons)
	}
	head := strings.Join(append(stopParts(reasons), TickPrefix+" "+tickText), "; ")
	text := strings.Join(append([]string{head}, tasks...), "; ")
	for kept := len(tasks) - 1; utf8.RuneCountInString(text) > MaxWakeRunes && kept >= 0; kept-- {
		text = strings.Join(append([]string{head}, tasks[:kept]...), "; ") + fmt.Sprintf("; (+%d more reasons)", len(tasks)-kept)
	}
	return text
}

// stopLineWhat is what each level's notice asks of the worker, in the
// server's wording (hadron-server src/mcp/tickerTools.ts stopLine, Uma's
// P3-5 on #2166). The wording is not part of cor:agt:090:04.
var stopLineWhat = map[string]string{
	"PAUSE_DISPATCH": "no new work is assigned to the workers it covers",
	"PAUSE":          "finish or stop at your next sub-task",
	"HALT":           "save your work and write a hand-off now",
}

// renderStop is one STOP reason's line (hadron-cli#1629): the server's
// stopLine after StopPrefix. Level and notice are closed enums; anything
// else is folded to one line, never trusted as text.
func renderStop(st *StopReason) string {
	level := oneLine(st.Level)
	if st.Notice == "RESUME" {
		return fmt.Sprintf("%s resume: %s lifted", StopPrefix, level)
	}
	what, ok := stopLineWhat[st.Level]
	if !ok {
		what = "see the stop signal"
	}
	return fmt.Sprintf("%s stop: %s (%s)", StopPrefix, level, what)
}

// stopParts are reasons' STOP lines, in order: typed first, ahead of every
// tick and task (cor:agt:090:04, "ahead of anything else"), and never cut.
func stopParts(reasons []WakeReason) []string {
	out := []string{}
	for _, r := range reasons {
		if r.Kind == WakeStop && r.Stop != nil {
			out = append(out, renderStop(r.Stop))
		}
	}
	return out
}

// renderTask is one TASK reason. A task-less alarm says its alarm type, or
// just that it has no task: a title without a task is never shown as one.
func renderTask(t *TaskReason) string {
	if t.ID != "" {
		return fmt.Sprintf("%s %s: %s (%s)", TaskPrefix, oneLine(t.Event), oneLine(t.Title), oneLine(t.ID))
	}
	if at := oneLine(t.AlarmType); at != "" {
		return fmt.Sprintf("%s %s: %s (no task)", TaskPrefix, oneLine(t.Event), at)
	}
	return fmt.Sprintf("%s %s (no task)", TaskPrefix, oneLine(t.Event))
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
	// HoldTimingUnavailable: the server gave no deliverAfter, or the agent
	// is busy or unproven and it gave no busy deadline.
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
	// Whether a wake is due is the server's to say: without its deliverAfter
	// nothing is typed, however idle the agent (Xylon, team chat #21827).
	if w.DeliverAfter.IsZero() {
		return d.emitWake(w, r, Outcome{Kind: Held, Reason: HoldTimingUnavailable})
	}
	if d.Now().Before(w.DeliverAfter) {
		return d.emitWake(w, r, Outcome{Kind: Deferred, Reason: "not-yet-due"})
	}
	if ctx.Err() != nil {
		return d.emitWake(w, r, Outcome{Kind: Deferred, Reason: "stopping"})
	}

	l := line{
		text: RenderWakeWith(untyped, w.TickText), busyDeadline: w.BusyDeadline, deadlineRequired: true,
		emit: func(o Outcome) Outcome { return d.emitWake(w, r, o) },
		// A wake hold is local: S5's hold is a batch event.
		hold: func(reason, terminal string) Outcome {
			return d.emitWake(w, r, Outcome{Kind: Held, Reason: reason, Terminal: terminal})
		},
	}
	handle, o, ok := d.typeLine(ctx, l, &r, stillOwner)
	if !ok {
		d.flagDroppedHalts(r, untyped, o)
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
	d.stampTypedStops(w.components())
	return true
}

// flagDroppedHalts records a halt notice that could not be typed
// (hadron-cli#1629; Holger's option A on hadron-server#2169): held on a draft,
// a question, an unsubmitted line or an unknown screen, failed, or skipped
// because the worker has no terminal. Nothing is typed over the composer and nothing is
// acknowledged, so the notice stays due and is retried every poll. The drop
// goes once per notice into the StopDrops outbox, which survives restarts and
// which the host's reporter turns into the coordinator's flag; the first
// record also logs one StopDropped event (span stop:HALT:<eventId>, ids
// only). A record that cannot be written is logged every time, as
// stop-drop-unrecorded, so a failing disk never reads as a quiet drop.
// A deferral (not yet due, waiting on a busy agent) is not a drop.
func (d *Deliverer) flagDroppedHalts(r Recipient, untyped []WakeReason, o Outcome) {
	if o.Kind != Held && o.Kind != Failed && (o.Kind != Skipped || o.Reason != "no-terminal") {
		return
	}
	for _, c := range untyped {
		if c.Kind != WakeStop || c.Stop == nil || c.Stop.Level != "HALT" || c.Stop.Notice == "RESUME" {
			continue
		}
		reason := StopDropped
		if d.StopDrops == nil {
			reason = StopDropUnrecorded
		} else {
			created, err := d.StopDrops.Drop(StopDrop{EventID: c.Stop.EventID, SignalID: c.Stop.SignalID, WorkerID: r.WorkerID,
				Worker: r.Name, Level: c.Stop.Level, HoldReason: o.Reason, HeldAt: d.Now().UTC()})
			switch {
			case err != nil:
				reason = StopDropUnrecorded
			case !created:
				continue
			}
		}
		if d.Emit != nil {
			d.Emit(Event{Time: d.Now().UTC(), Worker: r.Name, WorkerID: r.WorkerID, Span: "stop:HALT:" + c.Stop.EventID,
				Kind: Held, Reason: reason, Terminal: o.Terminal, ResolvedBy: r.resolvedBy, Wake: true})
		}
	}
}

// stampTypedStops marks recorded drops of the given reasons as since typed
// and acknowledged (hadron-cli#1629). Best effort: the notice is delivered
// either way, and a missed stamp only leaves a report saying it was not.
func (d *Deliverer) stampTypedStops(reasons []WakeReason) {
	if d.StopDrops == nil {
		return
	}
	for _, c := range reasons {
		if c.Kind == WakeStop && c.Stop != nil {
			_ = d.StopDrops.Typed(c.Stop.EventID, d.Now())
		}
	}
}
