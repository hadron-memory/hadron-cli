//go:build !windows

package team

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

// cli#2238: the binding lock file is created through O_CREATE, whose mode is
// 0600 &^ umask. Under umask 0277 it was r-------- (and under 0777 mode 000):
// the creating bind held it open read-write, but the NEXT bind's O_RDWR open
// failed "permission denied". The mode is exact whatever the umask. The umask
// is process wide, so the probe runs in a subprocess of this test binary.
func TestBindingLockFileModeIgnoresTheUmask2238(t *testing.T) {
	if mask := os.Getenv("HADRON_TEST_UMASK_PROBE_2238"); mask != "" {
		var m int
		if _, err := fmt.Sscanf(mask, "%o", &m); err != nil {
			t.Fatal(err)
		}
		syscall.Umask(m)
		if dir := os.Getenv("HADRON_TEST_UMASK_DIR"); dir != "" {
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
		}
		// A second opener arriving while the lock is being created must never
		// see it in a mode it cannot open: it either does not exist yet or it
		// is already exact (the create-then-chmod window, Julius's P2).
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
		// Two binds in the same directory: the second opens the lock the
		// first created.
		for i := range 2 {
			if _, err := writeBinding(context.Background(), &binding{SessionID: "s1", WorkerID: "wkr1", WorkerName: "Iris"}); err != nil {
				t.Fatalf("bind %d: %v", i+1, err)
			}
		}
		return
	}
	// A plain directory under umask 0777 is refused earlier and on purpose
	// (the new cache directory cannot be opened, cli#1969), so 0777 is a
	// git-dir case only.
	for _, mask := range []string{"277", "777", "022"} {
		if mask != "777" {
			t.Run("plain directory/"+mask, func(t *testing.T) {
				dir := plainDir1391(t)
				runLockProbe2238(t, mask, dir, "")
				assertLockMode2238(t, filepath.Join(dir, plainDirCacheName, bindingLockFileName))
			})
		}
		t.Run("git dir/"+mask, func(t *testing.T) {
			gitDir := t.TempDir()
			work := t.TempDir()
			runLockProbe2238(t, mask, work, gitDir)
			assertLockMode2238(t, filepath.Join(gitDir, bindingLockFileName))
		})
	}
}

func runLockProbe2238(t *testing.T, mask, dir, gitDir string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBindingLockFileModeIgnoresTheUmask2238$")
	env := append(os.Environ(), "HADRON_TEST_UMASK_PROBE_2238="+mask, "HADRON_TEST_UMASK_DIR="+dir)
	if gitDir == "" {
		// A PATH with no git: the directory reads as plain.
		env = append(env, "PATH="+t.TempDir(), GitDirEnv+"=")
	} else {
		env = append(env, GitDirEnv+"="+gitDir)
	}
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("two binds under umask %s: %v\n%s", mask, err, out)
	}
}

func assertLockMode2238(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("lock file mode = %v, want 0600 whatever the umask", st.Mode().Perm())
	}
	// The private temporary the lock is published from never lingers.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".new-") {
			t.Fatalf("a temporary lock file was left behind: %s", e.Name())
		}
	}
}

// An existing lock is opened as it is: only a lock this bind CREATED is set to
// 0600 (a file another uid made could not be chmodded by us anyway).
func TestBindingLockFileExistingModeIsKept2238(t *testing.T) {
	gitDir := t.TempDir()
	t.Setenv(GitDirEnv, gitDir)
	lock := filepath.Join(gitDir, bindingLockFileName)
	if err := os.WriteFile(lock, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lock, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := writeBinding(context.Background(), &binding{SessionID: "s1", WorkerID: "wkr1", WorkerName: "Iris"}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(lock); err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("an existing lock's mode changed: %v %v", st, err)
	}
}

// Julius's P3: two production openers race through creation. The first is
// paused after it created its private temporary; the second opens, publishes
// and returns; the first then resumes, its link finds the name taken, and it
// reopens the winner's file. Both succeed and share one inode, and no
// temporary is left.
func TestBindingLockCompetingCreators2238(t *testing.T) {
	dir := t.TempDir()
	h := cacheHandle{loc: bindingLoc{dir: dir}}
	paused, resume := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
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
	done := false
	// However the test exits, the paused first opener is released and its
	// descriptor closed, so a failure strands nothing.
	defer func() {
		release()
		if !done {
			if r := <-first; r.f != nil {
				_ = r.f.Close()
			}
		}
	}()
	go func() { f, err := h.openLock(); first <- result{f, err} }()
	<-paused
	second, err := h.openLock()
	if err != nil {
		t.Fatalf("second opener: %v", err)
	}
	defer func() { _ = second.Close() }()
	release()
	r := <-first
	done = true
	if r.err != nil {
		t.Fatalf("first opener after losing the race: %v", r.err)
	}
	defer func() { _ = r.f.Close() }()
	a, errA := second.Stat()
	b, errB := r.f.Stat()
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		t.Fatalf("the two openers hold different files: %v %v", errA, errB)
	}
	assertLockMode2238(t, filepath.Join(dir, bindingLockFileName))
}

