// Package delivery is one batch's deliver-and-confirm transaction (cli#907,
// C1), shared by the host poller and C2's posting CLI (#908). The server
// decides everything that needs no screen: recipients, filtering, rendering,
// batching and timing (S1, hadron-server#2017). This package decides only
// what the terminal shows: whether to type, and whether the text arrived.
//
// It talks to the server through the Server seam. The GraphQL adapter
// arrives with S1/S5's schema on server main; nothing here depends on it.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Prefix opens every rendered batch (D8). It is how a deliverer recognizes
// its own leftover in a composer: anything else there is someone's draft.
const Prefix = "[hadron chat"

// MaxTextRunes bounds a batch's text: S1 caps a batch at 10 messages or
// about 6,000 characters plus an overflow line, so a much longer text is a
// contract error, not something to type.
const MaxTextRunes = 8000

// Range is one channel's part of a batch (S1 #2017 issuecomment-6246620593).
// Seqs are the exact message seqs rendered, at most ten across the batch.
type Range struct {
	ChannelID   string `json:"channelId"`
	Incarnation int    `json:"incarnation"`
	FromSeq     int    `json:"fromSeq"`
	ThroughSeq  int    `json:"throughSeq"`
	Seqs        []int  `json:"seqs"`
}

// Batch is one ready-to-deliver batch as S1 renders it.
type Batch struct {
	// ID is S1's signed proof. It is sent back to ack or hold and nowhere
	// else: never logged, stored or put in argv.
	ID string
	// BatchKey is S1's stable hash over the ranges: the same messages, the
	// same key, across re-issued proofs. A lookup key, never evidence.
	BatchKey string
	WorkerID string
	Ranges   []Range
	Text     string
	// DeliverAfter has passed for every batch S1 returns; it is checked
	// anyway. BusyDeadline is DeliverAfter plus S9's busy-delay cap: a
	// working agent defers a batch until then and no later.
	DeliverAfter   time.Time
	BusyDeadline   time.Time
	ProofExpiresAt time.Time
	// Interrupt is set only for a halt (S6, phase 4): always false now.
	Interrupt bool
}

// Span is the batch's ranges for logs: channels and seqs, never text.
func (b Batch) Span() string {
	parts := make([]string, 0, len(b.Ranges))
	for _, r := range b.Ranges {
		parts = append(parts, fmt.Sprintf("%s#%d:%d-%d", r.ChannelID, r.Incarnation, r.FromSeq, r.ThroughSeq))
	}
	return strings.Join(parts, ",")
}

// ErrMalformed is a batch that breaks S1's contract. It is neither typed nor
// acknowledged nor held (S5 has no reason for it, by design): it is logged
// by span and left due.
var ErrMalformed = errors.New("malformed batch")

// CheckBatch is the shape a batch must have before anything is typed: one
// line (a newline would submit early), our prefix, a bounded length, and
// ranges whose seqs lie inside them.
func CheckBatch(b Batch) error {
	switch {
	case b.ID == "" || b.WorkerID == "":
		return fmt.Errorf("%w: no id or worker", ErrMalformed)
	case !strings.HasPrefix(b.Text, Prefix):
		return fmt.Errorf("%w: text does not open with the batch prefix", ErrMalformed)
	case strings.ContainsAny(b.Text, "\r\n"):
		return fmt.Errorf("%w: text is not one line", ErrMalformed)
	case !utf8.ValidString(b.Text) || utf8.RuneCountInString(b.Text) > MaxTextRunes:
		return fmt.Errorf("%w: text is not valid UTF-8 or exceeds %d characters", ErrMalformed, MaxTextRunes)
	case len(b.Ranges) == 0:
		return fmt.Errorf("%w: no ranges", ErrMalformed)
	}
	for _, r := range b.Ranges {
		if r.ChannelID == "" || len(r.Seqs) == 0 || r.FromSeq > r.ThroughSeq {
			return fmt.Errorf("%w: an empty or inverted range", ErrMalformed)
		}
		prev := 0
		for _, s := range r.Seqs {
			if s < r.FromSeq || s > r.ThroughSeq || s <= prev {
				return fmt.Errorf("%w: seqs outside their range or out of order", ErrMalformed)
			}
			prev = s
		}
	}
	return nil
}

// Through limits an ack to a channel's covered prefix: Seq must be one of
// that channel's rendered seqs (S1), and the mark moves from the range's
// start to Seq and no further. Channels left out move nothing.
type Through struct {
	ChannelID string `json:"channelId"`
	Seq       int    `json:"seq"`
}

// AckResult is the server's answer for one batch id.
type AckResult string

const (
	Acked     AckResult = "acknowledged"
	Unchanged AckResult = "unchanged" // already acknowledged: a retry's success
)

// Hold reasons: S5's closed vocabulary (hadron-server#2021).
const (
	HoldDraft           = "draft"
	HoldIsolated        = "isolated"
	HoldQuestion        = "question"
	HoldScreenUnknown   = "screen-unknown"
	HoldNotSubmitted    = "not-submitted"
	HoldTwoAgents       = "two-agents"
	HoldBindingMismatch = "binding-mismatch"
)

// Server is S1's acknowledgement and S5's hold, as delivery consumes them.
// An Ack error of any kind (a refusal included: BAD_USER_INPUT,
// WAKE_PROOF_STALE) means nothing moved, so a confirmed record is kept.
type Server interface {
	Ack(ctx context.Context, batchID string, through []Through) (AckResult, error)
	Hold(ctx context.Context, batchID, reason, terminalID string) error
}
