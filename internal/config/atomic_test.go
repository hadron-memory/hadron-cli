package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A pre-existing loose-mode file is replaced by one at the requested perm —
// the #117 fix (os.WriteFile left an existing 0644 file group/world-readable).
func TestWriteFileAtomicTightensLoosePerms(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("new-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("perms = %o, want 0600", perm)
	}
	if b, _ := os.ReadFile(p); string(b) != "new-secret" {
		t.Errorf("content = %q", b)
	}
}

// The write refuses a symlink at the target and leaves the link's target
// untouched (no writing a secret through an attacker-planted symlink).
func TestWriteFileAtomicRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "auth.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := WriteFileAtomic(link, []byte("secret"), 0o600); err == nil {
		t.Fatal("expected a symlink refusal error")
	}
	if b, _ := os.ReadFile(target); string(b) != "original" {
		t.Errorf("symlink target must be untouched, got %q", b)
	}
}
