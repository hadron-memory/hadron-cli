package hostpoller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

// cli#906 row 1: the poller reads this host's due wakes after its batches,
// in the same step, and queues each on its worker's chain.

type fakeWakes struct {
	mu    sync.Mutex
	calls int
	page  WakePage
	err   error
	order *[]string
}

func (f *fakeWakes) Wakes(context.Context) (WakePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.order != nil {
		*f.order = append(*f.order, "wakes")
	}
	return f.page, f.err
}

type wakeDelivered struct {
	wake delivery.Wake
	to   delivery.Recipient
}

type fakeWakeDeliverer struct {
	mu  sync.Mutex
	got []wakeDelivered
}

func (f *fakeWakeDeliverer) DeliverWake(_ context.Context, w delivery.Wake, r delivery.Recipient) delivery.Outcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, wakeDelivered{w, r})
	return delivery.Outcome{Kind: delivery.Delivered}
}

func (f *fakeWakeDeliverer) deliveries() []wakeDelivered {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]wakeDelivered{}, f.got...)
}

func wakeFor(worker string) delivery.Wake {
	return delivery.Wake{Proof: "proof", WorkerID: worker,
		Reasons: []delivery.WakeReason{{Kind: delivery.WakeTick, TickerID: "tk", TickerName: "Check-in", LastTickAt: time.Unix(1, 0)}}}
}

func wakePoller(t *testing.T) (*Poller, *fakeBatches, *fakeWakes, *fakeWakeDeliverer) {
	t.Helper()
	p, _, _, src, _ := deliveryPoller(t)
	ws, wd := &fakeWakes{}, &fakeWakeDeliverer{}
	p.EnableWakes(ws, wd)
	return p, src, ws, wd
}

func TestWakesAreNotReadUnlessEnabled(t *testing.T) {
	p, _, _, _, _ := deliveryPoller(t)
	step(t, p) // no wake source: nothing to call
	ws := &fakeWakes{}
	p2, _, _ := testPoller(t)
	p2.EnableWakes(ws, &fakeWakeDeliverer{})
	step(t, p2)
	if ws.calls != 0 {
		t.Fatal("wakes read while chat delivery is off")
	}
}

func TestAWakeIsQueuedForItsRegisteredWorkerAfterTheBatchRead(t *testing.T) {
	p, src, ws, wd := wakePoller(t)
	order := []string{}
	ws.order = &order
	src.queue = [][]delivery.Batch{{batchFor("id-iris", 5)}}
	ws.page = WakePage{Wakes: []delivery.Wake{wakeFor("id-iris")}}
	step(t, p)
	got := wd.deliveries()
	if len(got) != 1 {
		t.Fatalf("wake deliveries %+v", got)
	}
	if r := got[0].to; r.WorkerID != "id-iris" || r.Name != "iris" || r.TUI != "claude" || len(r.Worktrees) != 1 {
		t.Fatalf("recipient %+v", r)
	}
	if src.calls != 1 || len(order) != 1 {
		t.Fatalf("batch reads %d, wake reads %v", src.calls, order)
	}
}

func TestAWakeForAnUnregisteredWorkerIsNotDelivered(t *testing.T) {
	p, _, ws, wd := wakePoller(t)
	events := recordEvents(p)
	ws.page = WakePage{Wakes: []delivery.Wake{wakeFor("id-stranger")}}
	step(t, p)
	if len(wd.deliveries()) != 0 {
		t.Fatalf("delivered to a stranger: %+v", wd.deliveries())
	}
	if !hasEvent(events(), "delivery-skipped", "a wake for a worker not registered on this host") {
		t.Fatalf("events %+v", events())
	}
}

func TestAnExhaustedBudgetTypesNothingAndIsLoggedOncePerDay(t *testing.T) {
	p, _, ws, wd := wakePoller(t)
	events := recordEvents(p)
	var reads []delivery.WakeRead
	p.WakeStep = func(r delivery.WakeRead) { reads = append(reads, r) }
	ws.page = WakePage{BudgetExhausted: true, Withheld: 3, Dormant: 1, Wakes: []delivery.Wake{wakeFor("id-iris")}}
	step(t, p)
	step(t, p)
	if len(wd.deliveries()) != 0 {
		t.Fatal("typed with the budget spent")
	}
	n := 0
	for _, e := range events() {
		if e.Kind == "wake-budget-exhausted" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("logged %d times in one day", n)
	}
	if len(reads) != 2 || reads[1].Read != delivery.WakeBudgetExhausted || reads[1].Withheld != 3 || reads[1].Dormant != 1 {
		t.Fatalf("wake reads %+v", reads)
	}
	p.now = func() time.Time { return time.Date(2026, 10, 16, 0, 0, 1, 0, time.UTC) }
	step(t, p)
	n = 0
	for _, e := range events() {
		if e.Kind == "wake-budget-exhausted" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("a new day logs it again: %d", n)
	}
}

func TestAWakeScopeRefusalSwitchesDeliveryOff(t *testing.T) {
	p, src, ws, _ := wakePoller(t)
	var reads []string
	p.DeliveryStep = func(_ time.Time, read string) { reads = append(reads, read) }
	ws.err = ErrDeliveryScope
	step(t, p)
	step(t, p)
	if p.DeliveryEnabled() || ws.calls != 1 || src.calls != 1 {
		t.Fatalf("enabled %v wake reads %d batch reads %d", p.DeliveryEnabled(), ws.calls, src.calls)
	}
	if len(reads) != 1 || reads[0] != delivery.StepOffByScope {
		t.Fatalf("steps %v", reads)
	}
}

func TestAFailedWakeReadIsReportedAndTheBatchesStillGo(t *testing.T) {
	p, src, ws, _ := wakePoller(t)
	var reads []delivery.WakeRead
	p.WakeStep = func(r delivery.WakeRead) { reads = append(reads, r) }
	ws.err = errors.New("gateway 502")
	src.queue = [][]delivery.Batch{{batchFor("id-iris", 5)}}
	if err := p.Step(context.Background()); err != nil {
		t.Fatalf("a failed wake read must not fail the step: %v", err)
	}
	p.Wait()
	if len(reads) != 1 || reads[0].Read != delivery.StepReadFailed || !p.DeliveryEnabled() {
		t.Fatalf("reads %+v", reads)
	}
}
