package teamrouter

// Pins beyond the TS suite, added where the author mutation check found a
// guard router.test.ts does not tell apart from a sibling (cli#829 #883).
// Each names the mutant it kills.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// freshLockSwapServer swaps the lock's owner on the SECOND attention call —
// the tokenless re-read after the gates — so only the ownership check right
// before the send can catch it. (C4 swaps it on the first, which the check
// before the re-read already catches.) Mutant: drop that last check.
type freshLockSwapServer struct {
	*fakeCLIs
	c     *Config
	calls int
}

func (w *freshLockSwapServer) AttentionPage(ctx context.Context, req AttentionRequest) (AttentionPage, error) {
	w.calls++
	if w.calls == 2 {
		writeOwner(w.t, w.c, "someone-else")
	}
	return w.fakeCLIs.AttentionPage(ctx, req)
}

func TestMutantPinLockLostDuringFreshReReadStopsBeforeSend(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	wrapped := &freshLockSwapServer{fakeCLIs: f, c: c}
	_, err := PollOnce(t.Context(), c, wrapped, f.orca, func(string) {}, nil)
	if err == nil || !strings.Contains(err.Error(), "lost the writer lock") {
		t.Fatalf("err = %v, want the lost-lock refusal", err)
	}
	if wrapped.calls != 2 {
		t.Fatalf("attention calls = %d, want the main scan and the re-read", wrapped.calls)
	}
	wantNoSend(t, f)
}

// A flagged worker observed current at the probe (head <= cursor) resets its
// clock (router.ts: "observed current: the worker read the chat"), so its
// next idle episode starts from zero. Mutant: skip the reset — the stale
// 21-minute episode would make the worker owed the moment chat moves again.
func TestMutantPinLivenessProbeObservedCurrentResetsClock(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 120 // == head 120: caught up
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	out := f.poll(t, c)
	if len(out.Liveness) != 0 || len(out.LivenessDropped) != 0 {
		t.Fatalf("outcome = %+v, want a silent reset at the probe — not a send, not a drop", out)
	}
	// The probe decides it: the head is read once, never re-read by the full
	// guard chain's recompute.
	if f.headRead != 1 {
		t.Fatalf("head reads = %d, want 1 (the full chain ran for a caught-up worker)", f.headRead)
	}
	if rec := readLiveness(t, c)["w-ada"]; rec["idleSince"] != nil {
		t.Fatalf("record = %v, want the clock reset (no idleSince)", rec)
	}
	// The chat moves on: the reset episode restarts now, so nothing is owed yet.
	f.cursors["w-ada"] = 100
	if out := f.poll(t, c); len(out.Liveness) != 0 || len(f.sent) != 0 {
		t.Fatalf("liveness = %v sent = %v, want nothing until a fresh N minutes pass", out.Liveness, f.sent)
	}
}
