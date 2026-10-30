package orca

import (
	"context"
	"strings"
	"testing"
	"time"

	ts "github.com/hadron-memory/hadron-cli/internal/terminalstatus"
)

// wireDue is the rig's fake reads with DueTicks served by the real wake read
// against a canned dueWakes page, so the selection set and the decoding are
// part of what these pins check.
type wireDue struct {
	*fakeReads
	g graphReads
}

func (w wireDue) DueTicks(ctx context.Context, ids []string) (map[string][]ts.DueTick, error) {
	return w.g.DueTicks(ctx, ids)
}

// statusOverWire runs `status Joshua` (idle screen, clock 16:31:40) with the
// given wake reasons for w_j and the given subscriptions.
func statusOverWire(t *testing.T, anchorAt, reasons string, subs []ts.Ticker, jsonOut bool) (int, string) {
	t.Helper()
	g, _ := graphFixture(t, map[string]string{"DueTickWakes": `{"data":{"dueWakes":{"status":"OK","wakes":[
	  {"worker":{"id":"w_j"},"anchorAt":"` + anchorAt + `","reasons":[` + reasons + `]}],
	  "pageInfo":{"hasNextPage":false,"endCursor":null}}}}`})
	r := newRig(t)
	r.joshua(idleScreen)
	if subs != nil {
		r.reads.subs = map[string][]ts.Ticker{"w_j": subs}
	}
	r.env.Reads = func(context.Context, string) (Reads, error) { return wireDue{r.reads, g}, nil }
	return r.run(t, jsonOut, "Joshua")
}

func tickReason(id, lastTickAt, tickDueAt string) string {
	due := "null"
	if tickDueAt != "" {
		due = `"` + tickDueAt + `"`
	}
	return `{"kind":"TICK","ticker":{"id":"` + id + `","name":"` + id + `","lastTickAt":"` + lastTickAt + `"},"tickDueAt":` + due + `}`
}

const mention = `{"kind":"MENTION","ticker":null,"tickDueAt":null}`

// hadron-cli#1216: each tick reason is aged by its own tickDueAt
// (hadron-server#2790), not by anchorAt (the oldest reason of any kind) and not
// by the ticker's lastTickAt (its newest tick, the v1 stand-in that let a truly
// overdue merged tick read as fresh).
func TestStatusTickDueAtAgesEachMergedTick(t *testing.T) {
	five := []ts.Ticker{{ID: "tk5", Name: "tk5", Interval: 5 * time.Minute}}
	two := []ts.Ticker{{ID: "tkA", Name: "tkA", Interval: 10 * time.Minute}, {ID: "tkB", Name: "tkB", Interval: 5 * time.Minute}}
	onlyB := []ts.Ticker{{ID: "tkB", Name: "tkB", Interval: 5 * time.Minute}}
	for name, c := range map[string]struct {
		anchorAt, reasons string
		subs              []ts.Ticker
		status            string
		reason            any
	}{
		// Jane's #1211 fixture: an old mention must not age a fresh tick.
		"old mention, fresh tick": {"2026-10-29T15:00:00Z", mention + "," + tickReason("tk5", "2026-10-29T16:30:00Z", "2026-10-29T16:30:00Z"), five, "idle", nil},
		// The v1 miss: the ticker ticked again at 16:30, but its oldest
		// unanswered tick is 16:10, over one interval old.
		"old mention, overdue tick": {"2026-10-29T15:00:00Z", mention + "," + tickReason("tk5", "2026-10-29T16:30:00Z", "2026-10-29T16:10:00Z"), five, "dark", "no_turn_for_due_ticker"},
		// Joost's #1211 fixture, per ticker: A overdue, B fresh, in one wake.
		"two tickers, A overdue":      {"2026-10-29T16:00:00Z", tickReason("tkA", "2026-10-29T16:30:00Z", "2026-10-29T16:00:00Z") + "," + tickReason("tkB", "2026-10-29T16:30:00Z", "2026-10-29T16:30:00Z"), two, "dark", "no_turn_for_due_ticker"},
		"two tickers, A unsubscribed": {"2026-10-29T16:00:00Z", tickReason("tkA", "2026-10-29T16:30:00Z", "2026-10-29T16:00:00Z") + "," + tickReason("tkB", "2026-10-29T16:30:00Z", "2026-10-29T16:30:00Z"), onlyB, "idle", nil},
		// A tick-only wake is aged by its tickDueAt too, not by anchorAt.
		"tick only, tickDueAt fresh": {"2026-10-29T16:00:00Z", tickReason("tk5", "2026-10-29T16:30:00Z", "2026-10-29T16:30:00Z"), five, "idle", nil},
	} {
		t.Run(name, func(t *testing.T) {
			code, out := statusOverWire(t, c.anchorAt, c.reasons, c.subs, true)
			m := decode(t, out)
			if code != 0 || m["status"] != c.status || m["reason"] != c.reason {
				t.Fatalf("exit %d: %v", code, m)
			}
		})
	}
}

