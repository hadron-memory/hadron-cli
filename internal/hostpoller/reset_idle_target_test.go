package hostpoller

import (
	"context"
	"errors"
	"testing"
)

func TestResetIdleTargetAtBothProofs2057(t *testing.T) {
	x := newFwdFixture(t)
	x.p.register(context.Background()) //nolint:errcheck // initialize worker refs
	n := 0
	x.p.resetDeps.Idle = func(_ context.Context, got ResetIdleTarget) error {
		n++
		want := ResetIdleTarget{WorkerRef: "iris", WorkerID: "id-iris", SessionID: "s-distinct-2074", Worktree: x.p.cfg.WorktreePath("repo/iris"), Family: "claude", Process: oldIdentity}
		if got != want {
			t.Fatalf("proof %d: got %+v want %+v", n, got, want)
		}
		if n == 2 {
			return errors.New("NOT_IDLE")
		}
		return nil
	}
	op := resetOp()
	op.Args["sessionId"] = "s-distinct-2074"
	x.p.workerResetContext(context.Background(), op)
	if n != 2 {
		t.Fatalf("proof count %d", n)
	}
	if x.fp.signals != 0 {
		t.Fatal("signalled after second idle proof refused")
	}
	for _, call := range x.fr.calls {
		if call == "authorize@1 pid=4242" {
			t.Fatal("authorized before second proof")
		}
	}
}
