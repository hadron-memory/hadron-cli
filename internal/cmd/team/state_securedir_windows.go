//go:build windows

package team

import (
	"fmt"
	"os"
)

// secureCacheDir on Windows refuses a symbolic link or junction and returns an
// os.Root on the directory, proved the same directory by file identity; file
// modes are not owner-only bits there, so there is nothing to tighten.
func secureCacheDir(dir string) (*os.Root, error) {
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return nil, fmt.Errorf("the session binding cache %s must be a real directory (not a symbolic link)", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if rs, serr := root.Stat("."); serr != nil || !os.SameFile(st, rs) {
		_ = root.Close()
		return nil, fmt.Errorf("the session binding cache %s changed while it was being secured: nothing was changed", dir)
	}
	return root, nil
}

// mkdirCache creates the cache directory; Windows has no umask.
func mkdirCache(dir string) error { return os.MkdirAll(dir, 0o700) }
