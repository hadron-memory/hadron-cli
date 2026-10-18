package hostpoller

// RED (cli#907): the delivery seam's signatures; the loop follows.

import (
	"context"
	"errors"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

type BatchSource interface {
	Batches(ctx context.Context) ([]delivery.Batch, error)
}

type Deliverer interface {
	Deliver(ctx context.Context, b delivery.Batch, r delivery.Recipient) delivery.Outcome
}

var ErrDeliveryScope = errors.New("the host key lacks attention:deliver")

func (p *Poller) EnableDelivery(src BatchSource, d Deliverer) {}

func (p *Poller) DeliveryEnabled() bool { return false }
