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
