//go:build !windows

package team

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Jake's LOCAL P2 on #2229 (1e622fe4): a binding written through the held
// root must carry exactly the requested mode whatever the umask; the old
// config.WriteFileAtomic chmodded its temp file, and the root-based write
// regressed to umask & 0o600 (0000 under umask 0777). The umask is process
// wide, so the probe runs in a subprocess of this test binary.
func TestPlainDir1969BindingModeIgnoresTheUmask(t *testing.T) {
	if mask := os.Getenv("HADRON_TEST_UMASK_PROBE"); mask != "" {
		var m int
		if _, err := fmt.Sscanf(mask, "%o", &m); err != nil {
			t.Fatal(err)
		}
		syscall.Umask(m)
		if err := os.Chdir(os.Getenv("HADRON_TEST_UMASK_DIR")); err != nil {
			t.Fatal(err)
		}
		if _, err := writeBinding(context.Background(), &binding{SessionID: "s1", WorkerID: "wkr1", WorkerName: "Iris"}); err != nil {
			t.Fatal(err)
		}
		return
	}
	// Permissive (022, 000) and owner-write-stripping (277) masks: the final
	// modes are exactly 0700 and 0600. A mask that removes the owner's read or
	// search bit from a new directory (0400, 0100) cannot be opened and is
	// refused, not worked around by touching the process umask.
	for _, mask := range []string{"022", "000", "277"} {
		t.Run(mask, func(t *testing.T) {
			dir := plainDir1391(t)
			cmd := exec.Command(os.Args[0], "-test.run=^TestPlainDir1969BindingModeIgnoresTheUmask$")
			// A PATH with no git: the probe has no GitDirRunner seam in a new
			// process, so it finds no git and the directory reads as plain.
			cmd.Env = append(os.Environ(), "HADRON_TEST_UMASK_PROBE="+mask, "HADRON_TEST_UMASK_DIR="+dir, "PATH="+t.TempDir(), GitDirEnv+"=")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("probe: %v\n%s", err, out)
			}
			cache := filepath.Join(dir, plainDirCacheName)
			if st, err := os.Stat(cache); err != nil || st.Mode().Perm() != 0o700 {
				t.Fatalf("the cache directory must be 0700: %v %v", st, err)
			}
			if st, err := os.Stat(filepath.Join(cache, bindingFileName)); err != nil || st.Mode().Perm() != 0o600 {
				t.Fatalf("the binding must be 0600: %v %v", st, err)
			}
		})
	}
}

// Wolfgang's ruling on #2229: the process umask is never changed (Go runs
// goroutines on several threads, so it would leak into unrelated creates).
func TestPlainDir1969TheProcessUmaskIsNeverChanged(t *testing.T) {
	for _, name := range []string{"state.go", "state_securedir_unix.go", "state_securedir_windows.go", "session_reset_context.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "Umask(") {
			t.Errorf("%s changes the process umask", name)
		}
	}
}
