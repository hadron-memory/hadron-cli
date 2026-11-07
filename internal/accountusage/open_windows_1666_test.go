//go:build windows

package accountusage

import (
	"os"
	"path/filepath"
	"testing"
)

// Windows execution is intentionally not claimed by Linux CI. Run this test
// on Windows with Developer Mode or symlink privilege for the reparse control.
func TestOpenRollout1666WindowsReparse(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "rollout.jsonl")
	if err := os.WriteFile(target, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openRollout(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("Windows symlink privilege unavailable: %v", err)
	}
	f, err = openRollout(link)
	if f != nil {
		_ = f.Close()
	}
	if err == nil {
		t.Fatal("opened reparse point")
	}
}
