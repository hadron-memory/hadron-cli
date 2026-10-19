package hostpoller

// Chat delivery inside the poller (cli#907, C1): after each check-in, read
// this host's due batches (S1) and hand each to the deliverer, queued on the
// same per-worker chain as host operations. The server decides recipients,
// filtering, rendering and timing; the deliverer decides only what the
// terminal shows. The poller decides only who on THIS host a batch is for.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

// BatchSource reads this host's due batches with the host key.
type BatchSource interface {
	Batches(ctx context.Context) ([]delivery.Batch, error)
}

// WakeSource reads this host's due wakes with the host key (dueWakes,
// paged to exhaustion over this host's registered workers).
type WakeSource interface {
	Wakes(ctx context.Context) (WakePage, error)
}

// WakePage is one step's due wakes.
type WakePage struct {
	Wakes []delivery.Wake
	// BudgetExhausted: WAKE_BUDGET_EXHAUSTED. Nothing is typed; the
	// withheld wakes stay due.
	BudgetExhausted bool
	// Dormant and Withheld are counted in status only.
	Dormant, Withheld int
}

// WakeDeliverer carries one wake to its recipient; *delivery.Deliverer is
// one.
type WakeDeliverer interface {
	DeliverWake(ctx context.Context, w delivery.Wake, r delivery.Recipient) delivery.Outcome
}

// Deliverer carries one batch to its recipient; *delivery.Deliverer is one.
type Deliverer interface {
	Deliver(ctx context.Context, b delivery.Batch, r delivery.Recipient) delivery.Outcome
}

// ErrDeliveryScope is a host key without attention:deliver
// (APP_KEY_SCOPE_REQUIRED). It does not heal by retrying, so delivery is
// switched off for the rest of the run; host operations carry on.
var ErrDeliveryScope = errors.New("the host key lacks attention:deliver: delivery is off for this run")

// EnableDelivery turns chat delivery on.
func (p *Poller) EnableDelivery(src BatchSource, d Deliverer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.batches, p.deliverer, p.deliveryOff = src, d, false
}

// EnableWakes turns tick and task wake delivery on (cli#906 row 1). Wakes
// are read only while chat delivery is on: they share its key, scope and
// per-worker chain.
func (p *Poller) EnableWakes(src WakeSource, d WakeDeliverer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.wakes, p.wakeDeliverer = src, d
}

func (p *Poller) wakesEnabled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.wakes != nil
}

// wakeStep reads the due wakes and queues each for its worker, after the
// step's batches on the same chain. It returns the read's step code.
func (p *Poller) wakeStep(ctx context.Context) string {
	page, err := p.wakes.Wakes(ctx)
	if errors.Is(err, ErrDeliveryScope) {
		p.scopeOff(err)
		return delivery.StepOffByScope
	}
	at := p.now().UTC()
	if err != nil {
		p.log("delivery-failed", "", "", "", "reading wakes failed: "+diagnostic(err))
		p.wakeRead(delivery.WakeRead{At: at, Read: delivery.StepReadFailed})
		return delivery.StepReadFailed
	}
	if page.BudgetExhausted {
		p.wakeRead(delivery.WakeRead{At: at, Read: delivery.WakeBudgetExhausted, Dormant: page.Dormant, Withheld: page.Withheld})
		day := at.Format(time.DateOnly)
		p.mu.Lock()
		first := p.budgetLogged != day
		p.budgetLogged = day
		p.mu.Unlock()
		if first {
			p.log("wake-budget-exhausted", "", "", "", "the App's daily wake budget is spent: no tick or task is typed until it resets")
		}
		return delivery.StepRead
	}
	p.wakeRead(delivery.WakeRead{At: at, Read: delivery.StepRead, Dormant: page.Dormant, Withheld: page.Withheld})
	for i, w := range page.Wakes {
		r, ok := p.recipient(w.WorkerID)
		if !ok {
			p.log("delivery-skipped", "", "", "", "a wake for a worker not registered on this host ("+w.Span()+")")
			continue
		}
		if ctx.Err() != nil {
			break
		}
		// Keyed by position, not by reason keys: the queue key is never
		// logged, but keys stay out of everything but the records.
		key := fmt.Sprintf("wake:%s:%d", w.WorkerID, i)
		p.enqueue(w.WorkerID, key, func() { p.wakeDeliverer.DeliverWake(ctx, w, r) })
	}
	return delivery.StepRead
}

func (p *Poller) wakeRead(r delivery.WakeRead) {
	if p.WakeStep != nil {
		p.WakeStep(r)
	}
}

