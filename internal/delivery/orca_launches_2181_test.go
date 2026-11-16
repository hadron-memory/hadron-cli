package delivery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// #2181: a poll cycle's `orca` process count must not grow with the number of
// workers that have nothing to decide. Every method call on this fake is one
// `orca` process on the real client.
type countOrca struct {
	Terminals
	lists, reads, sends atomic.Int64
}

func (c *countOrca) List(ctx context.Context) ([]orcaterm.Terminal, error) {
	c.lists.Add(1)
	return c.Terminals.List(ctx)
}
func (c *countOrca) ReadScreen(ctx context.Context, h string) *orcaterm.Screen {
	c.reads.Add(1)
	return c.Terminals.ReadScreen(ctx, h)
}
func (c *countOrca) Send(ctx context.Context, h, t string, o orcaterm.SendOptions) orcaterm.Receipt {
	c.sends.Add(1)
	return c.Terminals.Send(ctx, h, t, o)
}

// roster builds n workers, each with a worktree and one terminal whose screen
// is the given tail and whose lastOutputAt is stamp ("" = the list gave none).
func roster(t *testing.T, n int, tail []string, stamp string) (*Deliverer, *fakeOrca, []Recipient) {
	t.Helper()
	h := newHarness(t)
	h.orca.terms = nil
	h.orca.screens = map[string][]*orcaterm.Screen{}
	var rs []Recipient
	bound := map[string]string{}
	for i := 0; i < n; i++ {
		tree := filepath.Join(t.TempDir(), fmt.Sprintf("w%d", i))
		if err := os.MkdirAll(filepath.Join(tree, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("w-%d", i)
		handle := fmt.Sprintf("term_%d", i)
		bound[tree] = id
		h.orca.terms = append(h.orca.terms, orcaterm.Terminal{Handle: handle, WorktreePath: tree, AgentIdentity: ptr("claude"),
			Connected: true, Writable: true, LastOutputAt: stamp})
		h.orca.screens[handle] = []*orcaterm.Screen{screen(tail)}
		rs = append(rs, Recipient{WorkerID: id, Name: id, TUI: "claude", Worktrees: []string{tree}})
	}
	h.d.Bound = func(w string) string { return bound[w] }
	return h.d, h.orca, rs
}

func batchFor(h *harness, id string) Batch {
	b := h.batch()
	b.WorkerID = id
	b.ID = "proof-" + id
	b.BatchKey = "key-" + id
	return b
}

var claudeDraft = []string{"⏺ Done.", rule, "❯ half a sentence", rule, claudeHint}

// A cycle's lists are shared: N held workers cost ONE list, not N.
func TestOneListServesEveryDeliveryOfACycle2181(t *testing.T) {
	d, f, rs := roster(t, 20, claudeDraft, "100")
	c := &countOrca{Terminals: f}
	now := time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC)
	d.Orca = NewSharedList(c, 5*time.Second, func() time.Time { return now })
	h := &harness{t: t, now: now, d: d}
	d.Emit = func(Event) {} // the harness collector is not safe for goroutines
	var wg sync.WaitGroup
	for _, r := range rs {
		wg.Add(1)
		go func() { defer wg.Done(); d.Deliver(context.Background(), batchFor(h, r.WorkerID), r) }()
	}
	wg.Wait()
	if n := c.lists.Load(); n != 1 {
		t.Fatalf("20 workers made %d list calls in one cycle, want 1", n)
	}
	now = now.Add(6 * time.Second)
	d.Deliver(context.Background(), batchFor(h, rs[0].WorkerID), rs[0])
	if n := c.lists.Load(); n != 2 {
		t.Fatalf("a list older than its window must be read again: %d", n)
	}
}

func TestAListErrorIsNeverShared2181(t *testing.T) {
	d, f, rs := roster(t, 1, claudeDraft, "100")
	c := &countOrca{Terminals: f}
	now := time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC)
	d.Orca = NewSharedList(c, 5*time.Second, func() time.Time { return now })
	h := &harness{t: t, now: now, d: d}
	f.listErr = fmt.Errorf("boom")
	d.Deliver(context.Background(), batchFor(h, rs[0].WorkerID), rs[0])
	f.listErr = nil
	d.Deliver(context.Background(), batchFor(h, rs[0].WorkerID), rs[0])
	if n := c.lists.Load(); n != 2 {
		t.Fatalf("a failed list was served again: %d calls", n)
	}
}

