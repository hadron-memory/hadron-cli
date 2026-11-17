//go:build linux

package hostpoller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func captureEvents2156(f *loopFixture2060) *[]Event {
	var got []Event
	f.p.emit = func(e Event) { got = append(got, e) }
	return &got
}

// F7: a confirmed outage episode must not be carried into a later outage
// that starts after the sweep left APPLY (OFF or OBSERVE).
func TestSweepOutageEpisodeEndsWhenSweepLeavesApply2156(t *testing.T) {
	for _, mode := range []string{"OFF", "OBSERVE"} {
		t.Run(mode, func(t *testing.T) {
			f := newLoopFixture2060(t)
			f.p.relaunch.Ready = func(context.Context) error { return ErrOrcaDown }
			var reports []SweepReport
			f.p.relaunch.ReportSweep = func(_ context.Context, r SweepReport) error { reports = append(reports, r); return nil }
			stepSweep2056(t, f)
			if len(reports) != 1 || f.p.state.SweepOutage == nil || !f.p.state.SweepOutage.Reported {
				t.Fatalf("first outage must be announced and confirmed: %+v", reports)
			}
			first := reports[0].RunID
			f.source.snapshot.SweepMode = mode
			stepSweep2056(t, f)
			f.source.snapshot.SweepMode = "APPLY"
			stepSweep2056(t, f)
			if len(reports) != 2 || reports[1].RunID == first || reports[1].Failed != 1 {
				t.Fatalf("a new outage after %s must be a new announced episode: %+v", mode, reports)
			}
		})
	}
}

// F7: readiness recovery ends the episode; the next outage is new.
func TestSweepOutageEpisodeEndsOnReadinessRecovery2156(t *testing.T) {
	f := newLoopFixture2060(t)
	down := true
	f.p.relaunch.Ready = func(context.Context) error {
		if down {
			return ErrOrcaDown
		}
		return nil
	}
	var reports []SweepReport
	f.p.relaunch.ReportSweep = func(_ context.Context, r SweepReport) error { reports = append(reports, r); return nil }
	stepSweep2056(t, f)
	down = false
	f.source.snapshot.Workers[0].Reason = "PAUSED" // recover without launching
	stepSweep2056(t, f)
	if f.p.state.SweepOutage != nil {
		t.Fatal("readiness recovery must end the episode")
	}
	down = true
	f.source.snapshot.Workers[0].Reason = "HOLDS_TASK"
	stepSweep2056(t, f)
	if len(reports) != 2 || reports[0].RunID == reports[1].RunID {
		t.Fatalf("new outage must carry a new token: %+v", reports)
	}
}

// F8: a state-save failure drops the report but never silently.
func TestSweepOutageStateSaveFailureIsCoded2156(t *testing.T) {
	f := newLoopFixture2060(t)
	f.p.relaunch.Ready = func(context.Context) error { return ErrOrcaDown }
	reports := 0
	f.p.relaunch.ReportSweep = func(context.Context, SweepReport) error { reports++; return nil }
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.p.state.path = filepath.Join(blocker, "state.json") // parent is a file: save fails
	events := captureEvents2156(f)
	stepSweep2056(t, f)
	if reports != 0 {
		t.Fatal("an unrecorded token must not be reported")
	}
	for _, e := range *events {
		if e.Kind == "sweep-record-failed" && e.Detail == "STATE_SAVE_FAILED" {
			return
		}
	}
	t.Fatalf("state-save failure was dropped silently: %+v", *events)
}

// F9: a pending outage whose report finds S10 unavailable backs off instead
// of retrying on every poll.
func TestSweepOutageBacksOffWhileS10Unavailable2156(t *testing.T) {
	f := newLoopFixture2060(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.p.now = func() time.Time { return clock }
	f.p.relaunch.Ready = func(context.Context) error { return ErrOrcaDown }
	attempts := 0
	f.p.relaunch.ReportSweep = func(context.Context, SweepReport) error { attempts++; return ErrSweepReportingUnavailable }
	for range 5 {
		stepSweep2056(t, f)
	}
	if attempts != 1 {
		t.Fatalf("repeated S10_UNAVAILABLE must back off within the window: attempts=%d", attempts)
	}
	clock = clock.Add(31 * time.Minute)
	stepSweep2056(t, f)
	if attempts != 2 {
		t.Fatalf("must retry after the backoff window: attempts=%d", attempts)
	}
}

// F9 (LOCAL P2, AT0.7887.2): readiness recovery ends the episode, so the
// backoff it left pending must not suppress the next outage's first report.
func TestSweepOutageBackoffClearsOnReadinessRecovery2156(t *testing.T) {
	f := newLoopFixture2060(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.p.now = func() time.Time { return clock }
	down := true
	f.p.relaunch.Ready = func(context.Context) error {
		if down {
			return ErrOrcaDown
		}
		return nil
	}
	attempts := 0
	f.p.relaunch.ReportSweep = func(context.Context, SweepReport) error { attempts++; return ErrSweepReportingUnavailable }
	stepSweep2056(t, f) // attempt 1, S10 unavailable: backoff pending
	down = false
	f.source.snapshot.Workers[0].Reason = "PAUSED" // quiet recovery
	clock = clock.Add(time.Second)
	stepSweep2056(t, f)
	down = true
	f.source.snapshot.Workers[0].Reason = "HOLDS_TASK"
	clock = clock.Add(time.Second)
	stepSweep2056(t, f)
	if attempts != 2 {
		t.Fatalf("a new outage's first report must not wait on the old episode's backoff: attempts=%d", attempts)
	}
}
