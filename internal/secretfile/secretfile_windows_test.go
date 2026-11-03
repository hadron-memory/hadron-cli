//go:build windows

package secretfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenRefusesSymlinkOnWindows pins that noFollow = 0 does not drop the
// guard: CREATE_NEW with FILE_FLAG_OPEN_REPARSE_POINT refuses a symlink at the
// destination, dangling or not, instead of writing through it (#1324).
func TestOpenRefusesSymlinkOnWindows(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "key")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symlink here (needs Developer Mode or the privilege): %v", err)
	}
	for _, force := range []bool{false, true} {
		p, err := Open(link, force)
		if force {
			// --force opens a fresh temp file and renames it over the link, which
			// replaces the link itself; the target must still never be written.
			if err != nil {
				t.Fatalf("force: Open: %v", err)
			}
			if err := p.Commit([]byte("secret")); err != nil {
				t.Fatalf("force: Commit: %v", err)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("force: symlink target was written: %v", err)
			}
			continue
		}
		if !errors.Is(err, ErrExists) {
			t.Fatalf("Open(symlink) = %v, want ErrExists", err)
		}
		if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("symlink target was created: %v", err)
		}
	}
}
