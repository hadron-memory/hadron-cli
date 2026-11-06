package team

// The host poller's tick and task wake calls (hadron-cli#1178, the dueWakes
// half of cli#906 row 1): read this host's due wakes and hand back the
// proofs of the ones typed, with the host's App key. hadron-cli#1629: it
// accepts STOP and types stop notices (hadron-server#2169, cor:agt:090:04),
// and its whole-lane read declares the host's lane.

import (
	"context"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// dueWakesPage is the server's page ceiling (workers per page). A lane that
// fits one page is read whole in one call, which is what declares it as
// served for stop notices (cor:agt:090:04); a larger lane is still read, but
// does not declare, and a stop set over it stays refused.
const dueWakesPage = 200

// wakeAccepts is every reason kind this deliverer types (hadron-cli#1629).
// Listing STOP is a promise to type the notices (cor:agt:090:04): the server
// then accepts stop signals over this host's workers.
var wakeAccepts = []gen.WakeKind{gen.WakeKindMention, gen.WakeKindTick, gen.WakeKindTask, gen.WakeKindStop}

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
		resp, err := gen.DueWakes(ctx, s.client, s.app, dueWakesPage, wakeAccepts, after)
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
	// The server's line for the wake's ticks, as given: no trim, repair or
	// cut (cor:agt:080:26). A null or omitted value is "" and the renderer
	// composes its own line.
	if w.TickText != nil {
		out.TickText = *w.TickText
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
	// WakeReason.action as the server answered (cor:agt:080:23): code, state,
	// loc and URN verbatim, never resolved or rendered here. WITHHELD stays
	// WITHHELD, distinct from UNMAPPED.
	// DueStopReason as given (hadron-cli#1629). interrupt is carried and
	// never acted on: no notice sends an interrupt key (option A).
	if st := r.Stop; st != nil {
		out.Stop = &delivery.StopReason{EventID: st.EventId, SignalID: st.SignalId, Level: string(st.Level), Notice: string(st.Notice), Interrupt: st.Interrupt}
	}
	if a := r.Action; a != nil {
		out.Action = &delivery.TickAction{Code: a.Code, State: string(a.State)}
		if a.TaskLoc != nil {
			out.Action.TaskLoc = *a.TaskLoc
		}
		if a.TaskUrn != nil {
			out.Action.TaskURN = *a.TaskUrn
		}
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