// Julius's P2 on the delta: a filesystem without hard links (FAT/exFAT, some
// network mounts) still gets a lock. The publish step is made to fail the way
// such a filesystem fails, under umask 0277; two binds still succeed and the
// lock is exactly 0600.
func TestBindingLockWithoutHardLinks2238(t *testing.T) {
	if mask := os.Getenv("HADRON_TEST_UMASK_PROBE_2238_NOLINK"); mask != "" {
		var m int
		if _, err := fmt.Sscanf(mask, "%o", &m); err != nil {
			t.Fatal(err)
		}
		syscall.Umask(m)
		lockLinkHook = func() error { return &os.LinkError{Op: "link", Err: syscall.ENOTSUP} }
		defer func() { lockLinkHook = nil }()
		for i := range 2 {
			if _, err := writeBinding(context.Background(), &binding{SessionID: "s1", WorkerID: "wkr1", WorkerName: "Iris"}); err != nil {
				t.Fatalf("bind %d: %v", i+1, err)
			}
		}
		return
	}
	gitDir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBindingLockWithoutHardLinks2238$")
	cmd.Env = append(os.Environ(), "HADRON_TEST_UMASK_PROBE_2238_NOLINK=277", GitDirEnv+"="+gitDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("two binds without hard links: %v\n%s", err, out)
	}
	assertLockMode2238(t, filepath.Join(gitDir, bindingLockFileName))
}

// Julius's P3 on the delta: only "this filesystem cannot hard-link" falls
// back to a direct create. A real fault surfaces, and leaves no lock and no
// temporary behind.
func TestBindingLockRealLinkFaultSurfaces2238(t *testing.T) {
	dir := t.TempDir()
	h := cacheHandle{loc: bindingLoc{dir: dir}}
	lockLinkHook = func() error { return &os.LinkError{Op: "link", Err: syscall.EIO} }
	defer func() { lockLinkHook = nil }()
	f, err := h.openLock()
	if err == nil {
		_ = f.Close()
		t.Fatal("an EIO from the publish step was swallowed by the fallback")
	}
	if !errors.Is(err, syscall.EIO) {
		t.Fatalf("the original EIO did not reach the caller: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("a failed publish left files behind: %v %v", entries, err)
	}
}

// If setting the mode fails in the fallback, the error is reported with how to
// clear the file, and the name is NOT removed: deleting a name on a guess about
// which inode it still names could delete a replacement (Julius's P2 on the
// second delta).
func TestBindingLockFallbackChmodFailureIsReportedNotRemoved2238(t *testing.T) {
	dir := t.TempDir()
	h := cacheHandle{loc: bindingLoc{dir: dir}}
	lockLinkHook = func() error { return &os.LinkError{Op: "link", Err: syscall.ENOTSUP} }
	boom := errors.New("chmod refused")
	lockFallbackChmodHook = func() error { return boom }
	defer func() { lockLinkHook, lockFallbackChmodHook = nil, nil }()
	f, err := h.openLock()
	if err == nil {
		_ = f.Close()
		t.Fatal("a failed mode change was ignored")
	}
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "remove "+filepath.Join(dir, bindingLockFileName)) || !strings.Contains(err.Error(), lockUsersStopped2327) {
		t.Fatalf("the error does not carry the cause and the remedy: %v", err)
	}
	if _, serr := os.Lstat(filepath.Join(dir, bindingLockFileName)); serr != nil {
		t.Fatalf("the name was removed on a guess: %v", serr)
	}
}

// Julius's P2 on the delta: a creator paused inside the fallback's window (the
// lock exists, mode not yet set, umask 0277) is waited for by a second opener,
// which succeeds once the creator finishes. The umask is set for this test only.
func TestBindingLockFallbackPausedCreatorIsWaitedFor2238(t *testing.T) {
	dir := t.TempDir()
	h := cacheHandle{loc: bindingLoc{dir: dir}}
	old := syscall.Umask(0o277)
	defer syscall.Umask(old)
	lockLinkHook = func() error { return &os.LinkError{Op: "link", Err: syscall.ENOTSUP} }
	paused, resume := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	var first atomic.Bool
	lockFallbackChmodHook = func() error {
		if first.CompareAndSwap(false, true) {
			close(paused)
			<-resume
		}
		return nil
	}
	defer func() { lockLinkHook, lockFallbackChmodHook = nil, nil }()
	type result struct {
		f   *os.File
		err error
	}
	firstRes := make(chan result, 1)
	go func() { f, err := h.openLock(); firstRes <- result{f, err} }()
	defer func() {
		release()
		if r := <-firstRes; r.f != nil {
			_ = r.f.Close()
		}
	}()
	<-paused
	// The lock exists in a mode the second opener cannot open until the
	// creator finishes: release the creator only once the second opener has
	// really met that mode and is retrying.
	if os.Geteuid() == 0 {
		t.Skip("root opens the restrictive file at once, so no retry happens")
	}
	var retried atomic.Bool
	lockPermRetryHook = func() { retried.Store(true); release() }
	defer func() { lockPermRetryHook = nil }()
	second, err := h.openLock()
	if !retried.Load() {
		t.Fatal("the second opener never retried: the test did not exercise the window")
	}
	if err != nil {
		t.Fatalf("second opener should wait out the creator: %v", err)
	}
	_ = second.Close()
}

// #2327: the manual-delete hint must say every lock user stays stopped for the
// whole deletion (a point-in-time "none is running" lets a later caller lock a
// different inode), and the EPERM comment must not claim a policy denial fails
// the direct create too.
const lockUsersStopped2327 = "stays stopped for the whole deletion"

func TestLockWordingSources2327(t *testing.T) {
	for _, f := range []string{"state_linkerr_unix.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if strings.Contains(strings.ReplaceAll(src, "\n// ", " "), "fails the direct create too") {
			t.Errorf("%s still claims a policy denial fails the direct create too", f)
		}
		if !strings.Contains(strings.ReplaceAll(src, "\n// ", " "), "operation-specific denial") {
			t.Errorf("%s: the EPERM comment does not say EPERM can be an operation-specific denial", f)
		}
	}
}
