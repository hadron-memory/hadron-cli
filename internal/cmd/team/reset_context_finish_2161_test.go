package team

import (
	"errors"
	"strings"
	"testing"
)

// Jibril's delta P2s on #2167: the receipt is written while the agent still
// reads it, and only then is the pinned agent signalled. A receipt that could
// not be written signals nothing. A signal that fails after the receipt says
// so on stderr. The pinned handle is closed on every path.

type recordedAgent struct {
	steps *[]string
	fail  bool
}

func (a recordedAgent) signal() error {
	*a.steps = append(*a.steps, "signal")
	if a.fail {
		return errors.New("ESRCH")
	}
	return nil
}
func (a recordedAgent) close() { *a.steps = append(*a.steps, "close") }

func TestResetContextWritesTheReceiptBeforeTheSignal2161(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var steps []string
		var errOut strings.Builder
		finishResetContext(&errOut, recordedAgent{steps: &steps, fail: fail}, func() error { steps = append(steps, "receipt"); return nil })
		if strings.Join(steps, ",") != "receipt,signal,close" {
			t.Fatalf("fail=%v: steps %v", fail, steps)
		}
		if said := strings.Contains(errOut.String(), "exit it now"); said != fail {
			t.Fatalf("fail=%v: stderr %q", fail, errOut.String())
		}
	}
	var steps []string
	finishResetContext(&strings.Builder{}, nil, func() error { steps = append(steps, "receipt"); return nil })
	if strings.Join(steps, ",") != "receipt" {
		t.Fatalf("no agent: steps %v", steps)
	}
}

func TestResetContextSignalsNothingWhenTheReceiptFails2161(t *testing.T) {
	var steps []string
	var errOut strings.Builder
	finishResetContext(&errOut, recordedAgent{steps: &steps}, func() error { steps = append(steps, "receipt"); return errors.New("EPIPE") })
	if strings.Join(steps, ",") != "receipt,close" {
		t.Fatalf("a failed receipt still signalled: steps %v", steps)
	}
	if !strings.Contains(errOut.String(), "nothing was signalled") {
		t.Fatalf("stderr %q", errOut.String())
	}
}
