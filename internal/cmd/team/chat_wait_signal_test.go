//go:build !windows

package team

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// A real SIGINT during the wait is the Cancelled exit, not the Go runtime's
// default signal death (130) — QA found the executable never wired it.
func TestChatWaitSIGINTIsCancelled(t *testing.T) {
	prevMin := chatWaitMinInterval
	t.Cleanup(func() { chatWaitMinInterval = prevMin })
	chatWaitMinInterval = time.Millisecond
	chatWaitTestReturned = 0

	io, _, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: io}
	cmd := waitCmd(f, func(*cobra.Command, []string) error { return nil },
		"--since", "5", "--wait", "1h", "--poll-interval", "1h")
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	}()
	err := cmd.ExecuteContext(context.Background())
	if got := exitcode.FromError(err); got != exitcode.Cancelled {
		t.Fatalf("exit %d (%v), want %d", got, err, exitcode.Cancelled)
	}
}
