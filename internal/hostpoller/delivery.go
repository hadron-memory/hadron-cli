package hostpoller

// Chat delivery inside the poller (cli#907, C1): after each check-in, read
// this host's due batches (S1) and hand each to the deliverer, queued on the
// same per-worker chain as host operations. The server decides recipients,
// filtering, rendering and timing; the deliverer decides only what the
// terminal shows. The poller decides only who on THIS host a batch is for.

import (
	"context"
	"errors"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

// BatchSource reads this host's due batches with the host key.
type BatchSource interface {
	Batches(ctx context.Context) ([]delivery.Batch, error)
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

// DeliveryEnabled reports whether this run delivers.
func (p *Poller) DeliveryEnabled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.batches != nil && !p.deliveryOff
}

// deliverStep reads the due batches and queues each for its worker. A
// failure here never fails the check-in: operations already ran.
func (p *Poller) deliverStep(ctx context.Context) {
	if !p.DeliveryEnabled() || ctx.Err() != nil {
		return
	}
	batches, err := p.batches.Batches(ctx)
	if errors.Is(err, ErrDeliveryScope) {
		p.mu.Lock()
		p.deliveryOff = true
		p.mu.Unlock()
		p.log("delivery-off", "", "", "", err.Error())
		p.stepDone(delivery.StepOffByScope)
		return
	}
	if err != nil {
		p.log("delivery-failed", "", "", "", "reading batches: "+err.Error())
		p.stepDone(delivery.StepReadFailed)
		return
	}
	defer p.stepDone(delivery.StepRead)
	for _, b := range batches {
		r, ok := p.recipient(b.WorkerID)
		if !ok {
			p.log("delivery-skipped", "", "", "", "a batch for a worker not registered on this host ("+b.Span()+")")
			continue
		}
		if ctx.Err() != nil {
			return
		}
		key := "deliver:" + b.WorkerID + ":" + b.BatchKey
		if b.BatchKey == "" {
			key = "deliver:" + b.WorkerID + ":" + b.Span()
		}
		p.enqueue(b.WorkerID, key, func() { p.deliverer.Deliver(ctx, b, r) })
	}
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
