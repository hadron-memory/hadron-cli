package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// hadron-cli#3239: the state directory for runtime state the CLI owns.
func TestHostStateDirHonoursXDGAndRefusesNonIDs(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)
	got, err := HostStateDir("box1")
	if err != nil || got != filepath.Join(base, "hadron", "hosts", "box1") {
		t.Fatalf("HostStateDir(box1) = %q, %v", got, err)
	}
	for _, bad := range []string{"", "..", ".", "a/b", `a\b`, "../x", "-lead"} {
		if _, err := HostStateDir(bad); !errors.Is(err, ErrBadHostID) {
			t.Errorf("HostStateDir(%q) = %v, want ErrBadHostID", bad, err)
		}
	}
	_, err = HostStateDir("orca1:a")
	if runtime.GOOS == "windows" {
		if !errors.Is(err, ErrBadHostID) {
			t.Errorf("a ':' id on Windows: %v", err)
		}
	} else if err != nil {
		t.Errorf("a ':' id on Unix: %v", err)
	}
}

func TestEnsureHostStateDirCreates0700(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, err := EnsureHostStateDir("box1")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("not created: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Errorf("mode %o, want 700", fi.Mode().Perm())
	}
}
