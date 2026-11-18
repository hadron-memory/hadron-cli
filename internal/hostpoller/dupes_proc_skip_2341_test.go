//go:build !windows

package hostpoller

import (
	"os"
	"path/filepath"
	"testing"
)

// cli#2341: dupes_unix.go builds for every non-windows target, but its live
// tests read Linux /proc semantics. On a host without them (darwin) those
// tests must SKIP, not fail on an empty scan. The gate is a pure function of
// the proc root so it is testable here without a darwin host.
// procReadable2341 reports whether root looks like a Linux /proc: its self/cwd
// entry is a symlink we can read. Darwin has no /proc; a stray directory or a
// regular file named cwd is not /proc semantics either.
func procReadable2341(root string) bool {
	_, err := os.Readlink(filepath.Join(root, "self", "cwd"))
	return err == nil
}

// requireProc2341 skips a live test on a host without Linux /proc semantics.
func requireProc2341(t *testing.T) {
	t.Helper()
	if !procReadable2341("/proc") {
		t.Skip("no readable /proc/self/cwd on this host (not Linux); the live dupes scan needs /proc")
	}
}

func TestProcReadableDetectsALinuxProcRoot2341(t *testing.T) {
	t.Run("absent root", func(t *testing.T) {
		if procReadable2341(filepath.Join(t.TempDir(), "no-such-proc")) {
			t.Fatal("a missing /proc must not count as readable")
		}
	})
	t.Run("empty root", func(t *testing.T) {
		if procReadable2341(t.TempDir()) {
			t.Fatal("a root with no self/cwd link must not count as readable")
		}
	})
	t.Run("a root with a readable self/cwd link", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "self"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), filepath.Join(root, "self", "cwd")); err != nil {
			t.Fatal(err)
		}
		if !procReadable2341(root) {
			t.Fatal("a root exposing self/cwd must count as readable")
		}
	})
	t.Run("a regular file named cwd is not a link", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "self"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "self", "cwd"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if procReadable2341(root) {
			t.Fatal("only a symlink proves /proc/<pid>/cwd semantics")
		}
	})
}
