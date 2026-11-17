//go:build !windows

package team

import (
	"fmt"
	"os"
	"syscall"
)

// secureCacheDir makes the plain directory's cache owner-only through a
// handle opened WITHOUT following a symlink, and checks that handle is still
// the directory at the path. A symlink swapped in at any moment is refused,
// never chmodded: the mode change lands on exactly the directory opened
// (Jibril's BLIND delta on #2229).
func secureCacheDir(dir string) error {
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0) // #nosec G304 — the cache directory under the working directory
	if err != nil {
		return fmt.Errorf("the session binding cache %s must be a real directory (not a symbolic link): %w", dir, err)
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if now, lerr := os.Lstat(dir); lerr != nil || !os.SameFile(opened, now) {
		return fmt.Errorf("the session binding cache %s changed while it was being secured: nothing was changed", dir)
	}
	return f.Chmod(0o700)
}
