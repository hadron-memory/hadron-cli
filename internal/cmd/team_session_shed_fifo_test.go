//go:build unix

package cmd

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A --transcript naming an unwritten FIFO must degrade to "unavailable", not
// block the shed ahead of the checkpoint and the claim.
func TestSessionShedTranscriptFIFODoesNotBlock(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	fifo := filepath.Join(t.TempDir(), "t.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no mkfifo:", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = runShed(t, url, "--transcript", fifo)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// release the blocked open so the goroutine can exit
		if fh, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = syscall.Close(fh)
		}
		t.Fatal("shed blocked on a FIFO transcript")
	}
	if rig.count("CreateNode") != 1 || rig.count("CreateResumeClaim") != 1 {
		t.Errorf("checkpoint and claim must still happen: %v", rig.calls)
	}
}
