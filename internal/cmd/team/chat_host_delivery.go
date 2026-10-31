package team

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Push states, one per recipient (cli#954). "not-pushed" is the ONE state for
// "nothing was typed and no batch was held": the server omits an off-host
// worker, an unknown worker and a worker with nothing due in the same way
// (cor:agt:080:22), so the CLI never claims which of them it was.
const (
	pushStatePushed    = "pushed"
	pushStateHeld      = "held"
	pushStateNotPushed = "not-pushed"
	// pushStateUnconfirmed: the pass ended in an error before this recipient
	// had an outcome, so whether anything was typed is not established.
	// Never "not-pushed", which asserts no batch came back.
	pushStateUnconfirmed = "unconfirmed"
)

// postPushResult is one recipient's report; Reason is a code, never text.
type postPushResult struct {
	Recipient string `json:"recipient"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
	// Acked is how many of the recipient's batches the server accepted an
	// acknowledgement for (marks advanced). pushed+ack-pending with 0 means
	// typed, marks unmoved, still due (gate refinement, #954).
	Acked int `json:"acked"`
}

// postHostPushPass is the opt-in --push surface of a pass: deliver the whole
// due batch of exactly the named recipients (the message's stored mentions),
// through the same transaction as Run. Never typed over a draft; held marks
// do not move (cor:agt:080:16).
type postHostPushPass interface {
	Push(ctx context.Context, recipients []string) ([]postPushResult, error)
}

// pushRecipients is the message's STORED mention set, in order and
// de-duplicated, minus broadcast tokens (cor:agt:080:14, cor:agt:020:04). The
// CLI never parses the body, and adds no quote or code exclusion of its own:
// the server's extraction is the one definition.
func pushRecipients(mentions []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, m := range mentions {
		t := strings.ToLower(strings.TrimSpace(m))
		if t == "" || t == "all" || t == "team" || strings.HasPrefix(t, "team.") || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// reportPostHostPush runs the push pass. A pass-level failure keeps the
// outcomes already confirmed and marks the rest not-pushed with a code; it never changes the post or its exit.
func reportPostHostPush(ctx context.Context, pass postHostPushPass, stderr io.Writer, seq int, recipients []string) []postPushResult {
	res, err := pass.Push(ctx, recipients)
	if err != nil {
		fmt.Fprintf(stderr, "note: posted seq %d; push failed or is unconfirmed; do not repost; run hadron team host run --config <host.yaml> to recover\n", seq)
	}
	return res
}

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
