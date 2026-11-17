//go:build unix

package delivery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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

// Julius's P3 and P2 on the delta, for the poller's create path: a competing
// creator that loses the publish reopens the winner's file (same inode, no
// leftover), and a filesystem without hard links still gets a lock.
func TestLockBindingCompetingCreators2238(t *testing.T) {
	d := gitTree(t)
	dir := filepath.Join(d, ".git")
	paused, resume := make(chan struct{}), make(chan struct{})
	var paused1 atomic.Bool
	lockCreatedHook = func(string) {
		if paused1.CompareAndSwap(false, true) { // only the first creator pauses
			close(paused)
			<-resume
		}
	}
	defer func() { lockCreatedHook = nil }()
	type result struct {
		f   *os.File
		err error
	}
	first := make(chan result, 1)
	go func() { f, err := openLockFile(dir, bindingLockFileName, nil); first <- result{f, err} }()
	<-paused
	second, err := openLockFile(dir, bindingLockFileName, nil)
	if err != nil {
		t.Fatalf("second opener: %v", err)
	}
	defer func() { _ = second.Close() }()
	close(resume)
	r := <-first
	if r.err != nil {
		t.Fatalf("first opener after losing the race: %v", r.err)
	}
	defer func() { _ = r.f.Close() }()
	a, errA := second.Stat()
	b, errB := r.f.Stat()
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		t.Fatalf("the two openers hold different files: %v %v", errA, errB)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".new-") {
			t.Fatalf("a temporary lock file was left behind: %s", e.Name())
		}
	}
}

func TestLockBindingWithoutHardLinks2238(t *testing.T) {
	if mask := os.Getenv("HADRON_TEST_UMASK_PROBE_2238_NOLINK"); mask != "" {
		var m int
		if _, err := fmt.Sscanf(mask, "%o", &m); err != nil {
			t.Fatal(err)
		}
		syscall.Umask(m)
		lockLinkHook = func() error { return unix.ENOTSUP }
		defer func() { lockLinkHook = nil }()
		d := os.Getenv("HADRON_TEST_UMASK_DIR")
		for i := range 2 {
			release, err := LockBinding(d, time.Second)
			if err != nil {
				t.Fatalf("lock %d: %v", i+1, err)
			}
			release()
		}
		return
	}
	d := gitTree(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockBindingWithoutHardLinks2238$")
	cmd.Env = append(os.Environ(), "HADRON_TEST_UMASK_PROBE_2238_NOLINK=277", "HADRON_TEST_UMASK_DIR="+d)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("two locks without hard links: %v\n%s", err, out)
	}
	st, err := os.Stat(filepath.Join(d, ".git", bindingLockFileName))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode: %v %v", st, err)
	}
}
