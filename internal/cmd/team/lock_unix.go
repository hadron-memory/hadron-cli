//go:build unix

// The `unix` constraint, not `!windows`: the latter also selects plan9 and
// js/wasm, where syscall.Flock does not exist (PR #519 review, @copilot).

package team

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock, blocking until it is available.
//
// flock(2) rather than an O_EXCL sidecar, and the difference is the whole
// reason a lock is acceptable here at all: the kernel releases a flock when
// the descriptor closes — INCLUDING when the process dies. So there is no
// stale lock to recover from, no age heuristic, and no timeout policy. Those
// were the objections that steered #499 toward compare-and-swap, and they do
// not apply to this primitive.
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
