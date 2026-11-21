//go:build !windows

package cmd

import (
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// QA F1 on cli#2738: Ctrl-C during `secret change get --wait` must exit 6 (the
// documented Cancelled exit), not kill the process. A real SIGINT is delivered to
// this process once the first read has been answered, so the wait is in flight.
func TestChangeGetWaitInterruptExitsCancelled2738(t *testing.T) {
	read := make(chan struct{}, 1)
	srv, calls := fake2738(t, func(op string, _ int) (int, string) {
		select {
		case read <- struct{}{}:
		default:
		}
		return 200, changeRow2738("open")
	})
	go func() {
		<-read
		time.Sleep(100 * time.Millisecond)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	}()
	done := make(chan int, 1)
	var stderr string
	go func() {
		_, e, code := run2738(t, srv.URL, "change", "get", req2738, "--wait", "--wait-timeout", "1m")
		stderr = e
		done <- code
	}()
	select {
	case code := <-done:
		if code != exitcode.Cancelled {
			t.Fatalf("exit = %d, want 6\n%s", code, stderr)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the wait did not end on an interrupt")
	}
	if !strings.Contains(stderr, "nothing is withdrawn") {
		t.Errorf("must say nothing was withdrawn: %q", stderr)
	}
	for _, op := range ops2738(calls) {
		if op != "SecretChangeRequest" {
			t.Errorf("an interrupted wait only reads, saw %s", op)
		}
	}
}
