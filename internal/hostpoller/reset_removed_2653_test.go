package hostpoller

import (
	"context"
	"strings"
	"testing"
)

// A reload that removes the worker while a worker.reset-context is being
// carried out must end the reset, never run process control on an agent whose
// configuration is gone (cli#2653 item 1, Gil N3). resetIdleTarget itself is
// unit-pinned (reload_2623_test.go); these exercise the two handler arms.

// removeIris applies what a reload that removed the only worker applies: the
// live configuration with no workers. A real reload stores the same merged
// config (reloadConfig's apply block, pinned by the reload tests); landing it
// inside an injected call puts the removal exactly where a concurrent reload
// would reach the handler.
func removeIris(t *testing.T, p *Poller) {
	t.Helper()
	merged := *p.config()
	merged.Workers = map[string]WorkerConfig{}
	p.live.Store(&merged)
}

// The removal lands inside the Git read, after the worker and agent checks
// passed: the handler's own idle-target resolution must refuse, before any
// protocol step and before any process control.
func TestAResetWhoseWorkerAReloadRemovedMidHandlerRefuses2653(t *testing.T) {
	x := newResetFixture(t)
	x.onGit = func() { removeIris(t, x.p) }
	r := x.run(t, resetOp())
	wantReason(t, r, "HOST_UNSUPPORTED")
	if want := "the worker is no longer configured on this host"; !strings.HasSuffix(r.reason, want) {
		t.Fatalf("reason %q, want suffix %q", r.reason, want)
	}
	if x.proc.observed != 1 {
		t.Fatalf("observed %d, want 1: the removal lands after the agent checks", x.proc.observed)
	}
}

// The same removal one phase later — after the claim and the checkpoint,
// before the stop is asked for — ends the reset RELAUNCH_BLOCKED with nothing
// stopped: the removed worker's agent keeps running (runReset's second
// resetIdleTarget).
func TestAResetWhoseWorkerAReloadRemovedAfterTheCheckpointStopsNothing2653(t *testing.T) {
	x := newResetFixture(t)
	x.resets.checkpointNode = "hrn:node:acme.com:m:reset-cp-1"
	x.idle = func(context.Context, string) error { removeIris(t, x.p); return nil }
	x.p.register(context.Background()) //nolint:errcheck // fills workerRefs
	x.p.carryOut(context.Background(), resetOp())
	reps := x.srv.finalReports()
	if len(reps) != 1 || reps[0].outcome != Failed {
		t.Fatalf("reports %+v, want one Failed", reps)
	}
	wantReason(t, reps[0], "RELAUNCH_BLOCKED")
	if want := "the worker is no longer configured on this host, so nothing was stopped"; !strings.HasSuffix(reps[0].reason, want) {
		t.Fatalf("reason %q, want suffix %q", reps[0].reason, want)
	}
	if got := x.resets.calls; len(got) != 3 || got[0] != "claim" || got[1] != "recovery" || got[2] != "relaunch-failed" {
		t.Fatalf("protocol steps %v, want claim, recovery, relaunch-failed", got)
	}
	if x.proc.signalled != 0 {
		t.Fatalf("signalled %d times after a removal that stops nothing", x.proc.signalled)
	}
}
