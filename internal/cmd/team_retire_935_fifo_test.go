//go:build unix

package cmd

import (
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// cli#935 — the stubs read no router config at all, not merely "use nothing
// they read". A parse with its error ignored leaves no file-system evidence,
// so the config the stub is pointed at (by --config and by both environment
// variables) is a FIFO. A reader's open of a FIFO blocks until a writer opens
// it, and a non-blocking writer open fails with ENXIO while no reader is
// waiting, so the background opener below sees exactly the opens the command
// makes, and unblocks them so a regressed stub fails rather than hangs.
func TestRetiredStubsOpenNoRouterConfig(t *testing.T) {
	for _, args := range [][]string{
		{"team", "attention", "--since", sinceSentinel},
		{"team", "attention", "switchover", "preview"},
		{"team", "attention", "switchover", "apply", "--proof", proofSentinel, "--yes"},
		{"team", "router", "poll", "--config", ""},
		{"team", "router", "loop", "--config", ""},
		{"team", "router", "status", "--config", ""},
		{"team", "router", "status"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			f, _, _ := retiredFactory(t)
			fifo := filepath.Join(t.TempDir(), "router.json")
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HADRON_ROUTER_CONFIG", fifo)
			t.Setenv("ORCA_ROUTER_CONFIG", fifo)
			argv := append([]string{}, args...)
			for i, a := range argv {
				if a == "--config" {
					argv[i+1] = fifo
				}
			}

			var opened atomic.Int32
			done := make(chan struct{})
			watched := make(chan struct{})
			go func() {
				defer close(watched)
				for {
					fd, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
					if err == nil {
						opened.Add(1)
						_ = syscall.Close(fd) // EOF for the reader: it fails, not hangs
					} else if !errors.Is(err, syscall.ENXIO) {
						t.Errorf("probe open: %v", err)
						return
					}
					select {
					case <-done:
						return
					case <-time.After(time.Millisecond):
					}
				}
			}()

			root := NewRootCmd(f)
			root.SetArgs(argv)
			err := root.Execute()
			close(done)
			<-watched
			if n := opened.Load(); n != 0 {
				t.Errorf("retired command opened the router config %d time(s)", n)
			}
			if err == nil || !strings.Contains(err.Error(), "retired (hadron-server#2027)") {
				t.Errorf("err = %v, want the retirement error", err)
			}
		})
	}
}
