//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || windows)

package accountusage

import "os"

// Targets without O_NOFOLLOW/O_NONBLOCK semantics we rely on (Plan 9, Solaris, ...): the Lstat and fstat checks in ReadCodex apply.
func openRollout(_ string, path string) (*os.File, error) { return os.Open(path) }
