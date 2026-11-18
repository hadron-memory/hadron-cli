//go:build linux

package hostpoller

import (
	"context"
	"testing"
	"time"
)

func TestSweepS10UnavailableKeepsTokenPendingAndRetries2153(t *testing.T) {
	f := newLoopFixture2060(t)
	f.p.relaunch.Ready = func(context.Context) error { return ErrOrcaDown }
	// #2156 F9: a pending outage backs off while S10 is unavailable, so each
	// retry happens after the backoff ceiling (30m) has passed.
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.p.now = func() time.Time { return clock }
	var tokens []string
	f.p.relaunch.ReportSweep = func(_ context.Context, r SweepReport) error {
		tokens = append(tokens, r.RunID)
		if len(tokens) < 3 {
			return ErrSweepReportingUnavailable
		}
		return nil
	}
	for i := 0; i < 3; i++ {
		clock = clock.Add(31 * time.Minute)
		stepSweep2056(t, f)
		state, err := LoadState(f.p.state.path)
		if err != nil {
			t.Fatal(err)
		}
		f.p.state = state
		if len(tokens) != i+1 || state.SweepOutage == nil || state.SweepOutage.Reported != (i == 2) {
			t.Fatalf("attempt%d tokens=%v state=%+v", i+1, tokens, state.SweepOutage)
		}
	}
	if tokens[0] == "" || tokens[0] != tokens[1] || tokens[1] != tokens[2] {
		t.Fatalf("unsupported reporting changed episode identity: %v", tokens)
	}
	clock = clock.Add(31 * time.Minute)
	stepSweep2056(t, f)
	if len(tokens) != 3 || len(f.launches) != 0 {
		t.Fatalf("acknowledged outage retried or launched: %v %v", tokens, f.launches)
	}
}
