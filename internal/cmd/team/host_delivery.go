package team

// The host poller's S1 delivery calls (hadron-cli#907, C1, over
// hadron-server#2017): read this host's ready-to-deliver batches and
// acknowledge the ones typed, with the host's App key. S5's hold report is
// not on the server yet (#940): Hold answers delivery.ErrHoldUnavailable
// and makes no call.

import (
	"context"
	"fmt"
	"regexp"
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

// deliveryCodes are the server codes a delivery error may carry into a log:
// S1's refusals (resolvers.deliveryBatch.ts and acknowledge.ts at server
// main ad7f1c2) and the generic auth refusals. Any other code is
// server-refused.
var deliveryCodes = []string{
	"APP_KEY_SCOPE_REQUIRED", "HOST_KEY_REQUIRED", "APP_NOT_FOUND", "APP_KEY_SESSION_BOUND",
	"BAD_USER_INPUT", "WAKE_PROOF_STALE", "WAKE_PROOF_INVALID", "WAKE_PROOF_REQUESTER_MISMATCH",
	"FORBIDDEN", "UNAUTHENTICATED",
}

// deliveryCallError is a delivery call's failure as the operator may see
// it: which call, and an allowlisted server code or a fixed class. It
// carries no upstream text: a server or proxy error may echo the request (a
// proof, the host key) or message text, and these errors reach the
// operator's log (Rodolfo's §10 P2 on #939).
type deliveryCallError struct{ call, code string }

func (e *deliveryCallError) Error() string        { return "delivery " + e.call + " failed: " + e.code }
func (e *deliveryCallError) DeliveryCode() string { return e.code }

// callError is the only constructor: a code outside the known vocabulary
// collapses to server-refused.
func callError(call, code string) *deliveryCallError {
	if !delivery.KnownCode(code) {
		code = "server-refused"
	}
	return &deliveryCallError{call: call, code: code}
}

// deliveryError classifies a delivery call's error. A key without the scope
// turns delivery off for the run; anything else is that call failing.
func deliveryError(call string, err error) error {
	e := callError(call, deliveryCodeOf(err))
	if e.code == "APP_KEY_SCOPE_REQUIRED" {
		return fmt.Errorf("%w: %w", hostpoller.ErrDeliveryScope, e)
	}
	return e
}

// deliveryCodeOf reads only codes, never messages: an allowlisted server
// code, else server-refused for a GraphQL error, else the transport class.
func deliveryCodeOf(err error) string {
	for _, code := range deliveryCodes {
		if api.HasErrorCode(err, code) {
			return code
		}
	}
	if len(api.ServerMessages(err)) > 0 {
		return "server-refused"
	}
	switch exitcode.FromError(api.MapError(err)) {
	case exitcode.AuthRequired:
		return "unauthenticated"
	case exitcode.Forbidden:
		return "forbidden"
	case exitcode.Unavailable:
		return "unavailable"
	}
	return "transport-error"
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
			return nil, deliveryError("read", err)
		}
		page := resp.DeliveryBatches
		if page == nil || page.PageInfo == nil {
			return nil, callError("read", "no-page")
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
			return nil, callError("read", "no-cursor")
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
		return out, callError("read", "bad-deliver-after")
	}
	if out.BusyDeadline, err = time.Parse(time.RFC3339, b.BusyDeadline); err != nil {
		return out, callError("read", "bad-busy-deadline")
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
		return "", deliveryError("ack", err)
	}
	r := resp.AcknowledgeDeliveryBatches
	switch {
	case r == nil:
		return "", callError("ack", "no-result")
	case r.Acknowledged == 1 && r.Unchanged == 0:
		return delivery.Acked, nil
	case r.Acknowledged == 0 && r.Unchanged == 1:
		return delivery.Unchanged, nil
	}
	return "", callError("ack", "bad-count")
}

// Hold is S5's hold report, not on the server yet (#940): it answers
// ErrHoldUnavailable and makes no call.
func (s *hostServer) Hold(context.Context, string, string, string) error {
	return delivery.ErrHoldUnavailable
}

// wireDelivery turns delivery on for this run: S1's chat batches and the
// host's due tick and task wakes (hadron-cli#1178), typed through Orca on
// the same per-worker chain, with the status snapshot written after each
// step. AckMixed follows the operator's A′ (#2072) assertion in host.yaml;
// its default is off. Holds remain unreported until S5 (#940).
// It returns the deliverer it wired.
func wireDelivery(p *hostpoller.Poller, cfg *hostpoller.Config, srv *hostServer, emit func(hostpoller.Event)) *delivery.Deliverer {
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
	d.AckMixed = cfg.Delivery.AckMixed
	d.Wakes = srv
	p.WakeStep = book.ObserveWakes
	p.EnableDelivery(srv, d)
	p.EnableWakes(srv, d)
	return d
}

// Upstream machine fields an event prints: Orca's terminal handle and the
// span built from S1's channel ids. Printed only in their plain shape, else
// withheld (Rodolfo's §10 P2 on #939: no upstream free text in a log).
var (
	safeTerminal = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
	safeSpan     = regexp.MustCompile(`^[A-Za-z0-9_.:#,-]+$`)
)

func shaped(re *regexp.Regexp, s string) string {
	if len(s) <= 2048 && re.MatchString(s) {
		return s
	}
	return "withheld"
}

// deliveryEvent is one delivery outcome as a host event line: codes, ids
// and spans only, never text, draft or proof.
func deliveryEvent(e delivery.Event) hostpoller.Event {
	detail := e.Worker + " " + shaped(safeSpan, e.Span)
	if e.Terminal != "" {
		detail += " in " + shaped(safeTerminal, e.Terminal)
	}
	if e.Unreported {
		detail += ", unreported: no server hold, alarm or escalation"
	}
	return hostpoller.Event{Time: e.Time, Kind: "delivery-" + string(e.Kind), Outcome: e.Reason, Detail: detail}
}
