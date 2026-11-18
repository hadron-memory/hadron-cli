//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"testing"
)

// S10's host-level stale clock advances for every inserted row. Routine C5
// observations must not impersonate the independently scheduled C1 sweep.
func TestIdleRecoveryDoesNotRefreshSweepClock2137(t *testing.T) {
	for _, scenario := range []string{"empty", "observe", "unproven"} {
		t.Run(scenario, func(t *testing.T) {
			f := newLoopFixture2060(t)
			switch scenario {
			case "empty":
				f.source.snapshot.Workers = nil
			case "observe":
				f.source.snapshot.SweepMode = "OBSERVE"
			case "unproven":
				f.p.relaunch.Ready = func(context.Context) error { return errors.New("scope unreadable") }
			}
			calls := 0
			f.p.relaunch.ReportSweep = func(context.Context, SweepReport) error { calls++; return nil }
			for range 10 {
				stepSweep2056(t, f)
			}
			if calls != 0 {
				t.Fatalf("routine %s poller passes refreshed S10 clock %d times", scenario, calls)
			}
			if len(f.launches) != 0 {
				t.Fatal("routine pass launched an agent")
			}
		})
	}
}
