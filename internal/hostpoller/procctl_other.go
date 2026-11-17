//go:build !linux

package hostpoller

import "time"

// LinuxProcessControl on another platform refuses everything: without a
// pidfd a signal could reach a reused pid, so the handler refuses before any
// claim (#1043).
type LinuxProcessControl struct{}

func (LinuxProcessControl) Observe(int) (ProcessHandle, error) { return nil, ErrUnsupportedPlatform }

// Now has no boot clock to read here, so it answers an error: nothing could
// be signalled even if a handle existed.
func (LinuxProcessControl) Now() (time.Duration, error) { return 0, ErrUnsupportedPlatform }
