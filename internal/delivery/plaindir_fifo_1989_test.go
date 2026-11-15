//go:build unix

package delivery

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO at a worker-writable path must not block the deliverer (Rook P3-2).
func TestPlainDirFIFOAtTheRecordOrBindingDoesNotBlock(t *testing.T) {
	for _, name := range []string{"terminal.json", "hadron-team-session.json"} {
		t.Run(name, func(t *testing.T) {
			p := newPlainHarness(t)
			path := filepath.Join(p.plain, ".hadron-team", name)
			_ = os.Remove(path)
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Skip("no mkfifo")
			}
			done := make(chan Outcome, 1)
			go func() { done <- p.deliver(p.batch()) }()
			select {
			case o := <-done:
				if o.Kind != Held {
					t.Fatalf("outcome %+v, want a hold", o)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("deliver blocked on a FIFO")
			}
		})
	}
}
