//go:build !windows

package hostfacts

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// cli#3285 (and the blind review N2 of cli#3283): C3 (cli#3237) executes the program an accounts entry names,
// so the loader refuses a file that anyone but the user could have written or redirected: a symlink at the
// file itself, a file another user owns, and a directory above it that is group- or world-writable (not
// sticky) or owned by another user, or a symlink on the way that another user owns. Each refusal names the
// file, the offending path and the fix.

const okAccounts3285 = "accounts:\n  work:\n    family: claude\n"

// trustedRoot3285 is a directory whose ancestors pass the walk: the scratch TMPDIR on a shared host can be
// group-writable, which is exactly what the walk refuses, so these pins build under /tmp (sticky).
func trustedRoot3285(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "accounts3285-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func put3285(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func wantRefused3285(t *testing.T, path string, wants ...string) {
	t.Helper()
	a, err := LoadAccounts(path)
	if err == nil {
		t.Fatalf("LoadAccounts(%s) = %+v, want a refusal containing %q", path, a, wants)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal must name the file %s: %v", path, err)
	}
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the refusal must contain %q: %v", w, err)
		}
	}
	if a.Present || len(a.Entries) != 0 {
		t.Errorf("a refused file yields no entries: %+v", a)
	}
}

func TestATrustedFileStillLoads3285(t *testing.T) {
	p := filepath.Join(trustedRoot3285(t), "hadron", "accounts.yaml")
	put3285(t, p, okAccounts3285, 0o600)
	if a, err := LoadAccounts(p); err != nil || a.Entries["work"].Family != "claude" {
		t.Fatalf("a 0600 file in a 0700 directory the user owns loads: %+v, %v", a, err)
	}
}

func TestASymlinkToA0600FileIsRefused3285(t *testing.T) {
	root := trustedRoot3285(t)
	target := filepath.Join(root, "elsewhere", "real.yaml")
	put3285(t, target, okAccounts3285, 0o600)
	p := filepath.Join(root, "hadron", "accounts.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	wantRefused3285(t, p, "symlink")
}

func TestANonRegularFileIsRefused3285(t *testing.T) {
	p := filepath.Join(trustedRoot3285(t), "hadron", "accounts.yaml")
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	wantRefused3285(t, p, "not a regular file")
}

func TestAGroupWritableConfigDirectoryIsRefused3285(t *testing.T) {
	dir := filepath.Join(trustedRoot3285(t), "hadron")
	p := filepath.Join(dir, "accounts.yaml")
	put3285(t, p, okAccounts3285, 0o600)
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	wantRefused3285(t, p, dir, "writable by group or others", "chmod go-w "+dir)
}

func TestAWorldWritableAncestorIsRefused3285(t *testing.T) {
	root := trustedRoot3285(t)
	up := filepath.Join(root, "config")
	p := filepath.Join(up, "hadron", "accounts.yaml")
	put3285(t, p, okAccounts3285, 0o600)
	if err := os.Chmod(up, 0o777); err != nil {
		t.Fatal(err)
	}
	wantRefused3285(t, p, up, "writable by group or others")
}

func TestAStickyAncestorIsAccepted3285(t *testing.T) {
	root := trustedRoot3285(t)
	up := filepath.Join(root, "shared")
	p := filepath.Join(up, "hadron", "accounts.yaml")
	put3285(t, p, okAccounts3285, 0o600)
	if err := os.Chmod(up, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAccounts(p); err != nil {
		t.Fatalf("a sticky directory (like /tmp) lets others add names but not replace ours: %v", err)
	}
}

// owner3285 makes every path for which pick is true read as owned by uid 4242 (a test cannot chown).
func owner3285(t *testing.T, pick func(path string) bool) {
	t.Helper()
	prev := accountsOwnerOf
	accountsOwnerOf = func(path string, fi os.FileInfo) (uint32, bool) {
		if pick(path) {
			return 4242, true
		}
		return prev(path, fi)
	}
	t.Cleanup(func() { accountsOwnerOf = prev })
}

func TestAFileAnotherUserOwnsIsRefused3285(t *testing.T) {
	p := filepath.Join(trustedRoot3285(t), "hadron", "accounts.yaml")
	put3285(t, p, okAccounts3285, 0o600)
	owner3285(t, func(path string) bool { return path == p })
	wantRefused3285(t, p, "uid 4242")
}

func TestAConfigDirectoryAnotherUserOwnsIsRefused3285(t *testing.T) {
	dir := filepath.Join(trustedRoot3285(t), "hadron")
	p := filepath.Join(dir, "accounts.yaml")
	put3285(t, p, okAccounts3285, 0o600)
	owner3285(t, func(path string) bool { return path == dir })
	wantRefused3285(t, p, dir, "uid 4242")
}

func TestASymlinkedConfigDirectoryIsJudgedWhereItLeads3285(t *testing.T) {
	root := trustedRoot3285(t)
	real := filepath.Join(root, "real-hadron")
	put3285(t, filepath.Join(real, "accounts.yaml"), okAccounts3285, 0o600)
	link := filepath.Join(root, "hadron")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(link, "accounts.yaml")
	if _, err := LoadAccounts(p); err != nil {
		t.Fatalf("a config directory symlinked within the user's own tree is fine: %v", err)
	}
	// ... but the directory it leads to is judged: out of the user's tree (another owner) is refused,
	owner3285(t, func(path string) bool { return path == real })
	wantRefused3285(t, p, real, "uid 4242")
}

func TestASymlinkAnotherUserOwnsOnTheWayIsRefused3285(t *testing.T) {
	root := trustedRoot3285(t)
	real := filepath.Join(root, "real-hadron")
	put3285(t, filepath.Join(real, "accounts.yaml"), okAccounts3285, 0o600)
	link := filepath.Join(root, "hadron")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	owner3285(t, func(path string) bool { return path == link })
	wantRefused3285(t, filepath.Join(link, "accounts.yaml"), link, "uid 4242", "symlink")
}

// The owner seam must be the real owner by default: the file a test wrote is the test's own.
func TestTheDefaultOwnerIsTheFilesOwner3285(t *testing.T) {
	p := filepath.Join(trustedRoot3285(t), "f")
	put3285(t, p, "x", 0o600)
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	uid, ok := accountsOwnerOf(p, fi)
	if !ok || uid != fi.Sys().(*syscall.Stat_t).Uid || int(uid) != os.Geteuid() {
		t.Fatalf("accountsOwnerOf = %d, %v; want this process's uid %d", uid, ok, os.Geteuid())
	}
}
