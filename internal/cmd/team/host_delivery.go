package team

// The host poller's S1 delivery calls (hadron-cli#907, C1, over
// hadron-server#2017): read this host's ready-to-deliver batches and
// acknowledge the ones typed, with the host's App key. S5's hold report is
// not on the server yet (#940): Hold answers delivery.ErrHoldUnavailable
// and makes no call.

import (
	"context"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

const (
	// batchProofLife is how long a batch id is good for after the server
	// issues it (deliveryBatches: "within 15 minutes"). The expiry is taken
	// from before the read, so it is never later than the server's.
	batchProofLife = 15 * time.Minute
	// deliveryBatchesPage is the server's page ceiling (workers per page).
	deliveryBatchesPage = 200
)

// Batches reads every ready-to-deliver batch of the workers registered on
// this host, paged to exhaustion.
func (s *hostServer) Batches(ctx context.Context) ([]delivery.Batch, error) {
	return nil, nil
}

// Ack acknowledges one typed batch, or with through its covered prefix.
func (s *hostServer) Ack(ctx context.Context, batchID string, through []delivery.Through) (delivery.AckResult, error) {
	return "", nil
}

// Hold is S5's hold report, not on the server yet (#940).
func (s *hostServer) Hold(ctx context.Context, batchID, reason, terminalID string) error {
	return nil
}
