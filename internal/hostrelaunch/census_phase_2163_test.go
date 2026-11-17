package hostrelaunch

import (
	"context"
	"errors"
	"testing"
)

func TestCensusUnavailableBeforeLaunchStaysLocal2163(t *testing.T) {
	f := newFixture(t)
	f.run(t, 1)
	f.store.onSave = func(r Record) {
		if len(r.Attempts) > 0 {
			f.probeErr = ErrCensusUnavailable
		}
	}
	f.runner.Sweep = 2
	out, err := f.runner.Execute(context.Background(), f.target)
	if !errors.Is(err, ErrCensusUnavailable) || out.Status != Skipped || out.Reason != "RELAUNCH_CENSUS_UNAVAILABLE" || out.FailureReason != "" {
		t.Fatalf("pre-launch census failure acquired a post-launch code: %+v err=%v", out, err)
	}
	if out.Attempt != 1 || len(f.store.rows[f.target.WorkerID].Attempts) != 1 || f.starts != 0 || f.rolledBack != 0 {
		t.Fatalf("pre-launch exhaustion changed charge or started/stopped an agent: %+v starts=%d rollback=%d", out, f.starts, f.rolledBack)
	}
}

func TestCensusUnavailableAfterLaunchIsVerifyFailed2163(t *testing.T) {
	f := newFixture(t)
	f.run(t, 1)
	f.onStart = func() { f.probeErr = ErrCensusUnavailable }
	f.runner.Sweep = 2
	out, err := f.runner.Execute(context.Background(), f.target)
	if !errors.Is(err, ErrCensusUnavailable) || out.Status != Failed || out.Reason != VerifyFailed || out.FailureReason != "VERIFY_FAILED" || out.LocalCause == "" {
		t.Fatalf("post-launch census failure lost its phase: %+v err=%v", out, err)
	}
	if out.Attempt != 1 || len(f.store.rows[f.target.WorkerID].Attempts) != 1 || out.Reference != "own-terminal" || f.starts != 1 || f.rolledBack != 1 {
		t.Fatalf("post-launch exhaustion changed charge or own-agent rollback: %+v starts=%d rollback=%d", out, f.starts, f.rolledBack)
	}
}
