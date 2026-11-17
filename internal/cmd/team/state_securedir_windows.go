//go:build windows

package team

import (
	"fmt"
	"os"
)

// secureCacheDir on Windows only refuses a symbolic link or junction: file
// modes are not owner-only bits there, so there is nothing to tighten.
func secureCacheDir(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("the session binding cache %s must be a real directory (not a symbolic link)", dir)
	}
	return nil
}
