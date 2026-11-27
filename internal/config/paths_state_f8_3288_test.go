package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// hadron-cli#3288 F8 (folded into C3, cli#3237, the first writer under hosts/<id>): MkdirAll keeps whatever an
// existing directory already is, so EnsureHostStateDir checks what it returns: an existing hosts/<id> (and
// hosts) wider than 0700 is tightened, a symlink or a non-directory there is refused, and on Windows a reserved
// device name (CON, NUL, COM1, ...) or a trailing dot or space, which Windows drops, never names a state path.

func TestEnsureHostStateDirTightensAnExistingLooseDirectory3288(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes do not carry this meaning on Windows")
	}
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)
	hosts := filepath.Join(base, "hadron", "hosts")
	dir := filepath.Join(hosts, "box1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{hosts, dir} {
		if err := os.Chmod(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	got, err := EnsureHostStateDir("box1")
	if err != nil || got != dir {
		t.Fatalf("EnsureHostStateDir = %q, %v", got, err)
	}
	for _, d := range []string{hosts, dir} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: mode %o, %v; want tightened to 700", d, fi.Mode().Perm(), err)
		}
	}
}

func TestEnsureHostStateDirRefusesASymlinkOrAFile3288(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink needs a privilege on Windows")
	}
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)
	hosts := filepath.Join(base, "hadron", "hosts")
	if err := os.MkdirAll(hosts, 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(hosts, "box1")); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureHostStateDir("box1"); err == nil || !strings.Contains(err.Error(), "symlink") || !strings.Contains(err.Error(), filepath.Join(hosts, "box1")) {
		t.Errorf("a symlinked hosts/<id> must be refused naming it: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hosts, "box2"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureHostStateDir("box2"); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("a file at hosts/<id> must be refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "x")); err == nil {
		t.Error("nothing is written through the link")
	}
}

func TestWindowsReservedNamesNeverNameAStatePath3288(t *testing.T) {
	for id, want := range map[string]bool{
		"CON": true, "con": true, "Nul": true, "prn": true, "aux": true, "COM1": true, "com9": true, "LPT1": true, "lpt9": true,
		"con.box": true, "NUL.x.y": true, "conin$": false, "box.": true, "box1": false, "console": false, "com10": false,
		"lpt": false, "orca1": false, "a.b": false, "COM0": false,
	} {
		if got := windowsUnsafeName(id); got != want {
			t.Errorf("windowsUnsafeName(%q) = %v, want %v", id, got, want)
		}
	}
	_, err := HostStateDir("nul")
	if runtime.GOOS == "windows" {
		if !errors.Is(err, ErrBadHostID) {
			t.Errorf("a reserved name on Windows: %v", err)
		}
	} else if err != nil {
		t.Errorf("a reserved Windows name is a plain name on Unix: %v", err)
	}
}

// hadron-cli#3301 (BLIND P3-7): a symlinked hosts is refused before MkdirAll, which would otherwise create <id>
// inside the link's target first.
func TestEnsureHostStateDirCreatesNothingThroughASymlinkedHosts3301(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink needs a privilege on Windows")
	}
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)
	if err := os.MkdirAll(filepath.Join(base, "hadron"), 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	hosts := filepath.Join(base, "hadron", "hosts")
	if err := os.Symlink(elsewhere, hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureHostStateDir("box1"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("a symlinked hosts must be refused: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(elsewhere, "box1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("nothing is created through the link: %v", err)
	}
}
