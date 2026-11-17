//go:build !windows

package cmdutil

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ReadSecretFile (#1657) shares readPassphraseFile's rule and wording: refuse a
// group- or other-accessible file, a non-regular file and an empty path, name
// the flag and never the path, and never block on a FIFO.
func TestReadSecretFileRule1657(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("the-key\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if data, err := ReadSecretFile(write("ok", 0o600), "--data-key-file"); err != nil || string(data) != "the-key\n" {
		t.Fatalf("0600: %q %v", data, err)
	}
	bad := write("open", 0o640)
	_, err := ReadSecretFile(bad, "--data-key-file")
	if exitcode.FromError(err) != exitcode.Usage || err.Error() != "--data-key-file is accessible by group or others (mode 0640); run chmod 600 on it. Nothing was sent" {
		t.Fatalf("0640: %v", err)
	}
	if strings.Contains(err.Error(), dir) {
		t.Fatalf("the path must not be echoed: %v", err)
	}
	if _, err := ReadSecretFile("  ", "--data-key-file"); err == nil || !strings.Contains(err.Error(), "--data-key-file is empty") {
		t.Fatalf("blank path: %v", err)
	}
	// The unlock wording is unchanged by the shared body.
	if _, err := readPassphraseFile(bad, 2); err == nil || err.Error() != "--unlock-passphrase-file #2 is accessible by group or others (mode 0640); run chmod 600 on it. Nothing was sent" {
		t.Fatalf("unlock wording moved: %v", err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	for _, path := range []string{fifo, dir} {
		done := make(chan error, 1)
		go func() { _, err := ReadSecretFile(path, "--data-key-file"); done <- err }()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "--data-key-file is not a regular file") {
				t.Fatalf("%s: %v", path, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: blocked on a non-regular file", path)
		}
	}
}
