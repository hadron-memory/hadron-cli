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
	for _, sample := range []struct {
		name      string
		counts    []int
		pre, post int
	}{
		{"first", []int{9, 7, 2, 1}, 9, 9},
		{"second", []int{3, 9, 2, 1}, 9, 9},
		{"third", []int{3, 7, 9, 1}, 9, 9},
		{"post-launch", []int{3, 7, 2, 11}, 7, 11},
	} {
		t.Run(sample.name, func(t *testing.T) {
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
					counts := sample.counts
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
					if (err != nil) != (mode != "success") {
						t.Fatalf("path error: %v", err)
					}
					if out.Status != wantStatus {
						t.Fatalf("status=%s want %s", out.Status, wantStatus)
					}
					if calls != wantCalls {
						t.Fatalf("probe calls=%d want %d", calls, wantCalls)
					}
					if f.starts != 1 {
						t.Fatalf("launches=%d want 1", f.starts)
					}
					if f.rolledBack != wantRollback {
						t.Fatalf("rollbacks=%d want %d", f.rolledBack, wantRollback)
					}
					wantCount := sample.post
					if mode == "setup failure" || mode == "proof error" {
						wantCount = sample.pre
					}
					if out.ForeignUIDSkipped != wantCount || logs != 1 || logged.ForeignUIDSkipped != wantCount || logged.Status != wantStatus {
						t.Fatalf("maximum lost or summed: out=%+v logged=%+v logs=%d", out, logged, logs)
					}
					if out.Attempt != 1 || len(f.store.rows[f.target.WorkerID].Attempts) != 1 {
						t.Fatalf("charge changed: %+v", out)
					}
				})
			}
		})
	}
}

func TestPrelaunchPartialForeignCountRefusesImmediately2197(t *testing.T) {
	for failAt := 0; failAt < 3; failAt++ {
		t.Run([]string{"first", "under-lock", "before-launch"}[failAt], func(t *testing.T) {
			f := newFixture(t)
			first := f.run(t, 1)
			if first.Reason != Pending || f.starts != 0 || len(f.store.rows[f.target.WorkerID].Attempts) != 0 {
				t.Fatalf("first sweep did not establish uncharged absence: %+v starts=%d", first, f.starts)
			}
			f.runner.Sweep = 2
			calls := 0
			failure := errors.New("prelaunch partial census")
			f.runner.Probe = func(context.Context, Target) (Observation, error) {
				at := calls
				calls++
				if at == failAt {
					return Observation{Proven: true, ForeignUIDSkipped: 99}, failure
				}
				return Observation{Proven: true, ForeignUIDSkipped: 3}, nil
			}
			var logged Result
			logs := 0
			f.runner.Log = func(_ Target, r Result) { logs++; logged = r }
			out, err := f.runner.Execute(context.Background(), f.target)
			if !errors.Is(err, failure) {
				t.Fatalf("prelaunch error lost: %v", err)
			}
			if calls != failAt+1 {
				t.Fatalf("probe continued after refusal: calls=%d", calls)
			}
			if f.starts != 0 || f.launched {
				t.Fatal("launched after prelaunch refusal")
			}
			wantAttempt := 0
			// The final pre-launch probe follows the durable attempt claim.
			if failAt == 2 {
				wantAttempt = 1
			}
			if out.Attempt != wantAttempt || len(f.store.rows[f.target.WorkerID].Attempts) != wantAttempt {
				t.Fatalf("charged refusal: %+v", out)
			}
			want := 3
			if failAt == 0 {
				want = 0
			}
			if out.ForeignUIDSkipped != want || logged.ForeignUIDSkipped != want || logs != 1 {
				t.Fatalf("partial count escaped: out=%+v logged=%+v logs=%d", out, logged, logs)
			}
		})
	}
}
