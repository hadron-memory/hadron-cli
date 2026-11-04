//go:build windows

package accountusage

import "os"

// Windows has no O_NOFOLLOW; the Lstat and fstat checks in ReadCodex apply.
func openRollout(path string) (*os.File, error) { return os.Open(path) }
