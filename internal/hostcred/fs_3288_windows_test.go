//go:build windows

package hostcred

import (
	"os"
	"path/filepath"
	"testing"
)

// hadron-cli#3288 F12: on Windows the store file is opened first and the opened handle's attributes are checked:
// a directory or a reparse point at the path is refused, a regular file is read.
func TestOpenNoFollow3288ChecksTheOpenedHandle(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "reg")
	if err := os.WriteFile(reg, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openNoFollow(reg)
	if err != nil {
		t.Fatalf("a regular file opens: %v", err)
	}
	_ = f.Close()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if f, err := openNoFollow(sub); err == nil {
		_ = f.Close()
		t.Fatal("a directory is refused")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(reg, link); err != nil {
		t.Skipf("no symlink privilege here: %v", err)
	}
	if f, err := openNoFollow(link); err == nil || !isSymlinkErr(err) {
		if f != nil {
			_ = f.Close()
		}
		t.Fatalf("a symlink is refused as one: %v", err)
	}
}
