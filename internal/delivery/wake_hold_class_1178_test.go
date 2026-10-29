package delivery

import (
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// hadron-cli#1178 pins the hadron-server#2746 class. On orca1, ticker-wake
// kept a send Orca never accepted as an ambiguous receipt, and that receipt
// held every later wake for its worker for two days. Here a send Orca
// refuses leaves no record, and the next pass sends a fresh request. A send
// Orca accepts without a confirmed turn start is never retyped, and it does
// not block a later wake.

// refused is a receipt Orca did not accept: what orcaterm reads from an
// error reply that names its request (orcaterm_test: "transport", req-9).
var refused = orcaterm.Receipt{ErrCode: "transport"}

func TestARefusedWakeSendDoesNotBlockLaterWakes(t *testing.T) {
	h := newHarness(t)
	h.orca.receipts = []orcaterm.Receipt{refused, refused}
	first := h.wake(tick("tk1", tickAt))
	for pass := 1; pass <= 2; pass++ {
		o := h.deliverWake(first)
		if o.Kind != Held || o.Reason != HoldNotSubmitted {
			t.Fatalf("pass %d: outcome %+v, want held %s", pass, o, HoldNotSubmitted)
		}
		if len(h.wakeRecords()) != 0 || len(h.wakeServer().acks) != 0 {
			t.Fatalf("pass %d: a refused send leaves no record and no ack: %+v", pass, h.wakeRecords())
		}
	}
	// Each pass is a fresh request carrying the whole line, never an
	// Enter-only retry of something Orca never took.
	if len(h.orca.sends) != 2 {
		t.Fatalf("sends %+v, want one fresh request per pass", h.orca.sends)
	}
	for i, s := range h.orca.sends {
		if s.text != tickLine || s.opts.EnterOnly || s.opts.NoEnter {
			t.Fatalf("send %d = %+v, want the full line as a new request", i, s)
		}
	}
	// The next tick is typed, recorded and acknowledged as normal.
	later := h.wake(tick("tk1", tickAt.Add(time.Hour)))
	later.Proof = "wake-proof-LATER"
	if o := h.deliverWake(later); o.Kind != Delivered || o.Reason != "" {
		t.Fatalf("later wake: %+v", o)
	}
	if len(h.orca.sends) != 3 || h.orca.sends[2].text != tickLine {
		t.Fatalf("sends %+v", h.orca.sends)
	}
	if acks := h.wakeServer().acks; len(acks) != 1 || acks[0] != "wake-proof-LATER" {
		t.Fatalf("acks %v, want the later proof once", acks)
	}
	h.wantWakeRecord(tick("tk1", tickAt.Add(time.Hour)).Key(), WakeAcked)
}

// Orca accepted the send but no turn started, and our line is still in the
// composer. Nothing is recorded, and a later pass types nothing twice: the
// same line is submitted with Enter alone, and a different later line
// clears ours first.
func TestAnAcceptedUnsubmittedWakeIsNeverRetypedAndDoesNotBlock(t *testing.T) {
	notStarted := orcaterm.Receipt{Accepted: true, Stages: []string{"input_accepted"}}
	ours := screen(claudeTyped(tickLine))

	t.Run("the same wake is submitted with Enter alone", func(t *testing.T) {
		h := newHarness(t)
		h.orca.receipts = []orcaterm.Receipt{notStarted}
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeIdle), ours}
		w := h.wake(tick("tk1", tickAt))
		if o := h.deliverWake(w); o.Kind != Held || o.Reason != HoldNotSubmitted || len(h.wakeRecords()) != 0 {
			t.Fatalf("first pass: %+v records %+v", o, h.wakeRecords())
		}
		h.orca.screens["term_j"], h.orca.reads = []*orcaterm.Screen{ours}, map[string]int{}
		if o := h.deliverWake(w); o.Kind != Delivered {
			t.Fatalf("second pass: %+v", o)
		}
		if len(h.orca.sends) != 2 || !h.orca.sends[1].opts.EnterOnly {
			t.Fatalf("sends %+v, want an Enter-only submit, never a retype", h.orca.sends)
		}
		h.wantWakeRecord(tick("tk1", tickAt).Key(), WakeAcked)
	})

	t.Run("a different later wake clears ours and is typed", func(t *testing.T) {
		h := newHarness(t)
		h.orca.receipts = []orcaterm.Receipt{notStarted}
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeIdle), ours}
		if o := h.deliverWake(h.wake(tick("tk1", tickAt))); o.Kind != Held || o.Reason != HoldNotSubmitted {
			t.Fatalf("first pass: %+v", o)
		}
		h.orca.screens["term_j"], h.orca.reads = []*orcaterm.Screen{ours, screen(claudeIdle)}, map[string]int{}
		later := h.wake(tick("tk1", tickAt), task("ev9", 0, "Fix the router"))
		if o := h.deliverWake(later); o.Kind != Delivered {
			t.Fatalf("later wake: %+v", o)
		}
		want := tickLine + "; [hadron task] assigned: Fix the router (task-ev9)"
		if len(h.orca.sends) != 3 || !h.orca.sends[1].opts.NoEnter || h.orca.sends[2].text != want {
			t.Fatalf("sends %+v, want a clear and then the new line", h.orca.sends)
		}
		h.wantWakeRecord("task:ev9:0", WakeAcked)
	})
}
