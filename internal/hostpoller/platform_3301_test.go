package hostpoller

import (
	"context"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// hadron-cli#3301 fix head (BLIND P2-3, P2-4 on C3): delivery is wired once on a platform host and follows the
// row both ways with no restart; an agent's recorded worktree token that did not resolve at this poll ("") is
// never a directory a census proves empty.

func TestPlatformDeliveryTurnsOnAtTheNextStepWhenTheRowStartedOff3301(t *testing.T) {
	fx := newPlatform3237(t, iris3237())
	fx.tree("r1", "/orca/hadron-cli/iris")
	off := false
	fx.set.DeliveryEnabled = &off
	src := &fakeBatches{}
	fx.p.EnableDelivery(src, &fakeDeliverer{})
	step(t, fx.p)
	if src.calls != 0 {
		t.Fatalf("a row with deliveryEnabled false reads no batches; read %d", src.calls)
	}
	fx.mu.Lock()
	fx.set.DeliveryEnabled = nil // null is on
	fx.mu.Unlock()
	step(t, fx.p)
	if src.calls == 0 {
		t.Fatal("turning deliveryEnabled on at the platform delivers at the next step, with no restart")
	}
	fx.mu.Lock()
	fx.set.DeliveryEnabled = &off
	fx.mu.Unlock()
	n := src.calls
	step(t, fx.p)
	if src.calls != n {
		t.Fatalf("turning it off again stops the reads at the next step: %d → %d", n, src.calls)
	}
}

func TestACensusNeverProvesAbsenceInAnUnresolvedWorktree3301(t *testing.T) {
	p, _, _ := testPoller(t)
	probed := 0
	probe := func(context.Context, hostrelaunch.Target) (hostrelaunch.Observation, error) {
		probed++
		return hostrelaunch.Observation{Proven: true}, nil
	}
	got := p.proveAgentAbsent(context.Background(), probe, []string{"/orca/hadron-cli/iris", ""})
	if got.Absent || got.Found {
		t.Fatalf("an unresolved worktree proves nothing: %+v", got)
	}
	if !strings.Contains(got.Detail, "did not resolve") {
		t.Errorf("the detail says why: %q", got.Detail)
	}
}
