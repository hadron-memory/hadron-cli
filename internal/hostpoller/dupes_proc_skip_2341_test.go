//go:build !windows

package hostpoller

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#2341: dupes_unix.go builds for every non-windows target, but its live
// tests read Linux /proc semantics. On a host without them (darwin) those
// tests must SKIP, not fail on an empty scan. The gate is a pure function of
// the proc root so it is testable here without a darwin host.
// procSelfCwd reads root/self/cwd and returns the read error, nil when root
// looks like a Linux /proc: its self/cwd entry is a symlink we can read. Darwin
// has no /proc; a stray directory or a regular file named cwd is not /proc
// semantics either.
func procSelfCwd(root string) error {
	_, err := os.Readlink(filepath.Join(root, "self", "cwd"))
	return err
}

// procReadable reports whether root exposes Linux /proc semantics.
func procReadable(root string) bool { return procSelfCwd(root) == nil }

// procRoot is the real proc mount; call sites pass it so tests can pass another.
const procRoot = "/proc"

// skipper is the part of *testing.T requireProc uses, so the skip path can be
// observed with a fake.
type skipper interface {
	Helper()
	Skipf(format string, args ...any)
}

// requireProc skips a live test on a host without Linux /proc semantics. The
// message states what was observed, with the error, not a guess about the OS.
func requireProc(t skipper, root string) {
	t.Helper()
	if err := procSelfCwd(root); err != nil {
		t.Skipf("%s is unreadable (%v); the live dupes scan needs Linux /proc semantics", filepath.Join(root, "self", "cwd"), err)
	}
}

type recordingSkipper struct {
	skipped bool
	message string
}

func (*recordingSkipper) Helper() {}
func (r *recordingSkipper) Skipf(format string, args ...any) {
	r.skipped = true
	r.message = fmt.Sprintf(format, args...)
}

func TestRequireProcSkipsWithTheObservation2391(t *testing.T) {
	t.Run("absent root skips and names the path and the error", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "no-such-proc")
		var r recordingSkipper
		requireProc(&r, root)
		want := filepath.Join(root, "self", "cwd")
		if !r.skipped || !strings.Contains(r.message, want+" is unreadable (") || !strings.Contains(r.message, "no such file") {
			t.Fatalf("skipped=%v message=%q, want the unreadable path with its error", r.skipped, r.message)
		}
		if strings.Contains(r.message, "not Linux") {
			t.Fatalf("the message guesses the OS instead of stating the observation: %q", r.message)
		}
	})
	t.Run("a regular file named cwd skips with the read error", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "self"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "self", "cwd"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		var r recordingSkipper
		requireProc(&r, root)
		if !r.skipped || !strings.Contains(r.message, "invalid argument") {
			t.Fatalf("skipped=%v message=%q, want a skip carrying the Readlink error", r.skipped, r.message)
		}
	})
	t.Run("a readable root does not skip", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "self"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), filepath.Join(root, "self", "cwd")); err != nil {
			t.Fatal(err)
		}
		var r recordingSkipper
		requireProc(&r, root)
		if r.skipped {
			t.Fatalf("a readable /proc must not skip: %q", r.message)
		}
	})
	t.Run("the call-site default is the real /proc", func(t *testing.T) {
		if procRoot != "/proc" {
			t.Fatalf("procRoot = %q", procRoot)
		}
	})
}

func TestProcReadableDetectsALinuxProcRoot2341(t *testing.T) {
	t.Run("absent root", func(t *testing.T) {
		if procReadable(filepath.Join(t.TempDir(), "no-such-proc")) {
			t.Fatal("a missing /proc must not count as readable")
		}
	})
	t.Run("empty root", func(t *testing.T) {
		if procReadable(t.TempDir()) {
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
		if !procReadable(root) {
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
		if procReadable(root) {
			t.Fatal("only a symlink proves /proc/<pid>/cwd semantics")
		}
	})
}
