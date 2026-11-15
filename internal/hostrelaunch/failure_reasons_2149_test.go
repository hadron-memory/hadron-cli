package hostrelaunch

import (
	"context"
	"errors"
	"testing"
)

func TestFailureReasonsFollowObservedPhase2149(t *testing.T) {
	cases := []struct {
		name, want string
		arrange    func(*fixture)
	}{
		{"typed prelaunch setup", "SETUP_FAILED", func(f *fixture) { f.startErr = &LaunchError{Code: "SETUP_FAILED", Err: errors.New("disk unavailable")} }},
		{"unknown launcher failure remains unknown", "", func(f *fixture) { f.startErr = errors.New("lost transport reply") }},
		{"unknown process proof", "VERIFY_FAILED", func(f *fixture) { f.post.Proven = false }},
		{"foreign process proof", "VERIFY_FAILED", func(f *fixture) { f.post.PIDs = []int{999} }},
		{"verifier cannot prove", "VERIFY_FAILED", func(f *fixture) { f.verifyErr = errors.New("pid proof unavailable") }},
		{"cancelled verifier", "", func(f *fixture) { f.verifyErr = context.Canceled }},
		{"actual proof timeout", FailureTimeout, func(f *fixture) { f.verifyErr = context.DeadlineExceeded }},
		{"server state change is not process failure", "", func(f *fixture) { f.onStart = func() { f.desired.State = "PAUSED" } }},
		{"actual create refusal", FailureTerminalCreateRefused, func(f *fixture) {
			f.startErr = &LaunchError{Code: FailureTerminalCreateRefused, Err: errors.New("refused")}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			f.run(t, 1)
			test.arrange(f)
			f.runner.Sweep = 2
			out, err := f.runner.Execute(context.Background(), f.target)
			if err == nil || out.Status != Failed {
				t.Fatalf("want failed execution, got %+v %v", out, err)
			}
			if out.FailureReason != test.want {
				t.Fatalf("reason=%q want%q; result=%+v", out.FailureReason, test.want, out)
			}
			if out.LocalCause == "" || len(f.store.rows[f.target.WorkerID].Attempts) != 1 {
				t.Fatal("lost local cause or charged attempt")
			}
		})
	}
}

func TestRejectedNewReasonNoticeKeepsLocalFailure2149(t *testing.T) {
	f := newFixture(t)
	f.run(t, 1)
	f.startErr = &LaunchError{Code: "SETUP_FAILED", Err: errors.New("original disk failure")}
	f.runner.Notice = func(context.Context, Target, Result) error { return errors.New("schema versions out of sync") }
	f.runner.Sweep = 2
	out, err := f.runner.Execute(context.Background(), f.target)
	if err == nil || out.Status != Failed || out.LocalCause != "original disk failure" || !out.NoticeFailed || out.NoticeCause != "schema versions out of sync" || out.FailureReason != "SETUP_FAILED" {
		t.Fatalf("lost local failure: %+v %v", out, err)
	}
	if f.store.rows[f.target.WorkerID].Notice != "" || len(f.store.rows[f.target.WorkerID].Attempts) != 1 || f.starts != 1 {
		t.Fatal("unconfirmed notice recorded or attempt changed")
	}
}

func TestFinalGuardCensusErrorIsNotProcessVerification2149(t *testing.T) {
	f := newFixture(t)
	f.run(t, 1)
	f.store.onSave = func(r Record) {
		if len(r.Attempts) > 0 {
			f.probeErr = errors.New("census identity unavailable")
		}
	}
	f.runner.Sweep = 2
	out, err := f.runner.Execute(context.Background(), f.target)
	if err == nil || out.Status != Skipped || out.Reason != Unproven || out.FailureReason != "" || f.starts != 0 {
		t.Fatalf("prelaunch observation mislabeled: %+v %v starts=%d", out, err, f.starts)
	}
}