// DeliveryEnabled reports whether this run delivers.
func (p *Poller) DeliveryEnabled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.batches != nil && !p.deliveryOff
}

// deliverStep reads the due batches and queues each for its worker, then
// does the same for the due wakes. A failure here never fails the check-in:
// operations already ran.
func (p *Poller) deliverStep(ctx context.Context) {
	if !p.DeliveryEnabled() || ctx.Err() != nil {
		return
	}
	read := p.batchStep(ctx)
	if read != delivery.StepOffByScope && p.wakesEnabled() && ctx.Err() == nil {
		if p.wakeStep(ctx) == delivery.StepOffByScope {
			read = delivery.StepOffByScope
		}
	}
	p.stepDone(read)
}

// scopeOff switches delivery off for the run. It logs only the fixed scope
// text: err may carry a source's text.
func (p *Poller) scopeOff(error) {
	p.mu.Lock()
	p.deliveryOff = true
	p.mu.Unlock()
	p.log("delivery-off", "", "", "", ErrDeliveryScope.Error())
}

// safeCode matches a code a log line may carry.
var safeCode = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// diagnostic is a source's error as a log line may carry it: the code it
// declares (DeliveryCode), when that is a plain token, else "unclassified".
// Never err.Error(): a server or proxy error may echo the request or carry
// message text (Rodolfo's §10 P2 on #939).
func diagnostic(err error) string {
	var coded interface{ DeliveryCode() string }
	if errors.As(err, &coded) && safeCode.MatchString(coded.DeliveryCode()) {
		return coded.DeliveryCode()
	}
	return "unclassified"
}

func (p *Poller) batchStep(ctx context.Context) string {
	batches, err := p.batches.Batches(ctx)
	if errors.Is(err, ErrDeliveryScope) {
		p.scopeOff(err)
		return delivery.StepOffByScope
	}
	if err != nil {
		p.log("delivery-failed", "", "", "", "reading batches failed: "+diagnostic(err))
		return delivery.StepReadFailed
	}
	for _, b := range batches {
		r, ok := p.recipient(b.WorkerID)
		if !ok {
			p.log("delivery-skipped", "", "", "", "a batch for a worker not registered on this host ("+b.Span()+")")
			continue
		}
		if ctx.Err() != nil {
			break
		}
		key := "deliver:" + b.WorkerID + ":" + b.BatchKey
		if b.BatchKey == "" {
			key = "deliver:" + b.WorkerID + ":" + b.Span()
		}
		p.enqueue(b.WorkerID, key, func() { p.deliverer.Deliver(ctx, b, r) })
	}
	return delivery.StepRead
}

// stepDone reports a finished delivery step (cli#906: `team host status`).
func (p *Poller) stepDone(read string) {
	if p.DeliveryStep != nil {
		p.DeliveryStep(p.now(), read)
	}
}

// recipient is the registered worker a batch is for, from this step's
// registration answer and this host's configuration: its name, its
// interface, and its worktrees as absolute paths.
func (p *Poller) recipient(workerID string) (delivery.Recipient, bool) {
	p.mu.Lock()
	ref, ok := p.workerRefs[workerID]
	p.mu.Unlock()
	if !ok {
		return delivery.Recipient{}, false
	}
	if _, ok := p.cfg.Workers[ref]; !ok {
		return delivery.Recipient{}, false
	}
	return p.cfg.Recipient(p.state, ref, workerID), true
}

// RefFor is the configured reference for a worker, matched by id, URN or
// name (names are unique per App case-insensitively, cor:agt:020:02), or ""
// when none matches. It is the poller's registration rule, for a caller that
// delivers on this host without a live registration (C2, cli#908).
func (c *Config) RefFor(id, urn, name string) string {
	for _, ref := range c.WorkerRefs() {
		if refMatches(ref, id, urn, name) {
			return ref
		}
	}
	return ""
}

// Recipient is the configured worker ref, resolved to workerID, as delivery
// needs it: its expected interface and its configured and created worktrees
// as absolute paths. The poller and C2 build it the same way.
func (c *Config) Recipient(s *State, ref, workerID string) delivery.Recipient {
	trees := []string{}
	for _, t := range s.worktreesFor(ref, c.Workers[ref].Worktrees) {
		trees = append(trees, c.WorktreePath(t))
	}
	return delivery.Recipient{WorkerID: workerID, Name: ref, TUI: c.Workers[ref].tui(), Worktrees: trees}
}
