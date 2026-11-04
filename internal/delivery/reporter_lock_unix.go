//go:build unix

package delivery

import (
	"errors"
	"os"
	"syscall"
)

// tryLockReporter takes the reporter's exclusive lock without waiting.
// flock(2): the kernel releases it when the descriptor closes, including on
// process death, so a crashed reporter leaves no stale lock (hadron-cli#1629).
func tryLockReporter(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
