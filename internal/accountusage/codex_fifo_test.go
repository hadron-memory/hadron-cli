//go:build !windows

package accountusage

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// A rollout swapped for a FIFO between the check and the open must not block
// the collector (BLIND delta, cli#1661).
func TestCodexFifoSwapDoesNotBlock(t *testing.T) {
	home, path := oneRollout(t)
	afterRolloutStat = func() {
		_ = os.Remove(path)
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { afterRolloutStat = func() {} }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s, _ := ReadCodex(codexAcct(home))
		if s.Present {
			t.Error("a FIFO must never yield a reading")
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("opening a swapped-in FIFO blocked the collector")
	}
}
