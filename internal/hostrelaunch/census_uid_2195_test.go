package hostrelaunch

import (
	"context"
	"errors"
	"testing"
)

func TestForeignUIDSkipCountStaysLocal2195(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "partial"}[fail], func(t *testing.T) {
			f := newFixture(t)
			f.runner.Probe = func(context.Context, Target) (Observation, error) {
				o := Observation{Proven: true, PIDs: []int{123}, ForeignUIDSkipped: 7}
				if fail {
					return o, errors.New("incomplete census")
				}
				return o, nil
			}
			out, _ := f.runner.Execute(context.Background(), f.target)
			want := 7
			if fail {
				want = 0
			}
			if out.ForeignUIDSkipped != want || f.starts != 0 || out.Attempt != 0 || out.FailureReason != "" {
				t.Fatalf("count changed authority: %+v starts=%d", out, f.starts)
			}
		})
	}
}

func TestForeignUIDMaximumAcrossLaunchPaths2196(t *testing.T) {
	for _, mode := range []string{"success", "setup failure", "proof error", "verify failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.run(t, 1) // establish absence without consuming this execution's samples
			f.runner.Sweep = 2
			if mode == "setup failure" {
				f.startErr = errors.New("setup failed")
			}
			if mode == "verify failure" {
				f.verifyErr = errors.New("pinned identity verification failed")
			}
			counts := []int{3, 7, 2, 1}
			calls := 0
			f.runner.Probe = func(context.Context, Target) (Observation, error) {
				if calls >= len(counts) {
					t.Fatal("unexpected extra probe")
				}
				count := counts[calls]
				calls++
				o := Observation{Proven: true, ForeignUIDSkipped: count}
				if f.launched {
					o.PIDs = []int{42}
					if mode == "proof error" {
						// Even a larger partial count from a failed census is not evidence.
						o.ForeignUIDSkipped = 99
						return o, errors.New("post-launch census incomplete")
					}
				}
				return o, nil
			}
			var logged Result
			logs := 0
			f.runner.Log = func(_ Target, r Result) { logged = r; logs++ }
			out, err := f.runner.Execute(context.Background(), f.target)
			wantCalls, wantStatus, wantRollback := 4, Applied, 0
			if mode == "setup failure" {
				wantCalls, wantStatus = 3, Failed
			}
			if mode == "proof error" || mode == "verify failure" {
				wantStatus, wantRollback = Failed, 1
			}
			if (err != nil) != (mode != "success") || out.Status != wantStatus || calls != wantCalls || f.starts != 1 || f.rolledBack != wantRollback {
				t.Fatalf("path not exercised: out=%+v err=%v calls=%d starts=%d rollback=%d", out, err, calls, f.starts, f.rolledBack)
			}
			if out.ForeignUIDSkipped != 7 || logs != 1 || logged.ForeignUIDSkipped != 7 || logged.Status != wantStatus {
				t.Fatalf("maximum lost or summed: out=%+v logged=%+v logs=%d", out, logged, logs)
			}
			if out.Attempt != 1 || len(f.store.rows[f.target.WorkerID].Attempts) != 1 {
				t.Fatalf("charge changed: %+v", out)
			}
		})
	}
}
