package team

// The host poller's S1 delivery calls (hadron-cli#907, C1, over
// hadron-server#2017): read this host's ready-to-deliver batches and
// acknowledge the ones typed, with the host's App key. S5's hold report is
// not on the server yet (#940): Hold answers delivery.ErrHoldUnavailable
// and makes no call.

import (
	"context"
	"fmt"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

const (
	// batchProofLife is how long a batch id is good for after the server
	// issues it (deliveryBatches: "within 15 minutes"). The expiry is taken
	// from before the read, so it is never later than the server's.
	batchProofLife = 15 * time.Minute
	// deliveryBatchesPage is the server's page ceiling (workers per page).
	deliveryBatchesPage = 200
)

type (
	genBatch      = gen.DeliveryBatchesDeliveryBatchesDeliveryBatchPageBatchesDeliveryBatch
	genBatchRange = gen.DeliveryBatchesDeliveryBatchesDeliveryBatchPageBatchesDeliveryBatchRangesDeliveryBatchRange
)

func (s *hostServer) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// deliveryError maps a delivery call's error: a key without the scope turns
// delivery off for the run; anything else is that call failing. Server
// messages carry no proof.
func deliveryError(err error) error {
	if api.HasErrorCode(err, "APP_KEY_SCOPE_REQUIRED") {
		return fmt.Errorf("%w: %v", hostpoller.ErrDeliveryScope, api.MapError(err))
	}
	return api.MapError(err)
}

// Batches reads every ready-to-deliver batch of the workers registered on
// this host, paged to exhaustion. The host is the key's, so no worker is
// named. A page that breaks the contract refuses the whole read: nothing
// is typed from it.
func (s *hostServer) Batches(ctx context.Context) ([]delivery.Batch, error) {
	out := []delivery.Batch{}
	var after *string
	for {
		read := s.clock()
		resp, err := gen.DeliveryBatches(ctx, s.client, s.app, deliveryBatchesPage, after)
		if err != nil {
			return nil, deliveryError(err)
		}
		page := resp.DeliveryBatches
		if page == nil || page.PageInfo == nil {
			return nil, exitcode.Newf(exitcode.Error, "the server returned no delivery batch page")
		}
		for _, b := range page.Batches {
			if b == nil {
				continue
			}
			batch, err := batchFrom(b, read)
			if err != nil {
				return nil, err
			}
			out = append(out, batch)
		}
		if !page.PageInfo.HasNextPage {
			return out, nil
		}
		next := page.PageInfo.EndCursor
		if next == nil || *next == "" || (after != nil && *after == *next) {
			return nil, exitcode.Newf(exitcode.Error, "the server's delivery batch page has a next page but no new cursor")
		}
		after = next
	}
}

func batchFrom(b *genBatch, read time.Time) (delivery.Batch, error) {
	out := delivery.Batch{ID: b.Id, Text: b.Text, Interrupt: b.Interrupt, ProofExpiresAt: read.Add(batchProofLife), Ranges: []delivery.Range{}}
	if b.Worker != nil {
		out.WorkerID = b.Worker.Id
	}
	var err error
	if out.DeliverAfter, err = time.Parse(time.RFC3339, b.DeliverAfter); err != nil {
		return out, exitcode.Newf(exitcode.Error, "a delivery batch's deliverAfter is not a time")
	}
	if out.BusyDeadline, err = time.Parse(time.RFC3339, b.BusyDeadline); err != nil {
		return out, exitcode.Newf(exitcode.Error, "a delivery batch's busyDeadline is not a time")
	}
	for _, r := range b.Ranges {
		if r == nil {
			continue
		}
		out.Ranges = append(out.Ranges, rangeFrom(r))
	}
	return out, nil
}

func rangeFrom(r *genBatchRange) delivery.Range {
	out := delivery.Range{Incarnation: r.Incarnation, FromSeq: r.FromSeq, ThroughSeq: r.ThroughSeq, Seqs: append([]int{}, r.Seqs...)}
	if r.Channel != nil {
		out.ChannelID = r.Channel.Id
	}
	return out
}

// Ack acknowledges one typed batch, or with through its covered prefix.
// Any error, a refusal included, means nothing moved.
func (s *hostServer) Ack(ctx context.Context, batchID string, through []delivery.Through) (delivery.AckResult, error) {
	var th []*gen.BatchThrough
	for _, t := range through {
		th = append(th, &gen.BatchThrough{ChannelId: t.ChannelID, Seq: t.Seq})
	}
	resp, err := gen.AcknowledgeDeliveryBatches(ctx, s.client, []string{batchID}, th)
	if err != nil {
		return "", deliveryError(err)
	}
	r := resp.AcknowledgeDeliveryBatches
	switch {
	case r == nil:
		return "", exitcode.Newf(exitcode.Error, "the server returned no acknowledgement result")
	case r.Acknowledged == 1 && r.Unchanged == 0:
		return delivery.Acked, nil
	case r.Acknowledged == 0 && r.Unchanged == 1:
		return delivery.Unchanged, nil
	}
	return "", exitcode.Newf(exitcode.Error, "the server counted %d acknowledged and %d unchanged for one batch", r.Acknowledged, r.Unchanged)
}

// Hold is S5's hold report, not on the server yet (#940): it answers
// ErrHoldUnavailable and makes no call.
func (s *hostServer) Hold(context.Context, string, string, string) error {
	return delivery.ErrHoldUnavailable
}

// wireDelivery turns chat delivery on for this run: S1's batches, typed
// through Orca, with the status snapshot written after each step. Tick and
// task wakes stay unwired (#906 row 1 waits on hadron-server#2069/#2072),
// and holds are unreported until S5 (#940).
func wireDelivery(p *hostpoller.Poller, cfg *hostpoller.Config, srv *hostServer, emit func(hostpoller.Event)) {
	stateDir := cfg.DeliveryStateDir()
	book := delivery.NewStatusBook(stateDir)
	d := delivery.New(srv, orcaterm.NewClient(orcaterm.Exec(cfg.Delivery.OrcaBin, orcaterm.CallTimeout)), stateDir)
	d.Emit = func(e delivery.Event) {
		book.Observe(e)
		emit(deliveryEvent(e))
	}
	p.DeliveryStep = func(at time.Time, read string) {
		if err := book.Step(at, read); err != nil {
			emit(hostpoller.Event{Time: at.UTC(), Kind: "delivery-failed", Detail: "writing the delivery status: " + err.Error()})
		}
	}
	p.EnableDelivery(srv, d)
}

// deliveryEvent is one delivery outcome as a host event line: codes, ids
// and spans only, never text, draft or proof.
func deliveryEvent(e delivery.Event) hostpoller.Event {
	detail := e.Worker + " " + e.Span
	if e.Terminal != "" {
		detail += " in " + e.Terminal
	}
	if e.Unreported {
		detail += ", unreported: no server hold, alarm or escalation"
	}
	return hostpoller.Event{Time: e.Time, Kind: "delivery-" + string(e.Kind), Outcome: e.Reason, Detail: detail}
}
