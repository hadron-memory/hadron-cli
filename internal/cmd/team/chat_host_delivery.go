package team

import (
	"context"
	"fmt"
	"io"
)

// postHostTarget is the posting scope, not a grant to deliver to that App.
// The C1 adapter must resolve canonical identity and authorize its own host
// using a separate session-free key before returning a pass.
type postHostTarget struct {
	AppRef string
	Server string
}

type postHostDeliverySummary struct {
	Delivered, Held, Pending, Failed, Excluded int
}

// A pass must use C1's shared locks, current registration, terminal transaction,
// confirmation and receipt reconciliation. It never posts or marks chat read.
type postHostDeliveryPass interface {
	Run(context.Context) (postHostDeliverySummary, error)
}

type postHostDeliveryPreparer func(context.Context, string, postHostTarget) (postHostDeliveryPass, error)

// Delivery is best effort only AFTER a confirmed post and its receipt. Adapter
// errors may embed proofs or foreign drafts, so never print their text here.
// Recovery belongs to the shared poller, never another chat post.
func reportPostHostDelivery(ctx context.Context, pass postHostDeliveryPass, stderr io.Writer, seq int) {
	summary, err := pass.Run(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "note: posted seq %d; host delivery failed or is unconfirmed; do not repost; run hadron team host run --config <host.yaml> to recover\n", seq)
		return
	}
	if summary.Delivered > 0 {
		fmt.Fprintf(stderr, "note: posted seq %d; delivered %d due batches\n", seq, summary.Delivered)
	}
	if summary.Excluded > 0 {
		fmt.Fprintf(stderr, "note: posted seq %d; %d due batches are outside configured/registered worktrees; check the host configuration\n", seq, summary.Excluded)
	}
	if summary.Held != 0 || summary.Pending != 0 || summary.Failed != 0 {
		fmt.Fprintf(stderr, "note: posted seq %d; host delivery held/pending/failed (%d held, %d pending, %d failed); do not repost; run hadron team host run --config <host.yaml> to recover\n", seq, summary.Held, summary.Pending, summary.Failed)
	}
}