func TestASendEndsTheSharedList2181(t *testing.T) {
	d, f, rs := roster(t, 2, claudeIdle, "100")
	c := &countOrca{Terminals: f}
	now := time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC)
	d.Orca = NewSharedList(c, 5*time.Second, func() time.Time { return now })
	h := &harness{t: t, now: now, d: d}
	if o := d.Deliver(context.Background(), batchFor(h, rs[0].WorkerID), rs[0]); o.Kind != Delivered {
		t.Fatalf("%+v", o)
	}
	d.Deliver(context.Background(), batchFor(h, rs[1].WorkerID), rs[1])
	if n := c.lists.Load(); n != 2 {
		t.Fatalf("a delivery after a send must list fresh: %d", n)
	}
}

// A held batch is not screen-read again while its terminal shows no new
// output since the last read.
func TestAHeldBatchIsNotReadAgainWhileTheTerminalIsQuiet2181(t *testing.T) {
	d, f, rs := roster(t, 1, claudeDraft, "100")
	c := &countOrca{Terminals: f}
	d.Orca = c
	h := &harness{t: t, now: time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC), d: d}
	d.Now = func() time.Time { return h.now }
	b := batchFor(h, rs[0].WorkerID)
	for i := 0; i < 5; i++ {
		if o := d.Deliver(context.Background(), b, rs[0]); o.Kind != Held || o.Reason != HoldDraft {
			t.Fatalf("cycle %d: %+v", i, o)
		}
	}
	if n := c.reads.Load(); n != 1 {
		t.Fatalf("5 cycles of a held draft made %d screen reads, want 1", n)
	}
	if c.sends.Load() != 0 {
		t.Fatal("typed over a draft")
	}
}

func TestNewOutputOrNoStampMeansAFreshRead2181(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stamp func(i int) string
	}{
		{"new output each cycle", func(i int) string { return fmt.Sprint(100 + i) }},
		{"the list gave no stamp", func(int) string { return "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, f, rs := roster(t, 1, claudeDraft, "")
			c := &countOrca{Terminals: f}
			d.Orca = c
			h := &harness{t: t, now: time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC), d: d}
			d.Now = func() time.Time { return h.now }
			for i := 0; i < 3; i++ {
				f.terms[0].LastOutputAt = tc.stamp(i)
				d.Deliver(context.Background(), batchFor(h, rs[0].WorkerID), rs[0])
			}
			if n := c.reads.Load(); n != 3 {
				t.Fatalf("%d reads, want 3", n)
			}
		})
	}
}

// Remembered evidence may keep a batch held; it never lets one be typed. When
// the remembered screen would allow a send, the screen is read again first.
func TestARememberedScreenNeverAllowsASend2181(t *testing.T) {
	d, f, rs := roster(t, 1, claudeBusy, "100")
	c := &countOrca{Terminals: f}
	d.Orca = c
	h := &harness{t: t, now: time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC), d: d}
	d.Now = func() time.Time { return h.now }
	b := batchFor(h, rs[0].WorkerID)
	if o := d.Deliver(context.Background(), b, rs[0]); o.Kind != Deferred || o.Reason != "busy" {
		t.Fatalf("%+v", o)
	}
	// The busy-delay cap passes with no new output: the remembered busy
	// screen now allows a send, so it must be read again before typing.
	h.now = h.now.Add(11 * time.Minute)
	b.ProofExpiresAt = h.now.Add(15 * time.Minute)
	b.DeliverAfter = h.now.Add(-time.Minute)
	f.screens["term_0"] = []*orcaterm.Screen{screen(claudeBusy), screen(claudeIdle)}
	if o := d.Deliver(context.Background(), b, rs[0]); o.Kind != Delivered {
		t.Fatalf("%+v", o)
	}
	if n := c.reads.Load(); n < 2 {
		t.Fatalf("typed on a remembered screen: %d reads", n)
	}
}

