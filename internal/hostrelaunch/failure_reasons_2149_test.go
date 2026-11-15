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
