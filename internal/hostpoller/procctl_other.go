//go:build !linux

package hostpoller

import "time"

// LinuxProcessControl on another platform refuses everything: without a
// pidfd a signal could reach a reused pid, so the handler refuses before any
// claim (#1043).
type LinuxProcessControl struct{}

func (LinuxProcessControl) Observe(int) (ProcessHandle, error) { return nil, ErrUnsupportedPlatform }

// Now answers a time past any lease, so nothing could be signalled even if a
// handle existed.
func (LinuxProcessControl) Now() time.Duration { return time.Duration(1<<62 - 1) }
