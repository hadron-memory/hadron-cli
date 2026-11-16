package team

import (
	"errors"
	"strings"
	"testing"
)

// Jibril's delta P2 on #2167: the receipt is written while the agent still
// reads it, and only then is the pinned agent signalled. A signal that fails
// after the receipt says so on stderr.

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
		finishResetContext(&errOut, recordedAgent{steps: &steps, fail: fail}, func() { steps = append(steps, "receipt") })
		if strings.Join(steps, ",") != "receipt,signal,close" {
			t.Fatalf("fail=%v: steps %v", fail, steps)
		}
		if said := strings.Contains(errOut.String(), "exit it now"); said != fail {
			t.Fatalf("fail=%v: stderr %q", fail, errOut.String())
		}
	}
	var steps []string
	finishResetContext(&strings.Builder{}, nil, func() { steps = append(steps, "receipt") })
	if strings.Join(steps, ",") != "receipt" {
		t.Fatalf("no agent: steps %v", steps)
	}
}
