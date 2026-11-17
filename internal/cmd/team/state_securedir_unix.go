//go:build !windows

package team

import (
	"fmt"
	"os"
	"syscall"
)

// secureCacheDir makes the plain directory's cache owner-only through a
// handle opened WITHOUT following a symlink, and returns an os.Root on that
// same directory (proved by file identity) for every later access. A symlink
// swapped in at any moment is refused, never chmodded or followed: the mode
// change lands on exactly the directory opened, and the root is that
// directory whatever happens to the path afterwards (Jibril's BLIND P2 and
// delta on #2229).
func secureCacheDir(dir string) (*os.Root, error) {
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0) // #nosec G304 — the cache directory under the working directory
	if err != nil {
		return nil, fmt.Errorf("the session binding cache %s must be a real directory (not a symbolic link): %w", dir, err)
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if rs, serr := root.Stat("."); serr != nil || !os.SameFile(opened, rs) {
		_ = root.Close()
		return nil, fmt.Errorf("the session binding cache %s changed while it was being secured: nothing was changed", dir)
	}
	return root, nil
}

// mkdirCache creates the cache directory owner-only whatever the umask: the
// mask is narrowed to group and other bits for the call, so a restrictive
// umask cannot leave the owner without access to the directory it must open.
func mkdirCache(dir string) error {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	return os.MkdirAll(dir, 0o700)
}
