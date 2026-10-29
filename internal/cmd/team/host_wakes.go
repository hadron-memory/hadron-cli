package team

// The host poller's tick and task wake calls (hadron-cli#1178, the dueWakes
// half of cli#906 row 1): read this host's due wakes and hand back the
// proofs of the ones typed, with the host's App key. No accepts, so the
// server never offers a STOP notice this deliverer cannot type.

import (
	"context"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// dueWakesPage is the server's page ceiling (workers per page).
const dueWakesPage = 200

type (
	genWake       = gen.DueWakesDueWakesDueWakePageWakesDueWake
	genWakeReason = gen.DueWakesDueWakesDueWakePageWakesDueWakeReasonsWakeReason
)

// Wakes reads every due wake of the workers registered on this host, paged
// to exhaustion. A page that breaks the contract refuses the whole read:
// nothing is typed from it. A spent budget returns no wakes at all.
func (s *hostServer) Wakes(ctx context.Context) (hostpoller.WakePage, error) {
	out := hostpoller.WakePage{Wakes: []delivery.Wake{}}
	var after *string
	// Every cursor seen: a repeat at any distance is a cycle (Joost's BLIND
	// P2 on #1183), and refuses the read rather than paging forever.
	seen := map[string]bool{}
	for {
		resp, err := gen.DueWakes(ctx, s.client, s.app, dueWakesPage, after)
		if err != nil {
			return hostpoller.WakePage{}, deliveryError("wake-read", err)
		}
		page := resp.DueWakes
		if page == nil || page.PageInfo == nil {
			return hostpoller.WakePage{}, callError("wake-read", "no-page")
		}
		out.Dormant += len(page.Dormant)
		// withheld is the App's count, not this page's: keep the largest.
		out.Withheld = max(out.Withheld, page.Withheld)
		if page.Status == gen.DueWakeStatusWakeBudgetExhausted {
			out.BudgetExhausted, out.Wakes = true, []delivery.Wake{}
			return out, nil
		}
		for _, w := range page.Wakes {
			if w == nil {
				continue
			}
			wake, err := wakeFrom(w)
			if err != nil {
				return hostpoller.WakePage{}, err
			}
			out.Wakes = append(out.Wakes, wake)
		}
		if !page.PageInfo.HasNextPage {
			return out, nil
		}
		next := page.PageInfo.EndCursor
		if next == nil || *next == "" || seen[*next] {
			return hostpoller.WakePage{}, callError("wake-read", "no-cursor")
		}
		seen[*next] = true
		after = next
	}
}

func wakeFrom(w *genWake) (delivery.Wake, error) {
	out := delivery.Wake{Proof: w.Proof, NamesChannels: len(w.Channels) > 0, Reasons: []delivery.WakeReason{}}
	if w.Worker != nil {
		out.WorkerID = w.Worker.Id
	}
	var err error
	if out.DeliverAfter, err = time.Parse(time.RFC3339, w.DeliverAfter); err != nil {
		return out, callError("wake-read", "bad-deliver-after")
	}
	if out.BusyDeadline, err = time.Parse(time.RFC3339, w.BusyDeadline); err != nil {
		return out, callError("wake-read", "bad-busy-deadline")
	}
	for _, r := range w.Reasons {
		if r != nil {
			out.Reasons = append(out.Reasons, reasonFrom(r))
		}
	}
	return out, nil
}

// reasonFrom maps one reason as given. A reason missing what its kind needs
// (a tick without its ticker or tick time) maps incomplete, and the
// deliverer refuses that wake as malformed: logged, never typed, left due.
func reasonFrom(r *genWakeReason) delivery.WakeReason {
	out := delivery.WakeReason{Kind: delivery.WakeKind(r.Kind)}
	if t := r.Ticker; t != nil {
		out.TickerID, out.TickerName = t.Id, t.Name
		if t.LastTickAt != nil {
			if at, err := time.Parse(time.RFC3339, *t.LastTickAt); err == nil {
				out.LastTickAt = at
			}
		}
	}
	if t := r.Task; t != nil {
		out.Task = &delivery.TaskReason{EventID: t.EventId, ChainIndex: t.ChainIndex, Event: wakeEventWords(string(t.Event)), ID: t.Id, Title: t.Title}
	}
	return out
}

// wakeEventWords is a TaskWakeEvent as the typed line words it: ASSIGNED is
// "assigned", CHILD_FINISHED "child finished". A value this build does not
// know is worded the same way rather than refused.
func wakeEventWords(ev string) string {
	return strings.ToLower(strings.ReplaceAll(ev, "_", " "))
}

// AckWake hands back one typed wake's proof. Any error, a refusal included,
// means nothing moved.
func (s *hostServer) AckWake(ctx context.Context, proof string) (delivery.AckResult, error) {
	resp, err := gen.AcknowledgeWakes(ctx, s.client, []string{proof})
	if err != nil {
		return "", deliveryError("wake-ack", err)
	}
	r := resp.AcknowledgeWakes
	switch {
	case r == nil:
		return "", callError("wake-ack", "no-result")
	case r.Acknowledged == 1 && r.Unchanged == 0:
		return delivery.Acked, nil
	case r.Acknowledged == 0 && r.Unchanged == 1:
		return delivery.Unchanged, nil
	}
	return "", callError("wake-ack", "bad-count")
}
