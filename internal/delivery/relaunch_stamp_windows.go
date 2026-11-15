//go:build windows

package delivery

import "time"

// The poller's exit relaunch is Linux only (C5's process proofs).
func HasRelaunchStamp(string) bool { return false }
func ConsumeRelaunchStamp(string, string, string, time.Time, time.Duration) bool {
	return false
}
