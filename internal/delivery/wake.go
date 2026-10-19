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
	"time"
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
func (r WakeReason) Key() string { return "" }

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
func (w Wake) Span() string { return "" }

// RenderWake is the one line typed for reasons (TICK and TASK only).
func RenderWake(reasons []WakeReason) string { return "" }

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
func (d *Deliverer) DeliverWake(ctx context.Context, w Wake, r Recipient) Outcome {
	return Outcome{}
}