// The dark detail names the overdue ticker and its own due time.
func TestStatusTickDueAtDetailNamesTheOverdueTicker(t *testing.T) {
	two := []ts.Ticker{{ID: "tkA", Name: "tkA", Interval: 10 * time.Minute}, {ID: "tkB", Name: "tkB", Interval: 5 * time.Minute}}
	_, text := statusOverWire(t, "2026-10-29T16:00:00Z", tickReason("tkA", "2026-10-29T16:30:00Z", "2026-10-29T16:00:00Z")+","+tickReason("tkB", "2026-10-29T16:30:00Z", "2026-10-29T16:30:00Z"), two, false)
	if !strings.Contains(text, "tkA") || !strings.Contains(text, "2026-10-29T16:00:00Z") || strings.Contains(text, "tkB") {
		t.Fatalf("detail should name tkA due 16:00: %q", text)
	}
}

// A TICK reason without tickDueAt (withheld, or a server before #2790) has no
// age evidence: an idle screen fails closed to locked/unrecognized, whether
// the wake is tick-only or merged, and never falls back to anchorAt or
// lastTickAt.
func TestStatusNullTickDueAtFailsClosed(t *testing.T) {
	five := []ts.Ticker{{ID: "tk5", Name: "tk5", Interval: 5 * time.Minute}}
	for name, c := range map[string]struct{ anchorAt, reasons string }{
		"tick only": {"2026-10-29T16:00:00Z", tickReason("tk5", "2026-10-29T16:00:00Z", "")},
		"merged":    {"2026-10-29T15:00:00Z", mention + "," + tickReason("tk5", "2026-10-29T16:30:00Z", "")},
	} {
		t.Run(name, func(t *testing.T) {
			code, out := statusOverWire(t, c.anchorAt, c.reasons, five, true)
			m := decode(t, out)
			if code != 0 || m["status"] != "locked" || m["reason"] != "unrecognized" {
				t.Fatalf("exit %d: %v", code, m)
			}
		})
	}
}

// Joost's #1255 LOCAL P2s: a TICK reason is evidence only with both its ticker
// and a strictly shaped tickDueAt. A TICK with a null ticker (the wire allows
// it) and a tickDueAt time.Parse alone accepts (+24:00, a comma fraction) both
// fail closed to locked/unrecognized instead of reading idle or dark.
func TestStatusTickReasonEvidenceFailsClosed(t *testing.T) {
	five := []ts.Ticker{{ID: "tk5", Name: "tk5", Interval: 5 * time.Minute}}
	for name, reasons := range map[string]string{
		"null ticker, null tickDueAt": `{"kind":"TICK","ticker":null,"tickDueAt":null}`,
		"null ticker, bad tickDueAt":  `{"kind":"TICK","ticker":null,"tickDueAt":"not-a-time"}`,
		"null ticker, fine tickDueAt": `{"kind":"TICK","ticker":null,"tickDueAt":"2026-10-29T16:00:00Z"}`,
		"offset +24:00":               tickReason("tk5", "2026-10-29T16:30:00Z", "2026-10-29T16:00:00+24:00"),
		"comma fraction":              tickReason("tk5", "2026-10-29T16:30:00Z", "2026-10-29T16:00:00,5Z"),
	} {
		t.Run(name, func(t *testing.T) {
			code, out := statusOverWire(t, "2026-10-29T16:00:00Z", reasons, five, true)
			m := decode(t, out)
			if code != 0 || m["status"] != "locked" || m["reason"] != "unrecognized" {
				t.Fatalf("exit %d: %v", code, m)
			}
		})
	}
}
