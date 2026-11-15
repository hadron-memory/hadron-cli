//go:build windows

package delivery

// noFollow has no open flag on Windows; the Lstat and regular-file checks in
// LockBinding carry the refusal there.
const noFollow = 0
