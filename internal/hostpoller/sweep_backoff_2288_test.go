//go:build linux

package hostpoller

import (
	"context"
	"testing"
	"time"
)

// cli#2288 — the two #2274 survivors the §10 review left in the S10 outage
// backoff (backOffOutageReport). How often the host retries recording a run is
// the CLI's own timing (no spec covers it), so these guard #2274's behaviour as
// built: one minute, doubling, a thirty-minute ceiling, a reset on success.

// The wait after each failed attempt doubles from one minute and stops at the
// ceiling. The earlier pin jumps 31 minutes in one step, so a constant one-minute
// delay survives it; this one retries at EVERY boundary, one second early (no
// attempt) and on time (an attempt). Every attempt of the pending outage also
// carries the same token (cor:agt:070:09: a run's identity is its host and its
// token, so a retry must not mint a second run).
func TestSweepOutageBackoffDoublesToTheCeiling2288(t *testing.T) {
	f := newLoopFixture2060(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.p.now = func() time.Time { return clock }
	f.p.relaunch.Ready = func(context.Context) error { return ErrOrcaDown }
	var tokens []string
	f.p.relaunch.ReportSweep = func(_ context.Context, r SweepReport) error {
		tokens = append(tokens, r.RunID)
		return ErrSweepReportingUnavailable
	}
	stepSweep2056(t, f) // attempt 1: S10 unavailable, the first backoff starts
	waits := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 30 * time.Minute, 30 * time.Minute, 30 * time.Minute}
	for i, wait := range waits {
		before := len(tokens)
		clock = clock.Add(wait - time.Second)
		stepSweep2056(t, f)
		if len(tokens) != before {
			t.Fatalf("wait %d (%v): retried a second early, the delay is shorter than %v (attempts %d -> %d)", i+1, wait, wait, before, len(tokens))
		}
		clock = clock.Add(time.Second)
		stepSweep2056(t, f)
		if len(tokens) != before+1 {
			t.Fatalf("wait %d (%v): not retried when due, the delay is longer than %v (attempts %d -> %d)", i+1, wait, wait, before, len(tokens))
		}
	}
	for i, token := range tokens {
		if token == "" || token != tokens[0] {
			t.Fatalf("attempt %d carried %q, want the pending token %q on every retry: %v", i+1, token, tokens[0], tokens)
		}
	}
}

// A successful recovery report ends the backoff: the delay and the retry time
// are cleared, so nothing carries into whatever S10 reports next. Observed on
// the success step itself, while the daemon is still down (so the readiness
// recovery and sweep-left-APPLY resets cannot be what clears it).
func TestSweepOutageBackoffResetsAfterASuccessfulReport2288(t *testing.T) {
	f := newLoopFixture2060(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.p.now = func() time.Time { return clock }
	f.p.relaunch.Ready = func(context.Context) error { return ErrOrcaDown }
	succeed := false
	attempts := 0
	f.p.relaunch.ReportSweep = func(context.Context, SweepReport) error {
		attempts++
		if succeed {
			return nil
		}
		return ErrSweepReportingUnavailable
	}
	stepSweep2056(t, f) // attempt 1 fails: delay 1m
	clock = clock.Add(time.Minute)
	stepSweep2056(t, f) // attempt 2 fails: delay 2m
	clock = clock.Add(2 * time.Minute)
	stepSweep2056(t, f) // attempt 3 fails: delay 4m
	if attempts != 3 || f.p.outageDelay != 4*time.Minute || !f.p.outageRetryAfter.Equal(clock.Add(4*time.Minute)) {
		t.Fatalf("the backoff did not grow as built: attempts=%d delay=%v retryAfter=%v", attempts, f.p.outageDelay, f.p.outageRetryAfter)
	}
	succeed = true
	clock = clock.Add(4 * time.Minute)
	stepSweep2056(t, f) // attempt 4 is recorded
	if attempts != 4 {
		t.Fatalf("the due retry was not sent: attempts=%d", attempts)
	}
	if f.p.outageDelay != 0 || !f.p.outageRetryAfter.IsZero() {
		t.Fatalf("a recorded report left the backoff behind: delay=%v retryAfter=%v", f.p.outageDelay, f.p.outageRetryAfter)
	}
	if f.p.state.SweepOutage == nil || !f.p.state.SweepOutage.Reported {
		t.Fatalf("the recorded outage was not marked reported: %+v", f.p.state.SweepOutage)
	}
}
