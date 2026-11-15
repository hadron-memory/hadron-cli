package hostrelaunch

import (
	"context"
	"errors"
	"testing"
)

func TestRepairIOStartsAfterAdmissionRelease2120(t *testing.T) {
	f := newFixture(t)
	f.run(t, 1)
	held := false
	f.runner.Lock = func(context.Context, Target) (func(), error) {
		held = true
		return func() { held = false; f.unlocks++ }, nil
	}
	f.runner.Notice = func(context.Context, Target, Result) error {
		if held {
			return errors.New("repair I/O retained admission/binding lock")
		}
		return nil
	}
	out := f.run(t, 2)
	if out.Status != Applied || out.NoticeFailed || held || f.unlocks != 2 {
		t.Fatalf("result=%+v held=%v unlocks=%d", out, held, f.unlocks)
	}
}
