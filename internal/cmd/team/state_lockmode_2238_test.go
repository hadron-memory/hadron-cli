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
