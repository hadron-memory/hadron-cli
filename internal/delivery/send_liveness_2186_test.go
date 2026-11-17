package delivery

import (
	"context"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// #2186: two liveness lines of #2182 are pinned directly. Each fails when its
// line is deleted; the #2181 tests pass without them because the delivery that
// follows a send lists and reads fresh anyway.

func rememberedScreen(d *Deliverer, handle string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.screens[handle]
	return ok
}

// After a Send, the shared list is ended: the next ListShared lists again and
// says it did not come from the window.
func TestSharedListSendEndsTheListDirectly2186(t *testing.T) {
	_, f, _ := roster(t, 1, claudeIdle, "100")
	c := &countOrca{Terminals: f}
	now := time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC)
	sl := NewSharedList(c, 5*time.Second, func() time.Time { return now })
	ctx := context.Background()
	if _, shared, err := sl.ListShared(ctx); err != nil || shared {
		t.Fatalf("first list: shared=%v err=%v", shared, err)
	}
	if _, shared, err := sl.ListShared(ctx); err != nil || !shared {
		t.Fatalf("a second list inside the window must be the shared one: shared=%v err=%v", shared, err)
	}
	if rc := sl.Send(ctx, "term_0", "text", orcaterm.SendOptions{}); !rc.Accepted {
		t.Fatalf("%+v", rc)
	}
	if _, shared, err := sl.ListShared(ctx); err != nil || shared {
		t.Fatalf("after a Send the list must be fresh: shared=%v err=%v", shared, err)
	}
	if n := c.lists.Load(); n != 2 {
		t.Fatalf("%d lists, want 2 (one before the Send, one after)", n)
	}
}

// After a delivery's Send, the screen remembered for the terminal is gone.
func TestASendForgetsTheScreenDirectly2186(t *testing.T) {
	d, _, rs := roster(t, 1, claudeIdle, "100")
	h := &harness{t: t, now: time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC), d: d}
	d.Now = func() time.Time { return h.now }
	if o := d.Deliver(context.Background(), batchFor(h, rs[0].WorkerID), rs[0]); o.Kind != Delivered {
		t.Fatalf("%+v", o)
	}
	if rememberedScreen(d, "term_0") {
		t.Fatal("a screen remembered from before the send is still held")
	}
}

// A composer clear forgets the screen too, and nothing after it sends: the
// clear is not proven (the leftover is still shown), so the line is held and
// the Send path's forget is not what removed the memo.
func TestAClearForgetsTheScreenDirectly2186(t *testing.T) {
	d, f, rs := roster(t, 1, claudeTyped("[hadron chat #1] Ada: older text"), "100")
	d.Caps = Capabilities{Clear: map[string]bool{"claude": true}, EnterOnly: map[string]bool{}}
	h := &harness{t: t, now: time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC), d: d}
	d.Now = func() time.Time { return h.now }
	o := d.Deliver(context.Background(), batchFor(h, rs[0].WorkerID), rs[0])
	if o.Kind != Held || o.Reason != HoldNotSubmitted {
		t.Fatalf("%+v", o)
	}
	f.mu.Lock()
	sends := append([]sendCall(nil), f.sends...)
	f.mu.Unlock()
	if len(sends) != 1 || !sends[0].opts.NoEnter {
		t.Fatalf("want exactly the one clear send: %+v", sends)
	}
	if rememberedScreen(d, "term_0") {
		t.Fatal("a screen remembered from before the clear is still held")
	}
}
