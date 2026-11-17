//go:build unix

package delivery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// cli#2238, the poller's side: LockBinding creates the same lock file, and
// under umask 0277 or 0777 the file it created could not be opened read-write
// by the next lock (the bind's, or the poller's own next stop). The mode is
// exact whatever the umask; the process umask is never touched in production,
// so the probe sets it in a subprocess.
func TestLockBindingFileModeIgnoresTheUmask2238(t *testing.T) {
	if mask := os.Getenv("HADRON_TEST_UMASK_PROBE_2238"); mask != "" {
		var m int
		if _, err := fmt.Sscanf(mask, "%o", &m); err != nil {
			t.Fatal(err)
		}
		syscall.Umask(m)
		d := os.Getenv("HADRON_TEST_UMASK_DIR")
		// A second opener arriving while the lock is being created must never
		// see it in a mode it cannot open (the create-then-chmod window).
		lockCreatedHook = func(path string) {
			f, err := os.OpenFile(path, os.O_RDWR, 0)
			if err == nil {
				_ = f.Close()
				return
			}
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("a concurrent open of the lock being created failed: %v", err)
			}
		}
		defer func() { lockCreatedHook = nil }()
		for i := range 2 {
			release, err := LockBinding(d, time.Second)
			if err != nil {
				t.Fatalf("lock %d: %v", i+1, err)
			}
			release()
		}
		return
	}
	for _, mask := range []string{"277", "777", "022"} {
		t.Run(mask, func(t *testing.T) {
			d := gitTree(t)
			cmd := exec.Command(os.Args[0], "-test.run=^TestLockBindingFileModeIgnoresTheUmask2238$")
			cmd.Env = append(os.Environ(), "HADRON_TEST_UMASK_PROBE_2238="+mask, "HADRON_TEST_UMASK_DIR="+d)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("two locks under umask %s: %v\n%s", mask, err, out)
			}
			st, err := os.Stat(filepath.Join(d, ".git", bindingLockFileName))
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != 0o600 {
				t.Fatalf("lock file mode = %v, want 0600 whatever the umask", st.Mode().Perm())
			}
		})
	}
}

// Julius's P3: the poller's own create path leaves an existing lock alone.
func TestLockBindingExistingModeIsKept2238(t *testing.T) {
	d := gitTree(t)
	lock := filepath.Join(d, ".git", bindingLockFileName)
	if err := os.WriteFile(lock, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lock, 0o640); err != nil {
		t.Fatal(err)
	}
	release, err := LockBinding(d, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if st, err := os.Stat(lock); err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("an existing lock's mode changed: %v %v", st, err)
	}
}
