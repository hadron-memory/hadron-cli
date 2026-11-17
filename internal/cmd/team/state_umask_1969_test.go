//go:build !windows

package team

import (
	"context"
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
	if os.Getenv("HADRON_TEST_UMASK_PROBE") == "1" {
		syscall.Umask(0o777)
		dir := os.Getenv("HADRON_TEST_UMASK_DIR")
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		path, err := writeBinding(context.Background(), &binding{SessionID: "s1", WorkerID: "wkr1", WorkerName: "Iris"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
		return
	}
	dir := plainDir1391(t)
	_ = dir
	cmd := exec.Command(os.Args[0], "-test.run=^TestPlainDir1969BindingModeIgnoresTheUmask$")
	// A PATH with no git: the probe has no GitDirRunner seam in a new process,
	// so it finds no git and the directory reads as plain (exec.ErrNotFound).
	cmd.Env = append(os.Environ(), "HADRON_TEST_UMASK_PROBE=1", "HADRON_TEST_UMASK_DIR="+dir, "PATH="+t.TempDir(), GitDirEnv+"=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("probe: %v\n%s", err, out)
	}
	st, err := os.Stat(filepath.Join(dir, plainDirCacheName, bindingFileName))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("the binding must be 0600 whatever the umask, got %v", st.Mode().Perm())
	}
	if strings.Contains(st.Mode().String(), "?") {
		t.Fatal(st.Mode())
	}
}
