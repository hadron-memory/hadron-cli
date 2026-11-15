//go:build unix

package delivery

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Blind delta P3: a directory swapped for a link AFTER its descriptor is held
// does not redirect the lock: the file is created through the descriptor, in
// the directory that was opened, never in the link's target.
func TestLockBindingAnchorsTheFileToTheOpenedDirectory(t *testing.T) {
	d := gitTree(t)
	outside := t.TempDir()
	afterDirOpenHook = func(dir string) {
		if err := os.Rename(dir, dir+".moved"); err != nil {
			t.Error(err)
			return
		}
		if err := os.Symlink(outside, dir); err != nil {
			t.Error(err)
		}
	}
	defer func() { afterDirOpenHook = nil }()
	release, err := LockBinding(d, time.Second)
	if err != nil {
		t.Fatalf("the lock must still be taken in the directory that was opened: %v", err)
	}
	release()
	if _, err := os.Stat(filepath.Join(outside, bindingLockFileName)); err == nil {
		t.Fatal("the lock was created in the link's target")
	}
	if _, err := os.Stat(filepath.Join(d, ".git.moved", bindingLockFileName)); err != nil {
		t.Fatalf("the lock must be in the directory that was held open: %v", err)
	}
}
