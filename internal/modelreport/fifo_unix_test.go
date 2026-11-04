//go:build !windows

package modelreport

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// A regular file swapped for a FIFO between the Lstat and the open must not
// block the hook: the open is non-blocking and the identity check refuses it.
func TestFIFOSwapDoesNotBlock(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", claudeLine("m1", ""))
	afterCanonical = func() {
		_ = os.Remove(p)
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { afterCanonical = nil })
	done := make(chan struct{})
	var got string
	var err error
	go func() { got, err = Read(opts(root, p)); close(done) }()
	select {
	case <-done:
		wantUnavailable(t, got, err)
	case <-time.After(3 * time.Second):
		t.Fatal("open on a FIFO blocked")
	}
}