func TestASendForgetsTheRememberedScreen2181(t *testing.T) {
	d, f, rs := roster(t, 1, claudeIdle, "100")
	c := &countOrca{Terminals: f}
	d.Orca = c
	h := &harness{t: t, now: time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC), d: d}
	d.Now = func() time.Time { return h.now }
	if o := d.Deliver(context.Background(), batchFor(h, rs[0].WorkerID), rs[0]); o.Kind != Delivered {
		t.Fatalf("%+v", o)
	}
	reads := c.reads.Load()
	// The terminal now shows a draft but its stamp is the one listed before
	// the send: the old idle screen must not be believed.
	f.screens["term_0"] = []*orcaterm.Screen{screen(claudeDraft)}
	f.mu.Lock()
	f.reads["term_0"] = 0
	f.mu.Unlock()
	b2 := batchFor(h, rs[0].WorkerID)
	b2.BatchKey = "key-2"
	if o := d.Deliver(context.Background(), b2, rs[0]); o.Kind != Held || o.Reason != HoldDraft {
		t.Fatalf("%+v", o)
	}
	if c.reads.Load() <= reads {
		t.Fatal("a screen remembered from before a send was believed")
	}
}

// Our own older leftover in the composer is acted on (cleared) only from a
// screen read now, never a remembered one.
func TestARememberedOwnLeftoverIsReadAgainBeforeItIsTouched2181(t *testing.T) {
	d, f, rs := roster(t, 1, claudeTyped("[hadron chat #1] Ada: older text"), "100")
	c := &countOrca{Terminals: f}
	d.Orca = c
	d.Caps = Capabilities{Clear: map[string]bool{}, EnterOnly: map[string]bool{}}
	h := &harness{t: t, now: time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC), d: d}
	d.Now = func() time.Time { return h.now }
	for i := 0; i < 2; i++ {
		if o := d.Deliver(context.Background(), batchFor(h, rs[0].WorkerID), rs[0]); o.Kind != Held {
			t.Fatalf("cycle %d: %+v", i, o)
		}
	}
	if n := c.reads.Load(); n != 2 {
		t.Fatalf("%d reads: our own leftover was judged on a remembered screen", n)
	}
}

// A list shared with an earlier delivery may keep a line held; it may never
// send. A second agent that appeared in the worktree inside the window must
// still hold the delivery (Joaquin F1, Jibril P1).
func TestASharedListNeverAuthorisesASendPastASecondAgent2181(t *testing.T) {
	d, f, rs := roster(t, 1, claudeIdle, "100")
	now := time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC)
	d.Orca = NewSharedList(f, 5*time.Second, func() time.Time { return now })
	h := &harness{t: t, now: now, d: d}
	if _, err := d.Orca.List(context.Background()); err != nil { // an earlier delivery's list
		t.Fatal(err)
	}
	second := f.terms[0]
	second.Handle = "term_second"
	f.terms = append(f.terms, second)
	f.screens["term_second"] = []*orcaterm.Screen{screen(claudeIdle)}
	o := d.Deliver(context.Background(), batchFor(h, rs[0].WorkerID), rs[0])
	if o.Kind != Held || o.Reason != HoldTwoAgents || len(f.sends) != 0 {
		t.Fatalf("outcome %+v, sends %d: want held two-agents and nothing typed", o, len(f.sends))
	}
}

// Remembered evidence has an age limit: a draft that vanished without the
// list's stamp moving (a stamp is milliseconds, and not a promise) is found.
func TestARememberedHoldExpires2181(t *testing.T) {
	d, f, rs := roster(t, 1, claudeDraft, "100")
	c := &countOrca{Terminals: f}
	d.Orca = c
	h := &harness{t: t, now: time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC), d: d}
	d.Now = func() time.Time { return h.now }
	b := batchFor(h, rs[0].WorkerID)
	if o := d.Deliver(context.Background(), b, rs[0]); o.Kind != Held {
		t.Fatalf("%+v", o)
	}
	h.now = h.now.Add(ScreenMemoLife - time.Second)
	d.Deliver(context.Background(), b, rs[0])
	if c.reads.Load() != 1 {
		t.Fatalf("a memo inside its life was read again: %d", c.reads.Load())
	}
	h.now = h.now.Add(2 * time.Second)
	b.ProofExpiresAt = h.now.Add(15 * time.Minute)
	b.DeliverAfter = h.now.Add(-time.Minute)
	f.screens["term_0"] = []*orcaterm.Screen{screen(claudeIdle)} // the draft is gone, the stamp is not
	f.mu.Lock()
	f.reads["term_0"] = 0
	f.mu.Unlock()
	if o := d.Deliver(context.Background(), b, rs[0]); o.Kind != Delivered {
		t.Fatalf("an expired memo kept a line held: %+v", o)
	}
}
